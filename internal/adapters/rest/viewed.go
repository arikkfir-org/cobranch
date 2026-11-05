package rest

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"slices"
	"strings"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/code"
)

// GetViewedFiles answers the viewer's Viewed marks on the branch, or 304 while the caller holds them.
func (a api) GetViewedFiles(ctx context.Context, req GetViewedFilesRequestObject) (GetViewedFilesResponseObject, error) {
	v, repo, err := a.viewerAndRepo(ctx, req.Org, req.Repo)
	if err != nil {
		return nil, err
	}
	marks, err := a.code.Viewed(ctx, v, repo.ID, req.BranchName)
	if err != nil {
		return nil, failViewed(err)
	}
	tag := viewedTag(marks)
	if matches(req.Params.IfNoneMatch, tag) {
		return GetViewedFiles304Response{Headers: GetViewedFiles304ResponseHeaders{ETag: tag}}, nil
	}
	return GetViewedFiles200JSONResponse{Body: viewedOf(marks), Headers: GetViewedFiles200ResponseHeaders{ETag: tag}}, nil
}

// UpdateViewedFiles changes the viewer's Viewed marks on the branch as the merge patch names, while they are still
// those If-Match names.
func (a api) UpdateViewedFiles(ctx context.Context, req UpdateViewedFilesRequestObject) (UpdateViewedFilesResponseObject, error) {
	v, repo, err := a.viewerAndRepo(ctx, req.Org, req.Repo)
	if err != nil {
		return nil, err
	}
	tag, err := viewedMatch(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	marks, err := a.code.UpdateViewed(ctx, v, repo.ID, req.BranchName, tag, viewedChanges(req.Body))
	if err != nil {
		return nil, failViewed(err)
	}
	return UpdateViewedFiles200JSONResponse{Body: viewedOf(marks), Headers: UpdateViewedFiles200ResponseHeaders{ETag: viewedTag(marks)}}, nil
}

// viewedTag is the marks' ETag: their digest, quoted.
func viewedTag(marks []code.ViewedFile) string {
	return `"` + code.ViewedTag(marks) + `"`
}

// viewedOf is marks as the API shows them, by path.
func viewedOf(marks []code.ViewedFile) ViewedFiles {
	files := make(map[string]ViewedMark, len(marks))
	for _, m := range marks {
		files[m.Path] = ViewedMark{Object: optional(m.Object), ViewedAt: m.ViewedAt}
	}
	return ViewedFiles{Files: files}
}

// viewedMatch is the tag of the marks If-Match names: code.AnyViewed for *, a refusal with 428 without the header,
// and with 412 for a weak tag, since If-Match compares strongly.
func viewedMatch(header *string) (string, error) {
	if header == nil || strings.TrimSpace(*header) == "" {
		return "", refuse(http.StatusPreconditionRequired, "precondition.required", "Name the Viewed marks' ETag in If-Match, or * for whichever are there.")
	}
	tag := strings.TrimSpace(*header)
	if tag == "*" {
		return code.AnyViewed, nil
	}
	inner := strings.TrimSuffix(strings.TrimPrefix(tag, `"`), `"`)
	if !strings.HasPrefix(tag, `"`) || !strings.HasSuffix(tag, `"`) || inner == "" || inner == code.AnyViewed {
		return "", refuse(http.StatusPreconditionFailed, "precondition.failed", "If-Match names no Viewed marks.")
	}
	return inner, nil
}

// viewedChanges are the changes patch names, by path, so that a patch changes the marks in the same order every time:
// a path given null takes its mark off, and any other marks it at the object it names, none for a file the branch
// deletes.
func viewedChanges(patch *ViewedFilesPatch) []code.ViewedChange {
	if patch == nil || patch.Files == nil {
		return nil
	}
	files := *patch.Files
	changes := make([]code.ViewedChange, 0, len(files))
	for _, path := range slices.Sorted(maps.Keys(files)) {
		mark := files[path]
		switch {
		case mark == nil:
			changes = append(changes, code.ViewedChange{Path: path, Unmark: true})
		case mark.Object == nil:
			changes = append(changes, code.ViewedChange{Path: path})
		default:
			changes = append(changes, code.ViewedChange{Path: path, Object: *mark.Object})
		}
	}
	return changes
}

// failViewed maps the Viewed marks' answers, then fail's.
func failViewed(err error) error {
	switch {
	case errors.Is(err, auth.ErrForbidden):
		return refuse(http.StatusForbidden, "viewed.signed_in_only", "Sign in to mark files Viewed: the marks are a person's own.")
	case errors.Is(err, code.ErrViewedChanged):
		return refuse(http.StatusPreconditionFailed, "precondition.failed", "Your Viewed marks changed since you read them: read them again.")
	}
	return fail(err, "branch")
}
