package code

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/git"
	"cobranch.dev/internal/services/orgs"
	"cobranch.dev/internal/services/repos"
)

var tracer = otel.Tracer("cobranch.dev/internal/services/code")

// Store reads what pushes recorded of a repository's code.
type Store interface {
	// Branches lists the repository's live branches, each with its head commit when a push recorded it.
	Branches(ctx context.Context, repoID uuid.UUID) ([]Branch, error)
	// Branch finds one of the repository's live branches by name, with its head commit, or repos.ErrNotFound.
	Branch(ctx context.Context, repoID uuid.UUID, name string) (Branch, error)
	// FirstParents lists at most limit of the repository's commits, from sha along each one's first parent, nearest
	// first: none when the repository recorded no commit sha.
	FirstParents(ctx context.Context, repoID uuid.UUID, sha string, limit int) ([]Commit, error)
	// CommitsAt finds those of the repository's commits that shas name, by name: none for a name no push recorded as
	// a commit.
	CommitsAt(ctx context.Context, repoID uuid.UUID, shas []string) (map[string]Commit, error)
	// Commit finds one of the repository's commits with the push that first brought it, or repos.ErrNotFound.
	Commit(ctx context.Context, repoID uuid.UUID, sha string) (Commit, Push, error)
	// Annotations lists the annotations on one of the repository's commits by name, or repos.ErrNotFound when the
	// repository holds no such commit.
	Annotations(ctx context.Context, repoID uuid.UUID, sha string) ([]Annotation, error)
	// SetAnnotation sets the annotation in names on one of the repository's commits for actor, or changes the one
	// actor set there before, records the change unless it changes nothing, and answers the annotation as it now is:
	// repos.ErrNotFound when the repository holds no such commit, a *TakenError when another principal set it, and
	// ErrAnnotationChanged when it is at another version than in names, isn't there while in names one, or is there
	// while in names NoAnnotation.
	SetAnnotation(ctx context.Context, actor uuid.UUID, repoID uuid.UUID, sha string, in AnnotationInput) (Annotation, error)
	// DeleteAnnotation deletes the annotation of name from one of the repository's commits and records it, when actor
	// set it or anyone is true: repos.ErrNotFound when there is none and version is 0, a *TakenError when another
	// principal set it, and ErrAnnotationChanged when version names another than it is, or one that isn't there.
	DeleteAnnotation(ctx context.Context, actor uuid.UUID, repoID uuid.UUID, sha, name string, version int64, anyone bool) error
	// Tags lists the repository's tags by name.
	Tags(ctx context.Context, repoID uuid.UUID) ([]Tag, error)
	// Slugs finds the repository's slug and its organization's, or repos.ErrNotFound.
	Slugs(ctx context.Context, repoID uuid.UUID) (org, repo string, err error)
}

// Service serves a repository's code to the viewers who read it: every operation checks repo.read through the
// authorizer first, and answers repos.ErrNotFound without it, as for a repository that doesn't exist.
type Service struct {
	store    Store
	objects  Objects
	markdown Renderer
	authz    *auth.Authorizer
}

// NewService returns a Service over store, objects for what only the Git server reads and markdown for rendering
// Markdown files and READMEs, checking permissions with authz. Without objects, as in an installation that runs no Git
// server, those reads answer ErrUnavailable; without markdown, Markdown answers as text alone.
func NewService(store Store, objects Objects, markdown Renderer, authz *auth.Authorizer) *Service {
	if objects == nil {
		objects = unavailable{}
	}
	return &Service{store: store, objects: objects, markdown: markdown, authz: authz}
}

// Viewer is who an operation runs for: a signed-in user, or the user or API key a credential acts as.
type Viewer = auth.User

// read checks that v reads the repository, or answers repos.ErrNotFound.
func (s *Service) read(ctx context.Context, v Viewer, repoID uuid.UUID) error {
	_, err := s.holds(ctx, v, repoID)
	return err
}

// holds returns what v holds on the repository, or repos.ErrNotFound without repo.read.
func (s *Service) holds(ctx context.Context, v Viewer, repoID uuid.UUID) ([]string, error) {
	held, err := s.authz.Holds(ctx, v.ID, auth.ScopeRepository, repoID)
	if err != nil {
		return nil, fmt.Errorf("reading the viewer's permissions: %w", err)
	}
	if !slices.Contains(held, repos.PermRead) {
		return nil, repos.ErrNotFound
	}
	return held, nil
}

// Branches lists the repository's live branches, main first and then by name, each with the commit it is at; it
// takes repo.read.
func (s *Service) Branches(ctx context.Context, v Viewer, repoID uuid.UUID) ([]Branch, error) {
	ctx, span := tracer.Start(ctx, "code.Service.Branches")
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.repository.id", repoID.String()))
	if err := s.read(ctx, v, repoID); err != nil {
		return nil, failed(span, "reading the branches", err)
	}
	branches, err := s.store.Branches(ctx, repoID)
	if err != nil {
		return nil, failed(span, "reading the branches", err)
	}
	slices.SortFunc(branches, func(a, b Branch) int {
		return cmp.Or(cmp.Compare(rank(a), rank(b)), cmp.Compare(a.Name, b.Name))
	})
	return branches, nil
}

// Branch finds one of the repository's live branches by name, with the commit it is at, its status and what a merge
// into it needs; it takes repo.read. A branch that isn't there is repos.ErrNotFound.
func (s *Service) Branch(ctx context.Context, v Viewer, repoID uuid.UUID, name string) (Branch, error) {
	ctx, span := tracer.Start(ctx, "code.Service.Branch")
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.repository.id", repoID.String()), attribute.String("cobranch.branch", name))
	if err := s.read(ctx, v, repoID); err != nil {
		return Branch{}, failed(span, "reading the branch", err)
	}
	b, err := s.store.Branch(ctx, repoID, name)
	if err != nil {
		return Branch{}, failed(span, "reading the branch", err)
	}
	return b, nil
}

// rank puts the default branch before the others.
func rank(b Branch) int {
	if b.Name == git.DefaultBranch {
		return 0
	}
	return 1
}

// Commit finds one of the repository's commits by its full name, with the push that first brought it; it takes
// repo.read.
func (s *Service) Commit(ctx context.Context, v Viewer, repoID uuid.UUID, sha string) (Commit, Push, error) {
	ctx, span := tracer.Start(ctx, "code.Service.Commit")
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.repository.id", repoID.String()), attribute.String("cobranch.commit.sha", sha))
	if err := s.read(ctx, v, repoID); err != nil {
		return Commit{}, Push{}, failed(span, "reading the commit", err)
	}
	if err := ValidateSHA("sha", sha); err != nil {
		return Commit{}, Push{}, err
	}
	commit, push, err := s.store.Commit(ctx, repoID, sha)
	if err != nil {
		return Commit{}, Push{}, failed(span, "reading the commit", err)
	}
	return commit, push, nil
}

// failed records err on span and returns it with what's context, unless it is nil or one of the domain's own
// answers, which it returns as it is.
func failed(span trace.Span, what string, err error) error {
	var verr *orgs.ValidationError
	switch {
	case err == nil:
		return nil
	case errors.Is(err, repos.ErrNotFound), errors.As(err, &verr), errors.Is(err, auth.ErrForbidden), errors.Is(err, ErrAnnotationTaken),
		errors.Is(err, ErrAnnotationChanged), answer(err):
		return err
	}
	span.RecordError(err)
	span.SetStatus(codes.Error, what)
	return fmt.Errorf("%s: %w", what, err)
}

// answer reports whether err is one of the Git server's answers about a ref or a path, which a viewer's request
// earns, rather than a failure.
func answer(err error) bool {
	for _, a := range []error{ErrRefNotFound, ErrPathNotFound, ErrNotTree, ErrNotBlob, ErrSubmodule} {
		if errors.Is(err, a) {
			return true
		}
	}
	return false
}

// sortEntries puts a tree's directories and submodules first and then its files and symlinks, each by name, as people
// browse them.
func sortEntries(entries []Entry) {
	folder := func(e Entry) int {
		if e.Kind == KindTree || e.Kind == KindSubmodule {
			return 0
		}
		return 1
	}
	slices.SortFunc(entries, func(a, b Entry) int { return cmp.Or(cmp.Compare(folder(a), folder(b)), cmp.Compare(a.Name, b.Name)) })
}
