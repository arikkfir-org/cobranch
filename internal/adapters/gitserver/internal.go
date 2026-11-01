package gitserver

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os/exec"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"cobranch.dev/internal/adapters/gitinternal"
	"cobranch.dev/internal/services/code"
)

// The modes Git writes a tree's entries with, beside a file's 100644 and 100755.
const (
	modeTree      = "040000"
	modeSymlink   = "120000"
	modeSubmodule = "160000"
)

// InternalHandler serves the internal call (package gitinternal) to callers that send token as a bearer token: the
// REST and GraphQL servers' reads of a repository's trees and files, each from one snapshot, and their merges. It
// checks no viewer, since its callers checked theirs, and nothing outside the cluster reaches its port.
func (s *Server) InternalHandler(token *gitinternal.Token) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+gitinternal.ReposPath+"{id}/tree", s.internal(token, "tree", s.readTree))
	mux.HandleFunc("GET "+gitinternal.ReposPath+"{id}/blob", s.internal(token, "blob", s.readBlob))
	mux.HandleFunc("GET "+gitinternal.ReposPath+"{id}/raw", s.internal(token, "raw", s.readRaw))
	mux.HandleFunc("POST "+gitinternal.ReposPath+"{id}/merge", s.mergeHandler(token))
	return mux
}

// objects is one internal read: the throwaway repository of the snapshot it reads, the commit its ref resolved to,
// and the path it asks for.
type objects struct {
	dir    string
	commit string
	path   string
}

// internal checks the request's token, ref and path, builds its repository from a snapshot read now and resolves its
// ref, then serves read with them.
func (s *Server) internal(token *gitinternal.Token, read string, next func(w http.ResponseWriter, r *http.Request, o objects) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !authorized(w, r, token) {
			return
		}
		id, err := uuid.Parse(r.PathValue("id"))
		ref, path := r.URL.Query().Get("ref"), r.URL.Query().Get("path")
		if err != nil || code.ValidateRef(ref) != nil || code.ValidatePath(path) != nil {
			problem(w, http.StatusBadRequest, gitinternal.Problem{Message: "a repository's ID, and a ref and a path the API takes"})
			return
		}
		ctx, span := tracer.Start(r.Context(), "gitserver.internal "+read, trace.WithAttributes(attribute.String("cobranch.repository.id", id.String()),
			attribute.String("cobranch.ref", ref), attribute.String("cobranch.path", path)))
		defer span.End()
		dir, remove, err := s.snapshotWorkspace(ctx, id)
		if err != nil {
			answer(ctx, w, span, read, err)
			return
		}
		defer remove()
		commit, err := s.resolve(ctx, dir, ref)
		if err == nil {
			span.SetAttributes(attribute.String("cobranch.commit", commit))
			err = next(w, r.WithContext(ctx), objects{dir: dir, commit: commit, path: path})
		}
		if err != nil {
			answer(ctx, w, span, read, err)
		}
	}
}

// authorized reports whether the request sends the internal call's token, or answers 401, or 503 while the token
// can't be read.
func authorized(w http.ResponseWriter, r *http.Request, token *gitinternal.Token) bool {
	want, err := token.Get(r.Context())
	if err != nil {
		slog.ErrorContext(r.Context(), "Reading the internal call's token failed", "error", err)
		problem(w, http.StatusServiceUnavailable, gitinternal.Problem{Message: "the internal call's token could not be read"})
		return false
	}
	scheme, got, _ := strings.Cut(r.Header.Get("Authorization"), " ")
	if !strings.EqualFold(scheme, "Bearer") || subtle.ConstantTimeCompare([]byte(strings.TrimSpace(got)), []byte(want)) != 1 {
		problem(w, http.StatusUnauthorized, gitinternal.Problem{Message: "the internal call needs its token"})
		return false
	}
	return true
}

// answer answers a read that failed: 404 with the code of what it didn't find, or 500, logged, for anything else.
func answer(ctx context.Context, w http.ResponseWriter, span trace.Span, read string, err error) {
	if c, ok := gitinternal.CodeOf(err); ok {
		problem(w, http.StatusNotFound, gitinternal.Problem{Code: c, Message: err.Error()})
		return
	}
	span.SetStatus(codes.Error, err.Error())
	slog.ErrorContext(ctx, "Reading a repository's objects failed", "read", read, "error", err)
	problem(w, http.StatusInternalServerError, gitinternal.Problem{Message: "the repository could not be read"})
}

// problem writes p as an answer with status.
func problem(w http.ResponseWriter, status int, p gitinternal.Problem) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(p) // a caller gone away gets nothing more
}

// readTree answers the tree at the request's path, with its entries in the order Git keeps them.
func (s *Server) readTree(w http.ResponseWriter, r *http.Request, o objects) error {
	ctx := r.Context()
	treeSHA := ""
	if o.path == "" {
		out, err := s.read(ctx, o.dir, nil, "rev-parse", "--verify", o.commit+"^{tree}")
		if err != nil {
			return err
		}
		treeSHA = strings.TrimSpace(string(out))
	} else {
		e, err := s.entry(ctx, o)
		if err != nil {
			return err
		}
		switch e.Kind {
		case code.KindSubmodule:
			return code.ErrSubmodule
		case code.KindBlob, code.KindSymlink:
			return code.ErrNotTree
		}
		treeSHA = e.SHA
	}
	out, err := s.read(ctx, o.dir, nil, "ls-tree", "-l", "-z", treeSHA)
	if err != nil {
		return err
	}
	entries, err := parseTree(out, o.path)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(gitinternal.TreeOf(code.Tree{Commit: o.commit, Path: o.path, SHA: treeSHA, Entries: entries})) // a caller gone away gets nothing more
	return nil
}

// readBlob answers the file or symlink at the request's path, with its content up to the limit the request asks for.
func (s *Server) readBlob(w http.ResponseWriter, r *http.Request, o objects) error {
	limit, err := strconv.ParseInt(r.URL.Query().Get("limit"), 10, 64)
	if err != nil || limit < 0 {
		problem(w, http.StatusBadRequest, gitinternal.Problem{Message: "limit must be a number of bytes"})
		return nil
	}
	e, err := s.blobEntry(r.Context(), o)
	if err != nil {
		return err
	}
	content, err := s.content(r.Context(), o.dir, e, limit)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(gitinternal.BlobOf(code.Blob{Commit: o.commit, Path: o.path, Kind: e.Kind, Mode: e.Mode, SHA: e.SHA, Size: e.Size,
		Content: content, Truncated: e.Size > limit})) // a caller gone away gets nothing more
	return nil
}

// readRaw streams the bytes of the file or symlink at the request's path, with its commit and object in headers.
func (s *Server) readRaw(w http.ResponseWriter, r *http.Request, o objects) error {
	ctx := r.Context()
	e, err := s.blobEntry(ctx, o)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.FormatInt(e.Size, 10))
	w.Header().Set(gitinternal.HeaderCommit, o.commit)
	w.Header().Set(gitinternal.HeaderObject, e.SHA)
	var stderr bytes.Buffer
	cmd := s.objectsGit(ctx, o.dir, "cat-file", "blob", e.SHA)
	cmd.Stdout, cmd.Stderr = w, &limited{buf: &stderr, n: 4096}
	// Once the bytes started, a failure can only cut the answer short, which its Content-Length shows the caller.
	if err := cmd.Run(); err != nil && ctx.Err() == nil {
		slog.ErrorContext(ctx, "Streaming a file's bytes failed", "error", err, "stderr", strings.TrimSpace(stderr.String()))
	}
	return nil
}

// resolve finds the commit ref names in the repository at dir: a full commit name, a branch or a tag, in that order,
// a tag peeled to the commit it names; or code.ErrRefNotFound. code.ValidateRef let only names through, so no ref
// reaches Git's revision syntax.
func (s *Server) resolve(ctx context.Context, dir, ref string) (string, error) {
	var asked strings.Builder
	if code.ValidateSHA("ref", ref) == nil {
		asked.WriteString(ref + "^{commit}\n")
	}
	asked.WriteString("refs/heads/" + ref + "^{commit}\nrefs/tags/" + ref + "^{commit}\n")
	out, err := s.read(ctx, dir, strings.NewReader(asked.String()), "cat-file", "--batch-check")
	if err != nil {
		return "", err
	}
	for line := range strings.Lines(string(out)) {
		fields := strings.Fields(line)
		switch {
		case len(fields) == 3 && fields[1] == "commit":
			return fields[0], nil
		case len(fields) >= 2 && (fields[len(fields)-1] == "missing" || fields[len(fields)-1] == "ambiguous"):
		default:
			return "", fmt.Errorf("resolving ref %s: git cat-file answered %q", ref, line)
		}
	}
	return "", code.ErrRefNotFound
}

// blobEntry finds the file or symlink at the read's path: code.ErrNotBlob for the root or a directory, and
// code.ErrSubmodule for a submodule.
func (s *Server) blobEntry(ctx context.Context, o objects) (code.Entry, error) {
	if o.path == "" {
		return code.Entry{}, code.ErrNotBlob
	}
	e, err := s.entry(ctx, o)
	if err != nil {
		return code.Entry{}, err
	}
	switch e.Kind {
	case code.KindTree:
		return code.Entry{}, code.ErrNotBlob
	case code.KindSubmodule:
		return code.Entry{}, code.ErrSubmodule
	}
	return e, nil
}

// entry finds what the commit's tree holds at the read's path, which isn't the root, or code.ErrPathNotFound.
func (s *Server) entry(ctx context.Context, o objects) (code.Entry, error) {
	out, err := s.read(ctx, o.dir, nil, "ls-tree", "-l", "-z", "--full-tree", o.commit, "--", o.path)
	if err != nil {
		return code.Entry{}, err
	}
	entries, err := parseTree(out, "")
	if err != nil {
		return code.Entry{}, err
	}
	for _, e := range entries {
		if e.Path == o.path {
			e.Name = e.Path[strings.LastIndex(e.Path, "/")+1:]
			return e, nil
		}
	}
	return code.Entry{}, code.ErrPathNotFound
}

// parseTree reads git ls-tree -l -z's entries, each "<mode> <type> <object> <size>\t<name>\0", as those of the tree at
// dir.
func parseTree(out []byte, dir string) ([]code.Entry, error) {
	var entries []code.Entry
	for record := range bytes.SplitSeq(bytes.TrimSuffix(out, []byte{0}), []byte{0}) {
		if len(record) == 0 {
			continue
		}
		meta, name, ok := bytes.Cut(record, []byte("\t"))
		fields := strings.Fields(string(meta))
		if !ok || len(fields) != 4 {
			return nil, fmt.Errorf("reading a tree: git ls-tree printed %q", record)
		}
		e := code.Entry{Name: string(name), Path: string(name), Mode: fields[0], SHA: fields[2], Kind: kindOf(fields[0])}
		if dir != "" {
			e.Path = dir + "/" + e.Name
		}
		if fields[3] != "-" {
			size, err := strconv.ParseInt(fields[3], 10, 64)
			if err != nil {
				return nil, fmt.Errorf("reading a tree: git ls-tree printed size %q", fields[3])
			}
			e.Size = size
		}
		entries = append(entries, e)
	}
	return entries, nil
}

// kindOf is the kind of a tree's entry that Git writes with mode.
func kindOf(mode string) code.EntryKind {
	switch mode {
	case modeTree:
		return code.KindTree
	case modeSymlink:
		return code.KindSymlink
	case modeSubmodule:
		return code.KindSubmodule
	}
	return code.KindBlob
}

// content reads up to limit bytes of e's blob. A larger blob's reading stops there, so a file of gigabytes costs no
// more than its first limit bytes.
func (s *Server) content(ctx context.Context, dir string, e code.Entry, limit int64) ([]byte, error) {
	ctx, stop := context.WithCancel(ctx)
	defer stop()
	var stderr bytes.Buffer
	cmd := s.objectsGit(ctx, dir, "cat-file", "blob", e.SHA)
	cmd.Stderr = &limited{buf: &stderr, n: 4096}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("reading blob %s: %w", e.SHA, err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("reading blob %s: %w", e.SHA, err)
	}
	content, readErr := io.ReadAll(io.LimitReader(out, limit))
	if e.Size > limit {
		stop() // the blob has more to give, which nobody wants; one that fits ends on its own
	}
	waitErr := cmd.Wait()
	if readErr != nil {
		return nil, fmt.Errorf("reading blob %s: %w", e.SHA, readErr)
	}
	if waitErr != nil && e.Size <= limit {
		return nil, fmt.Errorf("reading blob %s: %w: %s", e.SHA, waitErr, strings.TrimSpace(stderr.String()))
	}
	return content, nil
}

// read runs the git CLI's args over the repository at dir with stdin, and returns what it printed.
func (s *Server) read(ctx context.Context, dir string, stdin io.Reader, args ...string) ([]byte, error) {
	var stderr bytes.Buffer
	cmd := s.objectsGit(ctx, dir, args...)
	cmd.Stdin, cmd.Stderr = stdin, &limited{buf: &stderr, n: 4096}
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("running git %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

// objectsGit returns the git CLI's command for args over the repository at dir, which takes paths literally.
func (s *Server) objectsGit(ctx context.Context, dir string, args ...string) *exec.Cmd {
	return s.git(ctx, []string{"GIT_DIR=" + dir, "GIT_LITERAL_PATHSPECS=1"}, args...)
}
