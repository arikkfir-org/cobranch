package graphql

import (
	"context"
	"errors"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/code"
)

// viewedFiles is a person's Viewed marks as the schema shows them, with their tag.
func viewedFiles(marks []code.ViewedFile) *ViewedFiles {
	files := make([]*ViewedFile, 0, len(marks))
	for _, m := range marks {
		files = append(files, &ViewedFile{Path: m.Path, Object: optional(m.Object), ViewedAt: m.ViewedAt})
	}
	return &ViewedFiles{Tag: code.ViewedTag(marks), Files: files}
}

// viewedChanges are the changes files name, in their order: a file viewed false takes its mark off, and any other
// marks it at the object it names, none for a file the branch deletes.
func viewedChanges(files []*ViewedFileInput) []code.ViewedChange {
	changes := make([]code.ViewedChange, 0, len(files))
	for _, f := range files {
		c := code.ViewedChange{Path: f.Path, Unmark: !f.Viewed}
		if f.Object != nil && f.Viewed {
			c.Object = *f.Object
		}
		changes = append(changes, c)
	}
	return changes
}

// viewedFail maps the Viewed marks' answers, then fail's, for which what isn't there is the branch.
func viewedFail(ctx context.Context, err error, what string) error {
	switch {
	case errors.Is(err, auth.ErrForbidden):
		return refuse(ctx, forbidden, "viewed.signed_in_only", "sign in to mark files Viewed: the marks are a person's own")
	case errors.Is(err, code.ErrViewedChanged):
		return refuse(ctx, conflict, "precondition.failed", "your Viewed marks changed since you read them: read them again")
	}
	return fail(ctx, err, "branch", what)
}
