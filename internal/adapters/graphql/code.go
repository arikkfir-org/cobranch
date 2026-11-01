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
		Signature: c.Signature}
}

func branch(b code.Branch) *Branch {
	out := &Branch{Name: b.Name, Head: b.Head, CreatedAt: b.CreatedAt, UpdatedAt: b.UpdatedAt}
	if b.Commit != nil {
		out.Commit = commit(*b.Commit)
	}
	return out
}
