package code

import (
	"context"
	"fmt"
	"path"
	"strings"

	"github.com/google/uuid"
)

// Document is a Markdown file a Renderer renders, with where it is, so that its relative links and images resolve:
// the repository's slug and its organization's, the ref it was asked at (links keep it, so a reader stays on a branch),
// the commit that ref resolved to (images name it, so they never change) and its path.
type Document struct {
	Org    string
	Repo   string
	Ref    string
	Commit string
	Path   string
}

// Renderer renders a repository's Markdown as HTML that runs nothing and loads nothing from outside the installation
// but images, which the viewer's browser fetches: the code browsing design's Markdown, which an adapter implements.
type Renderer interface {
	Render(ctx context.Context, src []byte, doc Document) (string, error)
}

// markdownExtensions are the extensions of the files that render as Markdown, as GitHub's.
var markdownExtensions = []string{".md", ".markdown", ".mdown", ".mkd", ".mkdn"}

// readmeNames are the names a directory's README goes by, in the order one is picked, whatever their case.
var readmeNames = []string{"readme.md", "readme.markdown", "readme.mdown", "readme.mkd", "readme.mkdn", "readme.txt", "readme"}

// IsMarkdown reports whether the file at p renders as Markdown, by its extension, whatever its case.
func IsMarkdown(p string) bool {
	ext := strings.ToLower(path.Ext(p))
	for _, e := range markdownExtensions {
		if ext == e {
			return true
		}
	}
	return false
}

// readme is the entry of a directory's README among entries: the first of readmeNames that names one of its files,
// whatever its case; nil when none does.
func readme(entries []Entry) *Entry {
	for _, n := range readmeNames {
		for i, e := range entries {
			if e.Kind == KindBlob && strings.ToLower(e.Name) == n {
				return &entries[i]
			}
		}
	}
	return nil
}

// withREADME adds the README of the directory tree holds, read at the commit tree's ref resolved to, as its file,
// rendered when it is Markdown; a binary README is none.
func (s *Service) withREADME(ctx context.Context, repoID uuid.UUID, ref string, tree Tree) (Tree, error) {
	e := readme(tree.Entries)
	if e == nil {
		return tree, nil
	}
	blob, err := s.objects.Blob(ctx, repoID, tree.Commit, e.Path, MaxText)
	if err != nil {
		return Tree{}, fmt.Errorf("reading the README %s: %w", e.Path, err)
	}
	f := file(blob)
	if f.Binary {
		return tree, nil
	}
	if f, err = s.render(ctx, repoID, ref, f); err != nil {
		return Tree{}, err
	}
	tree.README = &f
	return tree, nil
}

// render adds f's text rendered as HTML when f is a Markdown file and the service has a Renderer; ref is the ref f
// was asked at.
func (s *Service) render(ctx context.Context, repoID uuid.UUID, ref string, f File) (File, error) {
	if s.markdown == nil || f.Binary || f.Kind != KindBlob || !IsMarkdown(f.Path) {
		return f, nil
	}
	org, repo, err := s.store.Slugs(ctx, repoID)
	if err != nil {
		return File{}, fmt.Errorf("reading where %s is: %w", f.Path, err)
	}
	doc := Document{Org: org, Repo: repo, Ref: ref, Commit: f.Commit, Path: f.Path}
	if f.HTML, err = s.markdown.Render(ctx, []byte(f.Text), doc); err != nil {
		return File{}, fmt.Errorf("rendering %s: %w", f.Path, err)
	}
	return f, nil
}
