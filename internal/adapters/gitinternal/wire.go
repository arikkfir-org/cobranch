// Package gitinternal is the internal call to the Git server, on a port no route names: the REST and GraphQL servers
// read a repository's trees, files and history with it, since only the Git server holds Git's objects. It holds the
// call's paths, its answers as JSON and the codes of the answers that find nothing, which the Git server writes, and
// the client that reads them, code.Objects
// (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/code-browsing.md, "The internal call").
package gitinternal

import (
	"errors"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/repos"
)

// ReposPath is the prefix of the internal call's paths, under a repository's ID: GET <ReposPath>{id}/tree, /blob and
// /raw, each with the query parameters ref and path, and blob with limit; /log, with ref, skip and limit; /diff, with
// ref, the head, and base and mergeBase; and POST <ReposPath>{id}/merge, with a MergeRequest.
const ReposPath = "/internal/v1/repos/"

// The headers of a raw answer, beside its Content-Length, which name what its bytes are.
const (
	HeaderCommit = "Cobranch-Commit"
	HeaderObject = "Cobranch-Object"
)

// Entry is a tree's entry as the internal call carries it: code.Entry.
type Entry struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Kind string `json:"kind"`
	Mode string `json:"mode"`
	SHA  string `json:"sha"`
	Size int64  `json:"size"`
}

// Tree is a directory at a commit as the internal call carries it: code.Tree.
type Tree struct {
	Commit  string  `json:"commit"`
	Path    string  `json:"path"`
	SHA     string  `json:"sha"`
	Entries []Entry `json:"entries"`
}

// Blob is a file or a symlink at a commit, with its content up to the limit asked for, as the internal call carries
// it: code.Blob. Its content travels in base64.
type Blob struct {
	Commit    string `json:"commit"`
	Path      string `json:"path"`
	Kind      string `json:"kind"`
	Mode      string `json:"mode"`
	SHA       string `json:"sha"`
	Size      int64  `json:"size"`
	Content   []byte `json:"content"`
	Truncated bool   `json:"truncated"`
}

// LogEntry is a commit as a walk of history names it, as the internal call carries it: code.LogEntry.
type LogEntry struct {
	SHA     string   `json:"sha"`
	Parents []string `json:"parents"`
}

// Log is a page of a walk of history as the internal call carries it: code.Log.
type Log struct {
	Commit  string     `json:"commit"`
	Entries []LogEntry `json:"entries"`
}

// LogOf is log as the internal call carries it.
func LogOf(log code.Log) Log {
	entries := make([]LogEntry, 0, len(log.Entries))
	for _, e := range log.Entries {
		entries = append(entries, LogEntry{SHA: e.SHA, Parents: e.Parents})
	}
	return Log{Commit: log.Commit, Entries: entries}
}

// Domain is the domain's log.
func (l Log) Domain() code.Log {
	entries := make([]code.LogEntry, 0, len(l.Entries))
	for _, e := range l.Entries {
		entries = append(entries, code.LogEntry{SHA: e.SHA, Parents: e.Parents})
	}
	return code.Log{Commit: l.Commit, Entries: entries}
}

// DiffLine is a line of a hunk as the internal call carries it: code.DiffLine.
type DiffLine struct {
	Kind      string `json:"kind"`
	Old       int    `json:"old,omitempty"`
	New       int    `json:"new,omitempty"`
	Text      string `json:"text"`
	NoNewline bool   `json:"noNewline,omitempty"`
}

// Hunk is a run of a file's changes as the internal call carries it: code.Hunk.
type Hunk struct {
	OldStart int        `json:"oldStart"`
	OldLines int        `json:"oldLines"`
	NewStart int        `json:"newStart"`
	NewLines int        `json:"newLines"`
	Section  string     `json:"section,omitempty"`
	Lines    []DiffLine `json:"lines"`
}

// FileDiff is a file's change as the internal call carries it: code.FileDiff.
type FileDiff struct {
	Status    string `json:"status"`
	Path      string `json:"path"`
	OldPath   string `json:"oldPath,omitempty"`
	OldMode   string `json:"oldMode,omitempty"`
	Mode      string `json:"mode,omitempty"`
	Binary    bool   `json:"binary,omitempty"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Truncated bool   `json:"truncated,omitempty"`
	Hunks     []Hunk `json:"hunks"`
}

// Diff is a change from one commit to another as the internal call carries it: code.Diff.
type Diff struct {
	Base         string     `json:"base,omitempty"`
	BaseTip      string     `json:"baseTip,omitempty"`
	Head         string     `json:"head"`
	Files        []FileDiff `json:"files"`
	FilesLeftOut int        `json:"filesLeftOut,omitempty"`
	Additions    int        `json:"additions"`
	Deletions    int        `json:"deletions"`
}

// DiffOf is d as the internal call carries it.
func DiffOf(d code.Diff) Diff {
	files := make([]FileDiff, 0, len(d.Files))
	for _, f := range d.Files {
		hunks := make([]Hunk, 0, len(f.Hunks))
		for _, h := range f.Hunks {
			lines := make([]DiffLine, 0, len(h.Lines))
			for _, l := range h.Lines {
				lines = append(lines, DiffLine{Kind: string(l.Kind), Old: l.Old, New: l.New, Text: l.Text, NoNewline: l.NoNewline})
			}
			hunks = append(hunks, Hunk{OldStart: h.OldStart, OldLines: h.OldLines, NewStart: h.NewStart, NewLines: h.NewLines, Section: h.Section, Lines: lines})
		}
		files = append(files, FileDiff{Status: string(f.Status), Path: f.Path, OldPath: f.OldPath, OldMode: f.OldMode, Mode: f.Mode, Binary: f.Binary,
			Additions: f.Additions, Deletions: f.Deletions, Truncated: f.Truncated, Hunks: hunks})
	}
	return Diff{Base: d.Base, BaseTip: d.BaseTip, Head: d.Head, Files: files, FilesLeftOut: d.FilesLeftOut, Additions: d.Additions, Deletions: d.Deletions}
}

// Domain is the domain's diff.
func (d Diff) Domain() code.Diff {
	files := make([]code.FileDiff, 0, len(d.Files))
	for _, f := range d.Files {
		hunks := make([]code.Hunk, 0, len(f.Hunks))
		for _, h := range f.Hunks {
			lines := make([]code.DiffLine, 0, len(h.Lines))
			for _, l := range h.Lines {
				lines = append(lines, code.DiffLine{Kind: code.LineKind(l.Kind), Old: l.Old, New: l.New, Text: l.Text, NoNewline: l.NoNewline})
			}
			hunks = append(hunks, code.Hunk{OldStart: h.OldStart, OldLines: h.OldLines, NewStart: h.NewStart, NewLines: h.NewLines, Section: h.Section, Lines: lines})
		}
		files = append(files, code.FileDiff{Status: code.FileStatus(f.Status), Path: f.Path, OldPath: f.OldPath, OldMode: f.OldMode, Mode: f.Mode,
			Binary: f.Binary, Additions: f.Additions, Deletions: f.Deletions, Truncated: f.Truncated, Hunks: hunks})
	}
	return code.Diff{Base: d.Base, BaseTip: d.BaseTip, Head: d.Head, Files: files, FilesLeftOut: d.FilesLeftOut, Additions: d.Additions,
		Deletions: d.Deletions}
}

// MergeRequest is a merge the internal call asks the Git server to build and record: code.MergeRequest.
type MergeRequest struct {
	Branch string `json:"branch"`
	Head   string `json:"head"`
	Base   string `json:"base"`
	Method string `json:"method"`
	By     Merger `json:"by"`
	Bypass bool   `json:"bypass"`
}

// Merger is who asks for a merge: code.Merger.
type Merger struct {
	ID    uuid.UUID `json:"id"`
	Name  string    `json:"name"`
	Email string    `json:"email"`
}

// Merged is the answer of a merge the Git server recorded: code.Merged.
type Merged struct {
	Base   string `json:"base"`
	Head   string `json:"head"`
	Commit string `json:"commit"`
}

// MergeRequestOf is req as the internal call carries it.
func MergeRequestOf(req code.MergeRequest) MergeRequest {
	return MergeRequest{Branch: req.Branch, Head: req.Head, Base: req.Base, Method: string(req.Method),
		By: Merger{ID: req.By.ID, Name: req.By.Name, Email: req.By.Email}, Bypass: req.Bypass}
}

// Domain is the domain's merge request.
func (r MergeRequest) Domain() code.MergeRequest {
	return code.MergeRequest{Branch: r.Branch, Head: r.Head, Base: r.Base, Method: code.MergeMethod(r.Method),
		By: code.Merger{ID: r.By.ID, Name: r.By.Name, Email: r.By.Email}, Bypass: r.Bypass}
}

// MergedOf is m as the internal call carries it.
func MergedOf(m code.Merged) Merged {
	return Merged{Base: m.Base, Head: m.Head, Commit: m.Commit}
}

// Domain is the domain's merge.
func (m Merged) Domain() code.Merged {
	return code.Merged{Base: m.Base, Head: m.Head, Commit: m.Commit}
}

// CodeMoved is the code of the 409 of a merge whose branch or base moved; any other 409 carries a *code.MergeError's.
const CodeMoved = "branch.moved"

// Problem is the body of an answer other than 200: a code, for one that finds nothing, and what went wrong.
type Problem struct {
	Code    string `json:"code,omitempty"`
	Message string `json:"message"`
}

// TreeOf is tree as the internal call carries it.
func TreeOf(tree code.Tree) Tree {
	entries := make([]Entry, 0, len(tree.Entries))
	for _, e := range tree.Entries {
		entries = append(entries, Entry{Name: e.Name, Path: e.Path, Kind: string(e.Kind), Mode: e.Mode, SHA: e.SHA, Size: e.Size})
	}
	return Tree{Commit: tree.Commit, Path: tree.Path, SHA: tree.SHA, Entries: entries}
}

// Domain is the domain's tree.
func (t Tree) Domain() code.Tree {
	entries := make([]code.Entry, 0, len(t.Entries))
	for _, e := range t.Entries {
		entries = append(entries, code.Entry{Name: e.Name, Path: e.Path, Kind: code.EntryKind(e.Kind), Mode: e.Mode, SHA: e.SHA, Size: e.Size})
	}
	return code.Tree{Commit: t.Commit, Path: t.Path, SHA: t.SHA, Entries: entries}
}

// BlobOf is blob as the internal call carries it.
func BlobOf(blob code.Blob) Blob {
	return Blob{Commit: blob.Commit, Path: blob.Path, Kind: string(blob.Kind), Mode: blob.Mode, SHA: blob.SHA, Size: blob.Size,
		Content: blob.Content, Truncated: blob.Truncated}
}

// Domain is the domain's blob.
func (b Blob) Domain() code.Blob {
	return code.Blob{Commit: b.Commit, Path: b.Path, Kind: code.EntryKind(b.Kind), Mode: b.Mode, SHA: b.SHA, Size: b.Size,
		Content: b.Content, Truncated: b.Truncated}
}

// answers are the domain's errors for what a read doesn't find, by the code of the 404 that carries each.
var answers = map[string]error{
	"repo.not_found": repos.ErrNotFound,
	"ref.not_found":  code.ErrRefNotFound,
	"path.not_found": code.ErrPathNotFound,
	"path.file":      code.ErrNotTree,
	"path.directory": code.ErrNotBlob,
	"path.submodule": code.ErrSubmodule,
}

// CodeOf is the code of the 404 that carries err, one of the domain's answers about a repository, a ref or a path, or
// false for any other error.
func CodeOf(err error) (string, bool) {
	for c, answer := range answers {
		if errors.Is(err, answer) {
			return c, true
		}
	}
	return "", false
}
