package gitserver

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"cobranch.dev/internal/adapters/gitinternal"
	"cobranch.dev/internal/services/code"
)

// readBlame answers which commit last changed each line of the file at the request's path, as git blame tells it, in
// runs of lines, in order.
func (s *Server) readBlame(w http.ResponseWriter, r *http.Request, o objects) error {
	ctx := r.Context()
	if _, err := s.blobEntry(ctx, o); err != nil {
		return err
	}
	out, err := s.read(ctx, o.dir, nil, "blame", "--incremental", o.commit, "--", o.path)
	if err != nil {
		return err
	}
	ranges, err := parseBlame(out)
	if err != nil {
		return fmt.Errorf("blaming %q: %w", o.path, err)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(gitinternal.BlameOf(code.Blame{Commit: o.commit, Path: o.path, Ranges: ranges})) // a caller gone away gets nothing more
	return nil
}

// parseBlame reads git blame --incremental's runs: each starts "<commit> <source line> <line> <lines>", then the
// commit's facts the first time a run names it, and ends "filename <path>". Git prints them in the order it finds
// them; parseBlame answers them by line, joining neighbours that one commit last changed.
func parseBlame(out []byte) ([]code.BlameRange, error) {
	var ranges []code.BlameRange
	inRun := false
	for line := range strings.Lines(string(out)) {
		line = strings.TrimSuffix(line, "\n")
		if inRun {
			inRun = !strings.HasPrefix(line, "filename ")
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 4 || code.ValidateSHA("commit", fields[0]) != nil {
			return nil, fmt.Errorf("git blame printed %q where a run starts", line)
		}
		start, startErr := strconv.Atoi(fields[2])
		lines, linesErr := strconv.Atoi(fields[3])
		if startErr != nil || linesErr != nil || start < 1 || lines < 1 {
			return nil, fmt.Errorf("git blame printed %q where a run starts", line)
		}
		ranges = append(ranges, code.BlameRange{SHA: fields[0], Start: start, Lines: lines})
		inRun = true
	}
	if inRun {
		return nil, errors.New("git blame's output ended inside a run")
	}
	slices.SortFunc(ranges, func(a, b code.BlameRange) int { return cmp.Compare(a.Start, b.Start) })
	joined := make([]code.BlameRange, 0, len(ranges))
	for _, r := range ranges {
		if n := len(joined); n > 0 && joined[n-1].SHA == r.SHA && joined[n-1].Start+joined[n-1].Lines == r.Start {
			joined[n-1].Lines += r.Lines
			continue
		}
		joined = append(joined, r)
	}
	return joined, nil
}

// readPaths answers the path of every file and symlink in the commit's tree, in the order Git keeps them, up to
// code.MaxPaths of them: a larger tree's listing stops there, ending its git early.
func (s *Server) readPaths(w http.ResponseWriter, r *http.Request, o objects) error {
	ctx, stop := context.WithCancel(r.Context())
	defer stop()
	var stderr bytes.Buffer
	cmd := s.objectsGit(ctx, o.dir, "ls-tree", "-r", "-z", o.commit)
	cmd.Stderr = &limited{buf: &stderr, n: 4096}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("listing the paths of %s: %w", o.commit, err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("listing the paths of %s: %w", o.commit, err)
	}
	paths, truncated, readErr := readPaths(bufio.NewReader(out))
	if truncated {
		stop() // the rest of the tree is more than anyone gets; one that fits ends on its own
	}
	waitErr := cmd.Wait()
	switch {
	case readErr != nil:
		return fmt.Errorf("listing the paths of %s: %w", o.commit, readErr)
	case waitErr != nil && !truncated:
		return fmt.Errorf("listing the paths of %s: %w: %s", o.commit, waitErr, strings.TrimSpace(stderr.String()))
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(gitinternal.PathsOf(code.Paths{Commit: o.commit, Paths: paths, Truncated: truncated})) // a caller gone away gets nothing more
	return nil
}

// readPaths reads git ls-tree -r -z's entries from out, each "<mode> <type> <object>\t<path>\0", and answers the paths
// of its files and symlinks, without its submodules', up to code.MaxPaths of them, and whether it holds more.
func readPaths(out *bufio.Reader) ([]string, bool, error) {
	paths := []string{}
	for {
		record, err := out.ReadString(0)
		if errors.Is(err, io.EOF) && record == "" {
			return paths, false, nil
		}
		if err != nil {
			return nil, false, fmt.Errorf("reading git ls-tree: %w", err)
		}
		meta, path, ok := strings.Cut(strings.TrimSuffix(record, "\x00"), "\t")
		fields := strings.Fields(meta)
		if !ok || len(fields) != 3 || path == "" {
			return nil, false, fmt.Errorf("reading git ls-tree: it printed %q", record)
		}
		if kindOf(fields[0]) == code.KindSubmodule {
			continue
		}
		if len(paths) == code.MaxPaths {
			return paths, true, nil
		}
		paths = append(paths, path)
	}
}
