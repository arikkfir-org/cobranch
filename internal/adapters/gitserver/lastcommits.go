package gitserver

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"cobranch.dev/internal/adapters/gitinternal"
	"cobranch.dev/internal/services/code"
)

// commitMark starts each commit's record in git log's output for lastCommits, which its name follows; Git writes no
// such byte of its own there.
const commitMark = "\x1e"

// readLastCommits answers what last changed the directory at the request's path: how many commits changed it, the
// latest of them, and the latest that changed each of its entries, as git log -- path simplifies history.
func (s *Server) readLastCommits(w http.ResponseWriter, r *http.Request, o objects) error {
	ctx := r.Context()
	_, entries, err := s.tree(ctx, o)
	if err != nil {
		return err
	}
	count, err := s.commitCount(ctx, o)
	if err != nil {
		return err
	}
	names := make(map[string]bool, len(entries))
	for _, e := range entries {
		names[e.Name] = true
	}
	latest, last, err := s.lastCommits(ctx, o, names)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(gitinternal.LastCommitsOf(code.LastCommits{Commit: o.commit, Path: o.path, Count: count, Latest: latest,
		Entries: last})) // a caller gone away gets nothing more
	return nil
}

// commitCount is how many commits reachable from the read's commit changed what is at its path, every commit for the root.
func (s *Server) commitCount(ctx context.Context, o objects) (int, error) {
	args := []string{"rev-list", "--count", o.commit, "--"}
	if o.path != "" {
		args = append(args, o.path)
	}
	out, err := s.read(ctx, o.dir, nil, args...)
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 0, fmt.Errorf("counting the commits of %q: git rev-list printed %q", o.path, out)
	}
	return n, nil
}

// lastCommits walks git log from the read's commit, newest first, over the commits that changed the directory at its
// path, and answers the first of them and, for each of names, the first that changed what the directory holds by that
// name. It stops once every name has one, or after code.MaxLastWalk commits, so a name nobody changed in that long has
// none; a walk it stops ends its git early.
func (s *Server) lastCommits(ctx context.Context, o objects, names map[string]bool) (string, map[string]string, error) {
	ctx, stop := context.WithCancel(ctx)
	defer stop()
	args := []string{"log", "-z", "--format=" + commitMark + "%H", "--name-only", "--no-renames", "--max-count=" + strconv.Itoa(code.MaxLastWalk), o.commit, "--"}
	if o.path != "" {
		args = append(args, o.path)
	}
	var stderr bytes.Buffer
	cmd := s.objectsGit(ctx, o.dir, args...)
	cmd.Stderr = &limited{buf: &stderr, n: 4096}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return "", nil, fmt.Errorf("walking the history of %q: %w", o.path, err)
	}
	if err := cmd.Start(); err != nil {
		return "", nil, fmt.Errorf("walking the history of %q: %w", o.path, err)
	}
	latest, last, readErr := readLastCommits(bufio.NewReader(out), o.path, names)
	done := readErr == nil && latest != "" && len(last) == len(names)
	if done {
		stop() // the rest of the walk finds nothing new; one that ran out ends on its own
	}
	waitErr := cmd.Wait()
	switch {
	case readErr != nil:
		return "", nil, fmt.Errorf("walking the history of %q: %w", o.path, readErr)
	case waitErr != nil && !done:
		return "", nil, fmt.Errorf("walking the history of %q: %w: %s", o.path, waitErr, strings.TrimSpace(stderr.String()))
	}
	return latest, last, nil
}

// readLastCommits reads git log -z's records for lastCommits from out, each a commit's name after commitMark, then
// the paths it changed under dir, until every one of names has its first commit or out ends.
func readLastCommits(out *bufio.Reader, dir string, names map[string]bool) (string, map[string]string, error) {
	prefix := ""
	if dir != "" {
		prefix = dir + "/"
	}
	latest, current, first := "", "", false
	last := make(map[string]string, len(names))
	for latest == "" || len(last) < len(names) {
		token, err := out.ReadString(0)
		if errors.Is(err, io.EOF) {
			return latest, last, nil
		}
		if err != nil {
			return "", nil, fmt.Errorf("reading git log: %w", err)
		}
		token = strings.TrimSuffix(token, "\x00")
		if sha, ok := strings.CutPrefix(token, commitMark); ok && code.ValidateSHA("commit", sha) == nil {
			current, first = sha, true
			if latest == "" {
				latest = sha
			}
			continue
		}
		if first {
			token, first = strings.TrimPrefix(token, "\n"), false // git ends a commit's format line before its paths
		}
		rest, ok := strings.CutPrefix(token, prefix)
		name, _, _ := strings.Cut(rest, "/")
		if ok && current != "" && names[name] && last[name] == "" {
			last[name] = current
		}
	}
	return latest, last, nil
}
