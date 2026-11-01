package gitserver

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/git"
	"cobranch.dev/internal/services/repos"
)

// The services of Git's smart HTTP protocol.
const (
	uploadPack  = "git-upload-pack"
	receivePack = "git-receive-pack"
)

// pushOverhead is what a push's body holds beside its pack: its ref updates and capabilities.
const pushOverhead = 1 << 20

// gitProtocol is what a Git-Protocol header may say, as git http-backend passes it on.
var gitProtocol = regexp.MustCompile(`^[A-Za-z0-9=:._-]+$`)

// infoRefs advertises the repository's refs for a fetch or a push, from one snapshot.
func (s *Server) infoRefs(w http.ResponseWriter, r *http.Request, req request) {
	service := r.URL.Query().Get("service")
	switch service {
	case uploadPack:
	case receivePack:
		if !mayPush(w, req) {
			return
		}
	default:
		http.Error(w, "Cobranch serves Git's smart HTTP protocol only: ask for service git-upload-pack or git-receive-pack", http.StatusForbidden)
		return
	}
	env := s.protocolEnv(r, service)
	dir, remove, ok := s.serveWorkspace(w, r, req)
	if !ok {
		return
	}
	defer remove()
	w.Header().Set("Content-Type", "application/x-"+service+"-advertisement")
	noCache(w)
	// As git http-backend does, protocol v2's advertisement starts with its capabilities instead of the service.
	if len(env) == 0 {
		_, _ = io.WriteString(w, pktLine("# service="+service+"\n")+"0000") // a client gone away gets nothing more
	}
	s.run(r.Context(), w, nil, env, strings.TrimPrefix(service, "git-"), "--stateless-rpc", "--advertise-refs", dir)
}

// uploadPack serves a fetch's negotiation and pack, from one snapshot.
func (s *Server) uploadPack(w http.ResponseWriter, r *http.Request, req request) {
	body, ok := requestBody(w, r, uploadPack)
	if !ok {
		return
	}
	env := s.protocolEnv(r, uploadPack)
	dir, remove, ok := s.serveWorkspace(w, r, req)
	if !ok {
		return
	}
	defer remove()
	w.Header().Set("Content-Type", "application/x-git-upload-pack-result")
	noCache(w)
	s.run(r.Context(), w, body, env, "upload-pack", "--stateless-rpc", dir)
}

// receivePack serves a push: Git checks and indexes its objects into the throwaway repository's quarantine, and its
// pre-receive hook calls back into this server, which has the push recorded before Git answers the client.
func (s *Server) receivePack(w http.ResponseWriter, r *http.Request, req request) {
	if !mayPush(w, req) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, s.cfg.MaxPushSize+pushOverhead)
	body, ok := requestBody(w, r, receivePack)
	if !ok {
		return
	}
	id, err := uuid.NewV7()
	if err != nil {
		slog.ErrorContext(r.Context(), "Making a push's ID failed", "error", err)
		http.Error(w, "the push could not start", http.StatusInternalServerError)
		return
	}
	dir, remove, ok := s.serveWorkspace(w, r, req)
	if !ok {
		return
	}
	defer remove()
	token := s.expect(&pending{ctx: r.Context(), id: id, repo: req.repo, pusher: req.viewer.User.ID, dir: dir, received: time.Now(),
		transport: git.TransportHTTPS})
	defer s.forget(token)
	w.Header().Set("Content-Type", "application/x-git-receive-pack-result")
	noCache(w)
	s.run(r.Context(), w, body, []string{HookURLEnv + "=" + s.hookURL, HookTokenEnv + "=" + token}, "receive-pack", "--stateless-rpc", dir)
}

// mayPush answers 403 to a request whose credential reads the repository but may not push to it.
func mayPush(w http.ResponseWriter, req request) bool {
	if slices.Contains(req.held, repos.PermPush) {
		return true
	}
	http.Error(w, "You may read this repository, but not push to it", http.StatusForbidden)
	return false
}

// serveWorkspace builds the request's throwaway repository from a snapshot read now, or answers 500.
func (s *Server) serveWorkspace(w http.ResponseWriter, r *http.Request, req request) (string, func(), bool) {
	dir, remove, err := s.snapshotWorkspace(r.Context(), req.repo.ID)
	if err != nil {
		slog.ErrorContext(r.Context(), "Building a repository failed", "repository", req.repo.ID, "error", err)
		http.Error(w, "the repository could not be read", http.StatusInternalServerError)
		return "", nil, false
	}
	return dir, remove, true
}

// snapshotWorkspace builds a throwaway repository of the repository from a snapshot read now. The cache then drops
// the repository's packs the snapshot no longer lists.
func (s *Server) snapshotWorkspace(ctx context.Context, repoID uuid.UUID) (string, func(), error) {
	read := time.Now()
	snapshot, err := s.snaps.Snapshot(ctx, repoID)
	if err != nil {
		return "", nil, fmt.Errorf("reading the repository's snapshot: %w", err)
	}
	dir, remove, err := s.workspace(ctx, repoID, snapshot)
	if err != nil {
		return "", nil, err
	}
	s.cache.retain(repoID, snapshot.Packs, read)
	return dir, remove, nil
}

// protocolEnv passes the client's Git-Protocol header on to git upload-pack, which reads it from GIT_PROTOCOL, when
// it asks for protocol v2; receive-pack speaks v0 alone.
func (s *Server) protocolEnv(r *http.Request, service string) []string {
	protocol := r.Header.Get("Git-Protocol")
	if service != uploadPack || !gitProtocol.MatchString(protocol) || !strings.Contains(protocol, "version=2") {
		return nil
	}
	return []string{"GIT_PROTOCOL=" + protocol}
}

// requestBody returns the request's body, decompressed when the client gzipped it, as Git does a large fetch's
// negotiation, or answers 415 for a body that isn't service's request.
func requestBody(w http.ResponseWriter, r *http.Request, service string) (io.Reader, bool) {
	if r.Header.Get("Content-Type") != "application/x-"+service+"-request" {
		http.Error(w, "the body must be application/x-"+service+"-request", http.StatusUnsupportedMediaType)
		return nil, false
	}
	if r.Header.Get("Content-Encoding") != "gzip" {
		return r.Body, true
	}
	body, err := gzip.NewReader(r.Body)
	if err != nil {
		http.Error(w, "the body is not gzip", http.StatusBadRequest)
		return nil, false
	}
	return body, true
}

// run runs the git CLI's args with stdin, streaming its output to w as it comes, so a client sees progress. Once
// output has started, a failure can only end the response, so it is logged.
func (s *Server) run(ctx context.Context, w http.ResponseWriter, stdin io.Reader, env []string, args ...string) {
	var stderr bytes.Buffer
	cmd := s.git(ctx, env, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, flushing{w: w, rc: http.NewResponseController(w)}, &limited{buf: &stderr, n: 4096}
	if err := cmd.Run(); err != nil && ctx.Err() == nil {
		slog.ErrorContext(ctx, "Running git failed", "command", args[0], "error", err, "stderr", strings.TrimSpace(stderr.String()))
	}
}

// flushing writes to a response and flushes it at once.
type flushing struct {
	w  io.Writer
	rc *http.ResponseController
}

func (f flushing) Write(p []byte) (int, error) {
	n, err := f.w.Write(p)
	if err != nil {
		return n, fmt.Errorf("writing the response: %w", err)
	}
	_ = f.rc.Flush() // a writer that can't flush sends the bytes when the response ends
	return n, nil
}

// limited keeps the first n bytes written to it and drops the rest.
type limited struct {
	buf *bytes.Buffer
	n   int
}

func (l *limited) Write(p []byte) (int, error) {
	if room := l.n - l.buf.Len(); room > 0 {
		l.buf.Write(p[:min(room, len(p))])
	}
	return len(p), nil
}

// pktLine frames s as one of Git's packet lines.
func pktLine(s string) string {
	return fmt.Sprintf("%04x%s", len(s)+4, s)
}

// noCache tells proxies and clients never to keep a response, as git http-backend does.
func noCache(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-cache, max-age=0, must-revalidate")
	w.Header().Set("Expires", "Fri, 01 Jan 1980 00:00:00 GMT")
	w.Header().Set("Pragma", "no-cache")
}
