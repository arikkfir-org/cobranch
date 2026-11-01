package graphql

import (
	"context"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/code"
)

// Code is what the API needs of the code's service.
type Code interface {
	Branches(ctx context.Context, v code.Viewer, repoID uuid.UUID) ([]code.Branch, error)
	History(ctx context.Context, v code.Viewer, repoID uuid.UUID, branch, cursor string, limit int) (code.Page, error)
	Commit(ctx context.Context, v code.Viewer, repoID uuid.UUID, sha string) (code.Commit, code.Push, error)
	SetAnnotation(ctx context.Context, v code.Viewer, repoID uuid.UUID, sha string, in code.AnnotationInput) (code.Annotation, error)
	DeleteAnnotation(ctx context.Context, v code.Viewer, repoID uuid.UUID, sha, name string) error
}

func commitPrincipal(p *code.Principal) *CommitPrincipal {
	if p == nil {
		return nil
	}
	return &CommitPrincipal{ID: p.ID.String(), DisplayName: p.DisplayName}
}

func person(p code.Person) *Person {
	return &Person{Name: p.Name, Email: p.Email, At: p.At.UTC(), Zone: p.Zone, Principal: commitPrincipal(p.Principal)}
}

// commit is c as the API shows it; a list's commits come without their pushes.
func commit(c code.Commit) *Commit {
	parents := c.Parents
	if parents == nil {
		parents = []string{}
	}
	return &Commit{Sha: c.SHA, Tree: c.Tree, Parents: parents, Message: c.Message, Author: person(c.Author), Committer: person(c.Committer),
		Signature: c.Signature, Annotations: mapAll(c.Annotations, annotation)}
}

// annotationStatuses maps the domain's annotation statuses to the schema's, and back.
var annotationStatuses = map[code.AnnotationStatus]AnnotationStatus{
	code.AnnotationPending: AnnotationStatusPending, code.AnnotationInProgress: AnnotationStatusInProgress,
	code.AnnotationFailed: AnnotationStatusFailed, code.AnnotationSuccessful: AnnotationStatusSuccessful,
}

func annotation(a code.Annotation) *Annotation {
	out := &Annotation{ID: a.ID.String(), Name: a.Name, Status: annotationStatuses[a.Status], Summary: a.Summary, Link: a.Link,
		Principal: commitPrincipal(a.Principal), CreatedAt: a.CreatedAt.UTC(), UpdatedAt: a.UpdatedAt.UTC(), StartedAt: a.StartedAt.UTC()}
	if a.CompletedAt != nil {
		at := a.CompletedAt.UTC()
		out.CompletedAt = &at
	}
	return out
}

// annotationInput is in as the domain takes it, for the annotation of name.
func annotationInput(name string, in AnnotationInput) code.AnnotationInput {
	out := code.AnnotationInput{Name: name, Summary: in.Summary, Link: in.Link}
	for domain, schema := range annotationStatuses {
		if schema == in.Status {
			out.Status = domain
		}
	}
	return out
}

// codeRepo is the viewer and the repository a mutation of its code names.
func codeRepo(ctx context.Context, repositoryID string) (code.Viewer, uuid.UUID, error) {
	v, err := viewer(ctx)
	if err != nil {
		return code.Viewer{}, uuid.Nil, err
	}
	repoID, err := parseID(ctx, repositoryID, "repository")
	if err != nil {
		return code.Viewer{}, uuid.Nil, err
	}
	return v.User, repoID, nil
}

func branch(b code.Branch) *Branch {
	out := &Branch{Name: b.Name, Head: b.Head, CreatedAt: b.CreatedAt, UpdatedAt: b.UpdatedAt}
	if b.Commit != nil {
		out.Commit = commit(*b.Commit)
	}
	return out
}
