package gitserver

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"gocloud.dev/blob"

	"cobranch.dev/internal/services/git"
	"cobranch.dev/internal/services/repos"
)

// hookPath is where a push's hook calls its Git server, on the loopback port.
const hookPath = "/pre-receive"

// pending is a push whose receive-pack runs: what its hook's call needs to record it.
type pending struct {
	// ctx is the push's request's, which carries its trace and ends when its client goes away.
	ctx    context.Context
	id     uuid.UUID
	repo   repos.Repository
	pusher uuid.UUID
	// bypass is whether the pusher's credential holds repo.bypass.
	bypass   bool
	dir      string
	received time.Time
	// transport is how the push came.
	transport git.Transport
}

// hookRequest is what a push's hook sends: the ref updates Git gave it on its standard input, and where Git keeps the
// push's objects until the hook accepts them.
type hookRequest struct {
	Updates         string `json:"updates"`
	Quarantine      string `json:"quarantine"`
	ObjectDirectory string `json:"objectDirectory"`
	Alternates      string `json:"alternates"`
}

// expect registers a push whose hook will call, and returns the token its call bears.
func (s *Server) expect(p *pending) string {
	token := rand.Text()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pushes[token] = p
	return token
}

// forget drops a push once its receive-pack ended.
func (s *Server) forget(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.pushes, token)
}

// hookHandler serves the hooks' calls: the push the call's token names is recorded, or refused, and the answer goes
// back as the lines the hook prints, which Git shows as remote: lines, with 200 for an accepted push.
func (s *Server) hookHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+hookPath, func(w http.ResponseWriter, r *http.Request) {
		token, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		s.mu.Lock()
		p := s.pushes[token]
		s.mu.Unlock()
		if p == nil || token == "" {
			http.Error(w, "no push of this server's", http.StatusUnauthorized)
			return
		}
		var hr hookRequest
		if err := json.NewDecoder(io.LimitReader(r.Body, 64<<20)).Decode(&hr); err != nil {
			http.Error(w, "not a hook's call", http.StatusBadRequest)
			return
		}
		answer := s.receive(p, hr)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if !answer.Accepted {
			w.WriteHeader(http.StatusConflict)
		}
		if answer.Message != "" {
			_, _ = io.WriteString(w, answer.Message+"\n") // a hook gone away fails its push anyway
		}
	})
	return mux
}

// receive checks a push's refs, builds its manifest, with what its refs say of the branches, uploads its pack and
// records the push, or refuses it with the reason. A failure of the bucket or the database refuses it, unchanged.
func (s *Server) receive(p *pending, hr hookRequest) git.Answer {
	ctx := p.ctx
	updates, err := parseUpdates(hr.Updates)
	if err != nil {
		return git.Answer{Message: err.Error()}
	}
	if why := git.Refusal(updates); why != "" {
		return git.Answer{Message: why}
	}
	objects, err := hr.within(p.dir)
	if err != nil {
		slog.ErrorContext(ctx, "A hook named objects outside its repository", "push", p.id, "error", err)
		return git.Answer{Message: git.MessageNotRecorded}
	}
	commits, err := s.newCommits(ctx, p.dir, objects.env, updates)
	if err != nil {
		slog.ErrorContext(ctx, "Reading a push's commits failed", "push", p.id, "repository", p.repo.ID, "error", err)
		return git.Answer{Message: git.MessageNotRecorded}
	}
	branches, refusal, err := s.branchFacts(ctx, p.dir, objects.env, updates)
	if err != nil {
		slog.ErrorContext(ctx, "Working out a push's branches failed", "push", p.id, "repository", p.repo.ID, "error", err)
		return git.Answer{Message: git.MessageNotRecorded}
	}
	if refusal != "" {
		return git.Answer{Message: refusal}
	}
	pack, refusal, err := s.storePack(ctx, p, objects.quarantine)
	if err != nil {
		slog.ErrorContext(ctx, "Storing a push's pack failed", "push", p.id, "repository", p.repo.ID, "error", err)
		return git.Answer{Message: git.MessageNotRecorded}
	}
	if refusal != "" {
		return git.Answer{Message: refusal}
	}
	answer, err := s.recorder.Record(ctx, git.Push{ID: p.id, RepoID: p.repo.ID, OrgID: p.repo.OrgID, PusherID: p.pusher, Transport: p.transport,
		Updates: updates, Pack: pack, Commits: commits, ReceivedAt: p.received, Requirements: branches.requirements, Facts: branches.facts,
		Tips: branches.tips, Brought: branches.brought, Bypass: p.bypass})
	if err != nil {
		slog.ErrorContext(ctx, "Recording a push failed", "push", p.id, "repository", p.repo.ID, "error", err)
		return git.Answer{Message: git.MessageNotRecorded}
	}
	return answer
}

// parseUpdates reads the lines Git gives a pre-receive hook, "<old> <new> <ref>" each.
func parseUpdates(lines string) ([]git.RefUpdate, error) {
	var updates []git.RefUpdate
	for line := range strings.SplitSeq(strings.TrimSpace(lines), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 3 {
			return nil, fmt.Errorf("the hook got %q from Git", line)
		}
		updates = append(updates, git.RefUpdate{Old: fields[0], New: fields[1], Name: fields[2]})
	}
	return updates, nil
}

// objectDirs is where Git keeps a push's objects while its hook runs: the quarantine that holds its pack, and the
// environment through which the git CLI reads them.
type objectDirs struct {
	quarantine string
	env        []string
}

// within resolves what Git told the hook of where the push's objects are, each path within dir, so that a call can't
// have the server read or upload files outside the push's repository.
func (hr hookRequest) within(dir string) (objectDirs, error) {
	quarantine, err := inside(dir, hr.Quarantine)
	if err != nil {
		return objectDirs{}, fmt.Errorf("GIT_QUARANTINE_PATH: %w", err)
	}
	objects, err := inside(dir, hr.ObjectDirectory)
	if err != nil {
		return objectDirs{}, fmt.Errorf("GIT_OBJECT_DIRECTORY: %w", err)
	}
	var alternates []string
	if hr.Alternates != "" {
		for path := range strings.SplitSeq(hr.Alternates, string(os.PathListSeparator)) {
			alternate, err := inside(dir, path)
			if err != nil {
				return objectDirs{}, fmt.Errorf("GIT_ALTERNATE_OBJECT_DIRECTORIES: %w", err)
			}
			alternates = append(alternates, alternate)
		}
	}
	od := objectDirs{quarantine: quarantine}
	for _, v := range [][2]string{{"GIT_QUARANTINE_PATH", quarantine}, {"GIT_OBJECT_DIRECTORY", objects},
		{"GIT_ALTERNATE_OBJECT_DIRECTORIES", strings.Join(alternates, string(os.PathListSeparator))}} {
		if v[1] != "" {
			od.env = append(od.env, v[0]+"="+v[1])
		}
	}
	return od, nil
}

// inside returns path, which must lie within dir, rebuilt from dir and path's place relative to it; "" stays "".
func inside(dir, path string) (string, error) {
	if path == "" {
		return "", nil
	}
	rel, err := filepath.Rel(dir, path)
	if err != nil || !filepath.IsLocal(rel) {
		return "", fmt.Errorf("%s is outside %s", path, dir)
	}
	return filepath.Join(dir, rel), nil
}

// newCommits reads every commit the push brings that the repository's refs don't reach, whole.
func (s *Server) newCommits(ctx context.Context, dir string, env []string, updates []git.RefUpdate) ([]git.Commit, error) {
	var tips []string
	for _, u := range updates {
		if u.New != git.ZeroSHA {
			tips = append(tips, u.New+"^{commit}")
		}
	}
	if len(tips) == 0 {
		return nil, nil
	}
	// A tag names a commit, which counts; one that names a tree or a blob brings no commit.
	peeled, err := s.output(ctx, env, strings.Join(tips, "\n")+"\n", "--git-dir="+dir, "cat-file", "--batch-check=%(objectname) %(objecttype)")
	if err != nil {
		return nil, err
	}
	var commits []string
	for line := range strings.SplitSeq(peeled, "\n") {
		if fields := strings.Fields(line); len(fields) == 2 && fields[1] == "commit" {
			commits = append(commits, fields[0])
		}
	}
	if len(commits) == 0 {
		return nil, nil
	}
	listed, err := s.output(ctx, env, "", append(append([]string{"--git-dir=" + dir, "rev-list"}, commits...), "--not", "--all")...)
	if err != nil {
		return nil, err
	}
	shas := strings.Fields(listed)
	if len(shas) == 0 {
		return nil, nil
	}
	return s.readCommits(ctx, dir, env, shas)
}

// readCommits reads commits shas with one git cat-file --batch.
func (s *Server) readCommits(ctx context.Context, dir string, env []string, shas []string) ([]git.Commit, error) {
	cmd := s.git(ctx, env, "--git-dir="+dir, "cat-file", "--batch")
	cmd.Stdin = strings.NewReader(strings.Join(shas, "\n") + "\n")
	var stderr bytes.Buffer
	cmd.Stderr = &limited{buf: &stderr, n: 4096}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("reading the commits: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("reading the commits: %w", err)
	}
	commits := make([]git.Commit, 0, len(shas))
	readErr := readObjects(out, func(sha, kind string, content []byte) error {
		if kind != "commit" {
			return fmt.Errorf("object %s is a %s, not a commit", sha, kind)
		}
		c, err := parseCommit(sha, content)
		commits = append(commits, c)
		return err
	})
	if readErr != nil {
		_ = cmd.Process.Kill() // its output is no longer read; Wait reaps it
	}
	if err := cmd.Wait(); err != nil && readErr == nil {
		return nil, fmt.Errorf("git cat-file: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	if readErr != nil {
		return nil, readErr
	}
	return commits, nil
}

// output runs the git CLI's args with stdin and returns what it printed.
func (s *Server) output(ctx context.Context, env []string, stdin string, args ...string) (string, error) {
	cmd := s.git(ctx, env, args...)
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.Output()
	if err != nil {
		stderr := ""
		if exit, ok := err.(*exec.ExitError); ok {
			stderr = strings.TrimSpace(string(exit.Stderr))
		}
		return "", fmt.Errorf("git %s: %w: %s", args[1], err, stderr)
	}
	return string(out), nil
}

// storePack uploads the push's pack from its quarantine to the bucket, as an upload of its own and the pack's index
// first, and keeps it in the cache: nil when the push brought no objects. The pack is listed only once the push is
// recorded, so a push that fails leaves files no row lists, which maintenance deletes. A pack the organization's Git
// data limit leaves no room for isn't uploaded: storePack returns the refusal's message instead.
func (s *Server) storePack(ctx context.Context, p *pending, quarantine string) (*git.Pack, string, error) {
	if quarantine == "" {
		return nil, "", nil
	}
	found, err := filepath.Glob(filepath.Join(quarantine, "pack", "pack-*.pack"))
	if err != nil || len(found) == 0 {
		return nil, "", nil
	}
	if len(found) > 1 {
		return nil, "", fmt.Errorf("the push left %d packs", len(found))
	}
	info, err := os.Stat(found[0])
	if err != nil {
		return nil, "", fmt.Errorf("reading the pack's size: %w", err)
	}
	refusal, err := s.recorder.Weigh(ctx, p.repo.OrgID, info.Size())
	if err != nil {
		return nil, "", fmt.Errorf("weighing the pack: %w", err)
	}
	if refusal != "" {
		return nil, refusal, nil
	}
	name := strings.TrimSuffix(filepath.Base(found[0]), ".pack")
	pack, err := s.keepPack(ctx, p.repo.ID, name, func(file string) string { return filepath.Join(quarantine, "pack", file) })
	return pack, "", err
}

// keepPack uploads the repository's pack name, whose files at says where are, to the bucket as an upload of its own,
// and keeps it in the cache.
func (s *Server) keepPack(ctx context.Context, repoID uuid.UUID, name string, at func(file string) string) (*git.Pack, error) {
	pack, stored, err := putPack(ctx, s.bucket, repoID, name, at)
	if err != nil {
		return nil, err
	}
	if err := s.cache.add(repoID, name, stored, at, os.Link); err != nil {
		slog.WarnContext(ctx, "Caching a pushed pack failed; a fetch downloads it", "pack", name, "error", err)
	}
	return &pack, nil
}

// putPack uploads pack name's files, where at says they are, to the bucket as an upload of their own, the index
// first, and returns the pack and how many bytes its files hold.
func putPack(ctx context.Context, bucket *blob.Bucket, repoID uuid.UUID, name string, at func(file string) string) (git.Pack, int64, error) {
	objects, err := idxObjects(at(name + ".idx"))
	if err != nil {
		return git.Pack{}, 0, err
	}
	pack := git.Pack{Name: name, Upload: uuid.Must(uuid.NewV7()), Objects: objects}
	var stored int64
	for _, suffix := range packFiles {
		n, err := upload(ctx, bucket, packKey(repoID, pack.Upload, name+suffix), at(name+suffix))
		if err != nil {
			return git.Pack{}, 0, err
		}
		stored += n
		if suffix == ".pack" {
			pack.Size = n
		}
	}
	return pack, stored, nil
}

// upload copies file to the bucket's object key, and returns its size.
func upload(ctx context.Context, bucket *blob.Bucket, key, file string) (int64, error) {
	f, err := os.Open(file)
	if err != nil {
		return 0, fmt.Errorf("opening %s: %w", file, err)
	}
	defer func() { _ = f.Close() }() // only read
	w, err := bucket.NewWriter(ctx, key, nil)
	if err != nil {
		return 0, fmt.Errorf("writing %s to the bucket: %w", key, err)
	}
	n, err := io.Copy(w, f)
	if closeErr := w.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return 0, fmt.Errorf("uploading %s: %w", key, err)
	}
	return n, nil
}

// idxObjects reads how many objects a pack holds from its version 2 index, whose fan-out table's last entry counts
// them.
func idxObjects(idx string) (int64, error) {
	f, err := os.Open(idx)
	if err != nil {
		return 0, fmt.Errorf("opening the pack's index: %w", err)
	}
	defer func() { _ = f.Close() }() // only read
	header := make([]byte, 8+256*4)
	if _, err := io.ReadFull(f, header); err != nil {
		return 0, fmt.Errorf("reading the pack's index: %w", err)
	}
	if !bytes.Equal(header[:4], []byte("\377tOc")) || binary.BigEndian.Uint32(header[4:8]) != 2 {
		return 0, fmt.Errorf("the pack's index is not version 2")
	}
	return int64(binary.BigEndian.Uint32(header[len(header)-4:])), nil
}
