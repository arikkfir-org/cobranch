package rest

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/git"
)

// ListOrganizationAttention lists the branches of the organization that need the viewer, and why.
func (a api) ListOrganizationAttention(ctx context.Context, req ListOrganizationAttentionRequestObject) (ListOrganizationAttentionResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	org, err := a.org(ctx, v, req.Org)
	if err != nil {
		return nil, err
	}
	list, err := a.code.Attention(ctx, v, org.ID)
	if err != nil {
		return nil, fail(err, "organization")
	}
	return ListOrganizationAttention200JSONResponse{Branches: mapAll(list, func(b code.Attention) AttentionBranch {
		return AttentionBranch{Repository: b.Repo, Branch: branch(b.Branch), Need: AttentionBranchNeed(b.Need)}
	})}, nil
}

// ListOrganizationActivity lists a page of what happened in the organization's repositories the viewer reads, and
// links the next.
func (a api) ListOrganizationActivity(ctx context.Context, req ListOrganizationActivityRequestObject) (ListOrganizationActivityResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	org, err := a.org(ctx, v, req.Org)
	if err != nil {
		return nil, err
	}
	var before code.ActivityCursor
	if req.Params.Cursor != nil {
		if before, err = activityCursor(*req.Params.Cursor); err != nil {
			return nil, err
		}
	}
	limit := 0
	if req.Params.Limit != nil {
		limit = *req.Params.Limit
	}
	page, err := a.code.Activity(ctx, v, org.ID, before, limit)
	if err != nil {
		return nil, fail(err, "organization")
	}
	out := ListOrganizationActivity200JSONResponse{}
	out.Body.Activity = mapAll(page.Activity, activity)
	if page.Next != (code.ActivityCursor{}) {
		next := url.Values{"cursor": {strconv.FormatInt(page.Next.At.UnixMicro(), 10) + "." + page.Next.ID.String()}}
		if req.Params.Limit != nil {
			next.Set("limit", strconv.Itoa(*req.Params.Limit))
		}
		link := `<?` + next.Encode() + `>; rel="next"`
		out.Headers.Link = &link
	}
	return out, nil
}

// activityCursor reads a cursor a page's Link named: the microseconds since the epoch of the activity the next page
// starts before, then a dot and its ID.
func activityCursor(cursor string) (code.ActivityCursor, error) {
	invalid := refuse(http.StatusBadRequest, "cursor.invalid", "Start from the cursor the previous page's Link names.")
	at, id, ok := strings.Cut(cursor, ".")
	if !ok {
		return code.ActivityCursor{}, invalid
	}
	micros, err := strconv.ParseInt(at, 10, 64)
	if err != nil || micros < 1 {
		return code.ActivityCursor{}, invalid
	}
	parsed, err := uuid.Parse(id)
	if err != nil {
		return code.ActivityCursor{}, invalid
	}
	return code.ActivityCursor{At: time.UnixMicro(micros), ID: parsed}, nil
}

// activity is x as the API shows it, with only what its kind names.
func activity(x code.Activity) Activity {
	out := Activity{Id: x.ID, At: x.At.UTC(), Kind: ActivityKind(x.Kind), Repository: x.Repo,
		Actor: Actor{Id: x.Actor.ID, DisplayName: x.Actor.DisplayName, Kind: x.Actor.Kind}}
	switch x.Kind {
	case code.ActivityPushed, code.ActivityMerged:
		refs := mapAll(x.Refs, func(u git.RefUpdate) RefUpdate { return RefUpdate{Name: u.Name, Old: u.Old, New: u.New} })
		out.Refs, out.Commits = &refs, &x.Commits
		if x.Kind == code.ActivityMerged {
			merged := mapAll(x.Merged, func(h git.Head) MergedHead { return MergedHead{Branch: h.Branch, Sha: h.SHA} })
			out.Merged = &merged
		}
	case code.ActivityReviewed:
		state := ReviewState(x.Review)
		out.Sha, out.Review = &x.SHA, &state
		if x.Branch != "" {
			out.Branch = &x.Branch
		}
	case code.ActivityDelivered:
		if x.SHA != "" {
			out.Sha = &x.SHA
		}
		d := x.Delivery
		out.Delivery = &ActivityDelivery{Number: d.Number, Kind: string(d.Kind), Environment: d.Environment,
			State: DeliveryState(d.State)}
	case code.ActivityCreated:
	}
	return out
}
