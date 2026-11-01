package codetest

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"io"
	"slices"
	"strings"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/code"
)

// Objects is a code.Objects in memory, the Git server's objects for tests: the files of each commit, by path, and the
// branches and tags that name commits. A path ending in Symlink holds a symlink, and one in Submodule a submodule.
// Every repository holds the same objects. Fail, when set, fails every read.
type Objects struct {
	Files map[string]map[string]string
	Refs  map[string]string
	Fail  error
}

// The suffixes of the paths Objects holds a symlink or a submodule at.
const (
	Symlink   = ".link"
	Submodule = ".module"
)

// NewObjects returns an Objects without commits.
func NewObjects() *Objects {
	return &Objects{Files: map[string]map[string]string{}, Refs: map[string]string{}}
}

// Commit records the files of the commit named sha, by path, and makes refs name it.
func (o *Objects) Commit(sha string, files map[string]string, refs ...string) {
	o.Files[sha] = files
	for _, ref := range refs {
		o.Refs[ref] = sha
	}
}

// resolve finds the commit ref names: a commit's full name, or a branch or a tag.
func (o *Objects) resolve(ref string) (string, map[string]string, error) {
	if o.Fail != nil {
		return "", nil, o.Fail
	}
	if files, ok := o.Files[ref]; ok {
		return ref, files, nil
	}
	if sha, ok := o.Refs[ref]; ok {
		return sha, o.Files[sha], nil
	}
	return "", nil, code.ErrRefNotFound
}

// Tree lists the directory at path of the commit ref names, in no order.
func (o *Objects) Tree(_ context.Context, _ uuid.UUID, ref, path string) (code.Tree, error) {
	commit, files, err := o.resolve(ref)
	if err != nil {
		return code.Tree{}, err
	}
	if _, ok := files[path]; ok {
		return code.Tree{}, notTree(path)
	}
	tree := code.Tree{Commit: commit, Path: path, SHA: name("tree " + commit + ":" + path)}
	prefix := path + "/"
	if path == "" {
		prefix = ""
	}
	for p, content := range files {
		rest, ok := strings.CutPrefix(p, prefix)
		if !ok {
			continue
		}
		child, _, deeper := strings.Cut(rest, "/")
		e := code.Entry{Name: child, Path: prefix + child, Kind: kind(child), Mode: "100644", SHA: name(content), Size: int64(len(content))}
		if deeper {
			e = code.Entry{Name: child, Path: prefix + child, Kind: code.KindTree, Mode: "040000", SHA: name("tree " + commit + ":" + prefix + child)}
		}
		if !slices.ContainsFunc(tree.Entries, func(o code.Entry) bool { return o.Name == child }) {
			tree.Entries = append(tree.Entries, e)
		}
	}
	if len(tree.Entries) == 0 && path != "" {
		return code.Tree{}, code.ErrPathNotFound
	}
	return tree, nil
}

// Blob reads the file at path of the commit ref names, up to limit bytes.
func (o *Objects) Blob(_ context.Context, _ uuid.UUID, ref, path string, limit int64) (code.Blob, error) {
	commit, content, err := o.file(ref, path)
	if err != nil {
		return code.Blob{}, err
	}
	b := code.Blob{Commit: commit, Path: path, Kind: kind(path), Mode: "100644", SHA: name(content), Size: int64(len(content)), Content: []byte(content)}
	if b.Size > limit {
		b.Content, b.Truncated = b.Content[:limit], true
	}
	return b, nil
}

// Raw streams the file at path of the commit ref names.
func (o *Objects) Raw(_ context.Context, _ uuid.UUID, ref, path string) (code.Raw, error) {
	commit, content, err := o.file(ref, path)
	if err != nil {
		return code.Raw{}, err
	}
	return code.Raw{Commit: commit, Path: path, SHA: name(content), Size: int64(len(content)), Body: io.NopCloser(strings.NewReader(content))}, nil
}

// file finds the file at path of the commit ref names.
func (o *Objects) file(ref, path string) (string, string, error) {
	commit, files, err := o.resolve(ref)
	if err != nil {
		return "", "", err
	}
	content, ok := files[path]
	switch {
	case ok && kind(path) == code.KindSubmodule:
		return "", "", code.ErrSubmodule
	case ok:
		return commit, content, nil
	}
	for p := range files {
		if strings.HasPrefix(p, path+"/") {
			return "", "", code.ErrNotBlob
		}
	}
	return "", "", code.ErrPathNotFound
}

// notTree is the error of a tree asked of a file's path.
func notTree(path string) error {
	if kind(path) == code.KindSubmodule {
		return code.ErrSubmodule
	}
	return code.ErrNotTree
}

// kind is the kind of the entry at path.
func kind(path string) code.EntryKind {
	switch {
	case strings.HasSuffix(path, Symlink):
		return code.KindSymlink
	case strings.HasSuffix(path, Submodule):
		return code.KindSubmodule
	}
	return code.KindBlob
}

// name is an object's name made from its content, as Git makes one.
func name(content string) string {
	sum := sha1.Sum([]byte(content))
	return hex.EncodeToString(sum[:])
}
