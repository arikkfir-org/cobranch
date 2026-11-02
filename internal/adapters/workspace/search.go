package workspace

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"strconv"
	"strings"
)

// Limits of a search.
const (
	maxMatches     = 200
	maxSearched    = 1 << 20
	maxMatchLength = 300
)

var searchSpec = spec("search", "Searches the text files under a directory of the workspace for lines that match a regular expression "+
	"(RE2's syntax), and lists them as path:line: text, 200 at most; .git and files over 1 MiB are left out.",
	`"pattern":{"type":"string","description":"The regular expression"},`+
		`"path":{"type":"string","description":"The directory, relative to the workspace; the workspace itself unless given"},`+
		`"glob":{"type":"string","description":"Only files whose names match this, such as *.go"}`, "pattern")

type searchArgs struct {
	Pattern string `json:"pattern"`
	Path    string `json:"path"`
	Glob    string `json:"glob"`
}

// search lists the lines of text files under a directory that match a pattern.
func (w *Workspace) search(ctx context.Context, a searchArgs) (string, error) {
	re, err := regexp.Compile(a.Pattern)
	if err != nil {
		return "", fmt.Errorf("reading the pattern: %w", err)
	}
	if _, err := path.Match(a.Glob, ""); err != nil {
		return "", fmt.Errorf("reading the glob: %w", err)
	}
	dir, err := w.rel(a.Path)
	if err != nil {
		return "", err
	}
	var matches []string
	err = fs.WalkDir(w.root.FS(), dir, func(p string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case ctx.Err() != nil:
			return ctx.Err()
		case d.IsDir() && d.Name() == ".git":
			return fs.SkipDir
		case d.IsDir() || len(matches) >= maxMatches || (a.Glob != "" && !globbed(a.Glob, d.Name())):
			return nil
		}
		matches = w.grep(p, re, matches)
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("searching %s: %w", dir, err)
	}
	if len(matches) == 0 {
		return "No line matches.", nil
	}
	if len(matches) >= maxMatches {
		matches = append(matches[:maxMatches], "[stopped at "+strconv.Itoa(maxMatches)+" matches: narrow the search]")
	}
	return strings.Join(matches, "\n"), nil
}

// globbed reports whether name matches glob, which search checked.
func globbed(glob, name string) bool {
	ok, _ := path.Match(glob, name) // checked before the walk
	return ok
}

// grep adds the lines of the file at p that match re to matches; a file it can't read, a large or a binary one, adds
// none.
func (w *Workspace) grep(p string, re *regexp.Regexp, matches []string) []string {
	if info, err := w.root.Stat(p); err != nil || info.Size() > maxSearched {
		return matches
	}
	data, err := w.root.ReadFile(p)
	if err != nil || binary(data) {
		return matches
	}
	lines := bufio.NewScanner(bytes.NewReader(data))
	lines.Buffer(make([]byte, 0, 64<<10), maxSearched)
	for n := 1; lines.Scan() && len(matches) < maxMatches; n++ {
		if line := lines.Text(); re.MatchString(line) {
			matches = append(matches, p+":"+strconv.Itoa(n)+": "+cut(line, maxMatchLength))
		}
	}
	return matches
}

// cut is s cut to at most n bytes, at a whole character.
func cut(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && s[n]&0xC0 == 0x80 {
		n--
	}
	return s[:n] + "…"
}
