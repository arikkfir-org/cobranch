package workspace

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"strconv"
	"strings"
)

// Limits of the file tools.
const (
	maxListed   = 1000
	maxDepth    = 5
	defaultRead = 2000
	// sniffed is how much of a file's start is looked at to tell text from binary.
	sniffed = 8 << 10
)

var listSpec = spec("list_files", "Lists the files and directories under a directory of the workspace, directories ending in /, "+
	"to a depth (2 unless given, 5 at most); .git's insides are left out.",
	`"path":{"type":"string","description":"The directory, relative to the workspace; the workspace itself unless given"},`+
		`"depth":{"type":"integer","description":"How many levels to list, 1 to 5"}`)

type listArgs struct {
	Path  string `json:"path"`
	Depth int    `json:"depth"`
}

// list lists the entries under a directory, to a depth.
func (w *Workspace) list(_ context.Context, a listArgs) (string, error) {
	dir, err := w.rel(a.Path)
	if err != nil {
		return "", err
	}
	depth := a.Depth
	if depth <= 0 {
		depth = 2
	}
	depth = min(depth, maxDepth)
	var lines []string
	more := 0
	err = fs.WalkDir(w.root.FS(), dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || p == dir {
			return err
		}
		name := strings.TrimPrefix(p, dir+"/")
		if dir == "." {
			name = p
		}
		if d.IsDir() {
			name += "/"
		}
		if len(lines) < maxListed {
			lines = append(lines, name)
		} else {
			more++
		}
		if d.IsDir() && (d.Name() == ".git" || strings.Count(name, "/") >= depth) {
			return fs.SkipDir
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("listing %s: %w", dir, err)
	}
	if more > 0 {
		lines = append(lines, "["+strconv.Itoa(more)+" more left out: list a directory beneath]")
	}
	if len(lines) == 0 {
		return "(empty)", nil
	}
	return strings.Join(lines, "\n"), nil
}

var readSpec = spec("read_file", "Reads a text file of the workspace, its lines numbered for reference: the numbers and the tab after them "+
	"are not part of the file. 2000 lines unless given; read on from a line with offset.",
	`"path":{"type":"string","description":"The file, relative to the workspace"},`+
		`"offset":{"type":"integer","description":"The first line to read, from 1"},`+
		`"limit":{"type":"integer","description":"How many lines to read"}`, "path")

type readArgs struct {
	Path   string `json:"path"`
	Offset int    `json:"offset"`
	Limit  int    `json:"limit"`
}

// read reads lines of a text file, numbered.
func (w *Workspace) read(_ context.Context, a readArgs) (string, error) {
	data, file, err := w.text(a.Path)
	if err != nil {
		return "", err
	}
	if len(data) == 0 {
		return "(empty file)", nil
	}
	lines := strings.SplitAfter(string(data), "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	first, limit := max(a.Offset, 1), a.Limit
	if limit <= 0 {
		limit = defaultRead
	}
	if first > len(lines) {
		return "", fmt.Errorf("%s has %d lines", file, len(lines))
	}
	last := min(first-1+limit, len(lines))
	var b strings.Builder
	for i := first - 1; i < last; i++ {
		b.WriteString(fmt.Sprintf("%6d\t%s", i+1, strings.TrimSuffix(lines[i], "\n")) + "\n")
	}
	if last < len(lines) {
		b.WriteString("[" + strconv.Itoa(len(lines)-last) + " more lines: read on from line " + strconv.Itoa(last+1) + "]\n")
	}
	return b.String(), nil
}

// text reads a text file of the workspace, and answers its path relative to it.
func (w *Workspace) text(p string) ([]byte, string, error) {
	file, err := w.rel(p)
	if err != nil {
		return nil, "", err
	}
	data, err := w.root.ReadFile(file)
	if err != nil {
		return nil, "", fmt.Errorf("reading %s: %w", file, err)
	}
	if binary(data) {
		return nil, "", fmt.Errorf("%s is binary, not text", file)
	}
	return data, file, nil
}

// binary reports whether data, a file's, looks binary: a NUL byte near its start.
func binary(data []byte) bool {
	return bytes.IndexByte(data[:min(len(data), sniffed)], 0) >= 0
}

var writeSpec = spec("write_file", "Writes a file of the workspace whole, creating it and its directories as needed, or replacing what it held.",
	`"path":{"type":"string","description":"The file, relative to the workspace"},`+
		`"content":{"type":"string","description":"What the file holds"}`, "path", "content")

type writeArgs struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// write writes a file whole.
func (w *Workspace) write(_ context.Context, a writeArgs) (string, error) {
	file, err := w.rel(a.Path)
	if err != nil {
		return "", err
	}
	if dir := path.Dir(filepath.ToSlash(file)); dir != "." {
		if err := w.root.MkdirAll(dir, 0o755); err != nil {
			return "", fmt.Errorf("making %s: %w", dir, err)
		}
	}
	if err := w.root.WriteFile(file, []byte(a.Content), 0o644); err != nil {
		return "", fmt.Errorf("writing %s: %w", file, err)
	}
	return "Wrote " + strconv.Itoa(len(a.Content)) + " bytes to " + file + ".", nil
}

var editSpec = spec("edit_file", "Replaces text in a file of the workspace: old must occur exactly once, unless all is set, which "+
	"replaces every occurrence. Give old with enough of the lines around it to be unique, without read_file's line numbers.",
	`"path":{"type":"string","description":"The file, relative to the workspace"},`+
		`"old":{"type":"string","description":"The text to replace, exactly as the file holds it"},`+
		`"new":{"type":"string","description":"The text to put in its place"},`+
		`"all":{"type":"boolean","description":"Replace every occurrence of old"}`, "path", "old", "new")

type editArgs struct {
	Path string `json:"path"`
	Old  string `json:"old"`
	New  string `json:"new"`
	All  bool   `json:"all"`
}

// edit replaces text in a file, keeping its mode.
func (w *Workspace) edit(_ context.Context, a editArgs) (string, error) {
	if a.Old == "" {
		return "", errors.New("old is empty: give the text to replace, or write the file whole")
	}
	data, file, err := w.text(a.Path)
	if err != nil {
		return "", err
	}
	n := strings.Count(string(data), a.Old)
	switch {
	case n == 0:
		return "", fmt.Errorf("%s doesn't hold old", file)
	case n > 1 && !a.All:
		return "", fmt.Errorf("%s holds old %d times: give more of the lines around it, or set all", file, n)
	}
	info, err := w.root.Stat(file)
	if err != nil {
		return "", fmt.Errorf("reading %s's mode: %w", file, err)
	}
	if err := w.root.WriteFile(file, []byte(strings.ReplaceAll(string(data), a.Old, a.New)), info.Mode().Perm()); err != nil {
		return "", fmt.Errorf("writing %s: %w", file, err)
	}
	return "Replaced " + strconv.Itoa(n) + " of old in " + file + ".", nil
}
