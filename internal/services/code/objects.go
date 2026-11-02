package code

import (
	"errors"
	"io"
	"strings"
	"unicode/utf8"

	"cobranch.dev/internal/services/orgs"
)

// EntryKind is what a tree's entry is: a directory, a file, a symlink, or a submodule, which pins a commit of another
// repository.
type EntryKind string

// The kinds of a tree's entries.
const (
	KindTree      EntryKind = "tree"
	KindBlob      EntryKind = "blob"
	KindSymlink   EntryKind = "symlink"
	KindSubmodule EntryKind = "submodule"
)

// Entry is one of a tree's entries: its name, its path from the repository's root, its kind and mode as Git writes it
// (such as 100644), the object it names, and a file's or a symlink's size.
type Entry struct {
	Name string
	Path string
	Kind EntryKind
	Mode string
	SHA  string
	Size int64
}

// Tree is a directory at a commit: the commit its ref resolved to, its path (empty for the root), its own object, its
// entries and its README, when it holds one.
type Tree struct {
	Commit  string
	Path    string
	SHA     string
	Entries []Entry
	README  *File
}

// Blob is a file or a symlink at a commit, as the Git server reads it: the commit its ref resolved to, its path, kind,
// mode, object and size, and its content up to the limit asked for, Truncated when there is more.
type Blob struct {
	Commit    string
	Path      string
	Kind      EntryKind
	Mode      string
	SHA       string
	Size      int64
	Content   []byte
	Truncated bool
}

// Raw is a file's bytes at a commit, as a stream the caller closes, with what names them.
type Raw struct {
	Commit string
	Path   string
	SHA    string
	Size   int64
	Body   io.ReadCloser
}

// File is a file or a symlink as people read it: a Blob's facts, whether it is binary, its text, when it is not
// binary, up to MaxText, and a Markdown file's text rendered as HTML; a symlink's text is its target.
type File struct {
	Commit    string
	Path      string
	Name      string
	Kind      EntryKind
	Mode      string
	SHA       string
	Size      int64
	Binary    bool
	Truncated bool
	Text      string
	HTML      string
}

// LogEntry is a commit as a walk of history names it: its name and its parents', the first first.
type LogEntry struct {
	SHA     string
	Parents []string
}

// Log is a page of a walk of history, as the Git server reads it: the commit its ref resolved to, and the commits the
// page holds, reachable from it with every parent followed, newest first.
type Log struct {
	Commit  string
	Entries []LogEntry
}

// Tag is one of a repository's tags and the object its ref names: a commit, or an annotated tag that names one.
type Tag struct {
	Name   string
	Target string
}

// MaxText is how much of a file's text the API shows; the rest is in its raw bytes.
const MaxText = 1 << 20

// binaryProbe is how much of a file Git looks into for a NUL to call it binary.
const binaryProbe = 8000

// The answers about a ref or a path that the Git server gives.
var (
	// ErrRefNotFound is a ref that names no branch, tag or commit of the repository.
	ErrRefNotFound = errors.New("no branch, tag or commit of the repository has this name")
	// ErrPathNotFound is a path that the commit's tree doesn't hold.
	ErrPathNotFound = errors.New("the commit holds nothing at this path")
	// ErrNotTree is a tree asked of a path that holds a file, a symlink or a submodule.
	ErrNotTree = errors.New("the path holds a file, not a directory")
	// ErrNotBlob is a file asked of a path that holds a directory.
	ErrNotBlob = errors.New("the path holds a directory, not a file")
	// ErrSubmodule is a file or a tree asked of a submodule, whose commit is another repository's.
	ErrSubmodule = errors.New("the path holds a submodule, whose files are another repository's")
	// ErrUnavailable is a read that needs the Git server, in an installation that runs none.
	ErrUnavailable = errors.New("this installation runs no Git server to read files with")
)

// ValidateRef reports whether ref may name a branch, a tag or a commit: a full commit name, or a ref name Git would
// take under refs/heads/ or refs/tags/. Nothing else reaches git's revision parser, so no URL asks it for HEAD~2.
func ValidateRef(ref string) error {
	if shaPattern.MatchString(ref) {
		return nil
	}
	invalid := &orgs.ValidationError{Field: "ref", Code: "ref.invalid", Message: "a branch's or a tag's name, or a commit's full name"}
	if ref == "" || len(ref) > 255 || strings.HasPrefix(ref, "-") || strings.HasPrefix(ref, "/") || strings.HasSuffix(ref, "/") ||
		strings.HasSuffix(ref, ".") || strings.HasSuffix(ref, ".lock") || strings.Contains(ref, "..") || strings.Contains(ref, "//") ||
		strings.Contains(ref, "@{") || ref == "@" {
		return invalid
	}
	for _, r := range ref {
		if r < 0x20 || r == 0x7f || strings.ContainsRune(" ~^:?*[\\", r) {
			return invalid
		}
	}
	for part := range strings.SplitSeq(ref, "/") {
		if strings.HasPrefix(part, ".") {
			return invalid
		}
	}
	return nil
}

// ValidatePath reports whether path names a place in a tree: names separated by /, none of them empty, . or .., and
// no NUL. The root is the empty path.
func ValidatePath(path string) error {
	if path == "" {
		return nil
	}
	invalid := &orgs.ValidationError{Field: "path", Code: "path.invalid", Message: "names separated by /, from the repository's root, without . or .."}
	if len(path) > 4096 || strings.ContainsRune(path, 0) || !utf8.ValidString(path) {
		return invalid
	}
	for part := range strings.SplitSeq(path, "/") {
		if part == "" || part == "." || part == ".." {
			return invalid
		}
	}
	return nil
}

// Binary reports whether content is binary as Git judges a file: a NUL in its first 8000 bytes; or, unlike Git, bytes
// that aren't UTF-8, which the API's text can't carry. A truncated file's last character may be cut in two, so only
// its last three bytes may be incomplete.
func Binary(content []byte, truncated bool) bool {
	probe := content[:min(len(content), binaryProbe)]
	for _, b := range probe {
		if b == 0 {
			return true
		}
	}
	if truncated {
		for cut := 0; cut < utf8.UTFMax && cut <= len(content); cut++ {
			if utf8.Valid(content[:len(content)-cut]) {
				return false
			}
		}
		return true
	}
	return !utf8.Valid(content)
}

// name is the last name in path.
func name(path string) string {
	return path[strings.LastIndex(path, "/")+1:]
}
