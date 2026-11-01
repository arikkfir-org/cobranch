// Package gitinternal is the internal call to the Git server, on a port no route names: the REST and GraphQL servers
// read a repository's trees and files with it, since only the Git server holds Git's objects. It holds the call's
// paths, its answers as JSON and the codes of the answers that find nothing, which the Git server writes, and the
// client that reads them, code.Objects
// (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/code-browsing.md, "The internal call").
package gitinternal

import (
	"errors"

	"cobranch.dev/internal/services/code"
)

// ReposPath is the prefix of the internal call's paths, under a repository's ID: GET <ReposPath>{id}/tree, /blob and
// /raw, each with the query parameters ref and path, and blob with limit.
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
	"ref.not_found":  code.ErrRefNotFound,
	"path.not_found": code.ErrPathNotFound,
	"path.file":      code.ErrNotTree,
	"path.directory": code.ErrNotBlob,
	"path.submodule": code.ErrSubmodule,
}

// CodeOf is the code of the 404 that carries err, one of the domain's answers about a ref or a path, or false for any
// other error.
func CodeOf(err error) (string, bool) {
	for c, answer := range answers {
		if errors.Is(err, answer) {
			return c, true
		}
	}
	return "", false
}
