// Package gitserver is the Git server: Git's smart HTTP protocol at /<org>/<repo>.git for clone, fetch and push, and
// SSH beside it, over the git CLI. Each request serves a throwaway repository built from one snapshot of the
// repository's refs and packs,
// its packs hard-linked from the pod's cache of the bucket. A push's pre-receive hook calls back into the server that
// started it, which uploads the push's pack, builds its manifest and records the push, in one transaction, before Git
// returns; the push worker does the rest later
// (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/git-hosting.md, "Git over HTTPS").
package gitserver

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/metric"
	"gocloud.dev/blob"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/git"
	"cobranch.dev/internal/services/repos"
)

// Authenticator resolves a personal token or an API key's secret, or an SSH key, to who it acts as: auth.Service.
type Authenticator interface {
	Authenticate(ctx context.Context, token string) (auth.Authenticated, error)
	AuthenticateSSHKey(ctx context.Context, wire []byte) (auth.Authenticated, error)
}

// Repositories finds the repository a Git URL names, or the internal call's ID, for a viewer who reads it:
// repos.Service.
type Repositories interface {
	Locate(ctx context.Context, v auth.User, orgSlug, slug string) (repos.Repository, []string, error)
	ByID(ctx context.Context, v auth.User, repoID uuid.UUID) (repos.Repository, []string, error)
}

// Snapshots reads what a fetch serves: git.Service.
type Snapshots interface {
	Snapshot(ctx context.Context, repoID uuid.UUID) (git.Snapshot, error)
}

// Recorder records a push before its client returns: git.Service.
type Recorder interface {
	Record(ctx context.Context, push git.Push) (git.Answer, error)
}

// Config is how the Git server runs.
type Config struct {
	// DataDir holds the pack cache, the throwaway repositories and the hook, on one filesystem, so a repository
	// hard-links packs from the cache. The server empties it when it starts, and keeps the directory itself, which may
	// be a volume's mount point.
	DataDir string
	// CacheSize is how many bytes of packs the cache keeps before it evicts the least recently used repositories'.
	CacheSize int64
	// MaxPushSize is the largest pack a push may send, in bytes.
	MaxPushSize int64
	// Hook is the command a push's pre-receive hook runs, such as the cobranch binary and git-hook; it reads the
	// push's refs on its standard input and the variables HookURLEnv and HookTokenEnv name.
	Hook []string
	// Git is the git CLI's path; "git" finds it on the PATH.
	Git string
	// Meter makes the server's metrics; nil takes the process's meter provider.
	Meter metric.MeterProvider
}

// The variables a push's hook reads to call back into its Git server.
const (
	HookURLEnv   = "COBRANCH_HOOK_URL"
	HookTokenEnv = "COBRANCH_HOOK_TOKEN"
)

// The directories under DataDir.
const (
	cacheDir    = "cache"
	incomingDir = ".incoming"
	workDir     = "work"
	hooksDir    = "hooks"
)

// Server serves Git's smart HTTP protocol over Cobranch's repositories.
type Server struct {
	cfg      Config
	auth     Authenticator
	repos    Repositories
	snaps    Snapshots
	recorder Recorder
	bucket   *blob.Bucket
	cache    *cache
	metrics  *metrics

	hookURL string
	hooks   *http.Server
	mu      sync.Mutex
	pushes  map[string]*pending
}

// New prepares a Git server: it empties cfg.DataDir, writes the hook there, and listens for hooks on a loopback
// port, until Close. ctx carries what its log lines name.
func New(ctx context.Context, cfg Config, a Authenticator, r Repositories, s Snapshots, recorder Recorder, bucket *blob.Bucket) (*Server, error) {
	if cfg.Git == "" {
		cfg.Git = "git"
	}
	if len(cfg.Hook) == 0 {
		return nil, errors.New("the Git server needs a hook command")
	}
	srv := &Server{cfg: cfg, auth: a, repos: r, snaps: s, recorder: recorder, bucket: bucket, pushes: map[string]*pending{},
		cache: newCache(filepath.Join(cfg.DataDir, cacheDir), cfg.CacheSize, bucket)}
	m, err := newMetrics(cfg.Meter, srv.cache)
	if err != nil {
		return nil, err
	}
	srv.metrics, srv.cache.metrics = m, m
	if err := srv.prepare(); err != nil {
		return nil, err
	}
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listening for hooks: %w", err)
	}
	srv.hookURL = "http://" + ln.Addr().String() + hookPath
	srv.hooks = &http.Server{Handler: srv.hookHandler(), ReadHeaderTimeout: 10 * time.Second}
	logCtx := context.WithoutCancel(ctx)
	go func() {
		if err := srv.hooks.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
			slog.ErrorContext(logCtx, "Serving hooks failed", "error", err)
		}
	}()
	return srv, nil
}

// prepare empties the data directory, left from a container that restarted, and writes the hook.
func (s *Server) prepare() error {
	left, err := os.ReadDir(s.cfg.DataDir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("reading %s: %w", s.cfg.DataDir, err)
	}
	for _, entry := range left {
		if err := os.RemoveAll(filepath.Join(s.cfg.DataDir, entry.Name())); err != nil {
			return fmt.Errorf("emptying %s: %w", s.cfg.DataDir, err)
		}
	}
	for _, dir := range []string{filepath.Join(cacheDir, incomingDir), workDir, hooksDir} {
		if err := os.MkdirAll(filepath.Join(s.cfg.DataDir, dir), 0o700); err != nil {
			return fmt.Errorf("making %s: %w", dir, err)
		}
	}
	quoted := make([]string, 0, len(s.cfg.Hook))
	for _, arg := range s.cfg.Hook {
		quoted = append(quoted, "'"+strings.ReplaceAll(arg, "'", `'\''`)+"'")
	}
	script := "#!/bin/sh\nexec " + strings.Join(quoted, " ") + "\n"
	if err := os.WriteFile(filepath.Join(s.cfg.DataDir, hooksDir, "pre-receive"), []byte(script), 0o700); err != nil {
		return fmt.Errorf("writing the hook: %w", err)
	}
	return nil
}

// Close stops listening for hooks.
func (s *Server) Close() error {
	if err := s.hooks.Close(); err != nil {
		return fmt.Errorf("closing the hooks' listener: %w", err)
	}
	return nil
}

// Handler serves Git's smart HTTP protocol at /<org>/<repo>.git (or without .git): the ref advertisement, a fetch and
// a push.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{org}/{repo}/info/refs", s.serve(s.infoRefs))
	mux.HandleFunc("POST /{org}/{repo}/git-upload-pack", s.serve(s.uploadPack))
	mux.HandleFunc("POST /{org}/{repo}/git-receive-pack", s.serve(s.receivePack))
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "Cobranch serves Git's smart HTTP protocol at /<org>/<repo>.git", http.StatusNotFound)
	})
	return mux
}

// request is one request for a repository, by someone who reads it.
type request struct {
	viewer auth.Authenticated
	repo   repos.Repository
	held   []string
}

// serve authenticates the request and finds its repository: 401 with a Basic challenge without a credential, or with
// one that acts as nobody, so Git asks for one and nobody learns what exists; 404 for a repository the credential
// can't read, as for one that isn't there.
func (s *Server) serve(next func(w http.ResponseWriter, r *http.Request, req request)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		viewer, err := s.authenticate(r)
		if errors.Is(err, auth.ErrUnauthenticated) {
			w.Header().Set("WWW-Authenticate", `Basic realm="Cobranch", charset="UTF-8"`)
			http.Error(w, "Cobranch needs a personal token, or an API key's secret, as the password", http.StatusUnauthorized)
			return
		}
		if err != nil {
			slog.ErrorContext(r.Context(), "Checking a Git client's credential failed", "error", err)
			http.Error(w, "the credential could not be checked", http.StatusInternalServerError)
			return
		}
		ctx := auth.WithViewer(r.Context(), viewer)
		repo, held, err := s.repos.Locate(ctx, viewer.User, r.PathValue("org"), strings.TrimSuffix(r.PathValue("repo"), ".git"))
		if errors.Is(err, repos.ErrNotFound) {
			http.Error(w, "Repository not found", http.StatusNotFound)
			return
		}
		if err != nil {
			slog.ErrorContext(ctx, "Finding a repository for Git failed", "error", err)
			http.Error(w, "the repository could not be found", http.StatusInternalServerError)
			return
		}
		next(w, r.WithContext(ctx), request{viewer: viewer, repo: repo, held: held})
	}
}

// authenticate resolves the request's credential: a token as the Basic password, with any user name, or as a bearer
// token. A session's token never works here, so no page can push for its visitor.
func (s *Server) authenticate(r *http.Request) (auth.Authenticated, error) {
	token := ""
	if _, password, ok := r.BasicAuth(); ok {
		token = password
	} else if scheme, bearer, ok := strings.Cut(r.Header.Get("Authorization"), " "); ok && strings.EqualFold(scheme, "Bearer") {
		token = strings.TrimSpace(bearer)
	}
	if token == "" || strings.HasPrefix(token, auth.PrefixSession) {
		return auth.Authenticated{}, auth.ErrUnauthenticated
	}
	a, err := s.auth.Authenticate(r.Context(), token)
	if err != nil {
		return auth.Authenticated{}, fmt.Errorf("authenticating: %w", err)
	}
	return a, nil
}
