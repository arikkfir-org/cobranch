package code

import (
	"context"
	"fmt"
	"unicode/utf8"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
)

// Objects reads a repository's Git objects, which only the Git server holds: the Git server's internal call. Each read
// resolves ref in one snapshot of the repository and answers ErrRefNotFound, ErrPathNotFound, ErrNotTree, ErrNotBlob
// or ErrSubmodule for what it doesn't find there.
type Objects interface {
	// Tree reads the directory at path in the commit ref names.
	Tree(ctx context.Context, repoID uuid.UUID, ref, path string) (Tree, error)
	// Blob reads the file or symlink at path in the commit ref names, its content up to limit bytes.
	Blob(ctx context.Context, repoID uuid.UUID, ref, path string, limit int64) (Blob, error)
	// Raw streams the bytes of the file or symlink at path in the commit ref names.
	Raw(ctx context.Context, repoID uuid.UUID, ref, path string) (Raw, error)
	// Log lists at most limit of the commits reachable from the commit ref names, every parent followed, newest first
	// as git log --date-order lists them, after skipping the first skip.
	Log(ctx context.Context, repoID uuid.UUID, ref string, skip, limit int) (Log, error)
}

// unavailable answers every read with ErrUnavailable, for an installation that runs no Git server.
type unavailable struct{}

func (unavailable) Tree(context.Context, uuid.UUID, string, string) (Tree, error) {
	return Tree{}, ErrUnavailable
}

func (unavailable) Blob(context.Context, uuid.UUID, string, string, int64) (Blob, error) {
	return Blob{}, ErrUnavailable
}

func (unavailable) Raw(context.Context, uuid.UUID, string, string) (Raw, error) {
	return Raw{}, ErrUnavailable
}

func (unavailable) Log(context.Context, uuid.UUID, string, int, int) (Log, error) {
	return Log{}, ErrUnavailable
}

// Tags lists the repository's tags by name, each with the object its ref names; it takes repo.read.
func (s *Service) Tags(ctx context.Context, v Viewer, repoID uuid.UUID) ([]Tag, error) {
	ctx, span := tracer.Start(ctx, "code.Service.Tags")
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.repository.id", repoID.String()))
	if err := s.read(ctx, v, repoID); err != nil {
		return nil, failed(span, "reading the tags", err)
	}
	tags, err := s.store.Tags(ctx, repoID)
	if err != nil {
		return nil, failed(span, "reading the tags", err)
	}
	return tags, nil
}

// Tree reads the directory at path in the commit ref names (a full commit name, a branch or a tag, in that order),
// its directories first and then its files, each by name, and its README; it takes repo.read.
func (s *Service) Tree(ctx context.Context, v Viewer, repoID uuid.UUID, ref, path string) (Tree, error) {
	ctx, span := tracer.Start(ctx, "code.Service.Tree")
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.repository.id", repoID.String()), attribute.String("cobranch.ref", ref), attribute.String("cobranch.path", path))
	if err := s.check(ctx, v, repoID, ref, path); err != nil {
		return Tree{}, failed(span, "reading the tree", err)
	}
	tree, err := s.objects.Tree(ctx, repoID, ref, path)
	if err != nil {
		return Tree{}, failed(span, "reading the tree", err)
	}
	sortEntries(tree.Entries)
	if tree, err = s.withREADME(ctx, repoID, ref, tree); err != nil {
		return Tree{}, failed(span, "reading the tree", err)
	}
	return tree, nil
}

// File reads the file or symlink at path in the commit ref names: whether it is binary and, when it isn't, its text
// up to MaxText, rendered as HTML when it is Markdown; it takes repo.read.
func (s *Service) File(ctx context.Context, v Viewer, repoID uuid.UUID, ref, path string) (File, error) {
	ctx, span := tracer.Start(ctx, "code.Service.File")
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.repository.id", repoID.String()), attribute.String("cobranch.ref", ref), attribute.String("cobranch.path", path))
	if err := s.check(ctx, v, repoID, ref, path); err != nil {
		return File{}, failed(span, "reading the file", err)
	}
	if path == "" {
		return File{}, ErrNotBlob
	}
	blob, err := s.objects.Blob(ctx, repoID, ref, path, MaxText)
	if err != nil {
		return File{}, failed(span, "reading the file", err)
	}
	f, err := s.render(ctx, repoID, ref, file(blob))
	if err != nil {
		return File{}, failed(span, "reading the file", err)
	}
	return f, nil
}

// file is blob as people read it: its text, unless it is binary, without a character the limit cut in two.
func file(blob Blob) File {
	f := File{Commit: blob.Commit, Path: blob.Path, Name: name(blob.Path), Kind: blob.Kind, Mode: blob.Mode, SHA: blob.SHA, Size: blob.Size,
		Truncated: blob.Truncated, Binary: Binary(blob.Content, blob.Truncated)}
	if f.Binary {
		return f
	}
	text := blob.Content
	for !utf8.Valid(text) {
		text = text[:len(text)-1]
	}
	f.Text = string(text)
	return f
}

// Raw streams the bytes of the file or symlink at path in the commit ref names; the caller closes its Body. It takes
// repo.read.
func (s *Service) Raw(ctx context.Context, v Viewer, repoID uuid.UUID, ref, path string) (Raw, error) {
	ctx, span := tracer.Start(ctx, "code.Service.Raw")
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.repository.id", repoID.String()), attribute.String("cobranch.ref", ref), attribute.String("cobranch.path", path))
	if err := s.check(ctx, v, repoID, ref, path); err != nil {
		return Raw{}, failed(span, "reading the file's bytes", err)
	}
	if path == "" {
		return Raw{}, ErrNotBlob
	}
	raw, err := s.objects.Raw(ctx, repoID, ref, path)
	if err != nil {
		return Raw{}, failed(span, "reading the file's bytes", err)
	}
	return raw, nil
}

// check checks that v reads the repository, then that ref and path are ones the Git server takes.
func (s *Service) check(ctx context.Context, v Viewer, repoID uuid.UUID, ref, path string) error {
	if err := s.read(ctx, v, repoID); err != nil {
		return err
	}
	if err := ValidateRef(ref); err != nil {
		return fmt.Errorf("checking the ref: %w", err)
	}
	if err := ValidatePath(path); err != nil {
		return fmt.Errorf("checking the path: %w", err)
	}
	return nil
}
