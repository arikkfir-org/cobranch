package graphql

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/git"
)

// attentionNeeds are the needs as the API names them.
var attentionNeeds = map[code.Need]AttentionNeed{code.NeedFix: AttentionNeedFix, code.NeedReview: AttentionNeedReview,
	code.NeedMerge: AttentionNeedMerge, code.NeedWatch: AttentionNeedWatch}

// attentionBranches is list as the API shows it, each branch of its repository of obj, which its issue and
// environments resolve from.
func attentionBranches(ctx context.Context, obj *Organization, list []code.Attention) ([]*AttentionBranch, error) {
	orgID, err := parseID(ctx, obj.ID, "organization")
	if err != nil {
		return nil, err
	}
	return mapAll(list, func(a code.Attention) *AttentionBranch {
		b := branch(a.Branch)
		b.orgID, b.repoID = orgID, a.RepoID
		return &AttentionBranch{Repository: a.Repo, Branch: b, Need: attentionNeeds[a.Need]}
	}), nil
}

// activityCursor reads where a page of activity starts from the previous page's nextCursor: the microseconds since
// the epoch of the activity it starts before, then a dot and its ID; after the newest without one.
func activityCursor(ctx context.Context, after *string) (code.ActivityCursor, error) {
	if after == nil {
		return code.ActivityCursor{}, nil
	}
	invalid := refuse(ctx, badUserInput, "cursor.invalid", "start from the cursor the previous page's nextCursor names")
	at, id, ok := strings.Cut(*after, ".")
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

// activityPage is page as the API shows it.
func activityPage(page code.ActivityPage) *ActivityPage {
	out := &ActivityPage{Activity: mapAll(page.Activity, activity)}
	if page.Next != (code.ActivityCursor{}) {
		next := strconv.FormatInt(page.Next.At.UnixMicro(), 10) + "." + page.Next.ID.String()
		out.NextCursor = &next
	}
	return out
}

// activity is x as the API shows it, with only what its kind names.
func activity(x code.Activity) *Activity {
	out := &Activity{ID: x.ID.String(), At: x.At.UTC(), Kind: ActivityKind(strings.ToUpper(string(x.Kind))), Repository: x.Repo,
		Actor: &Actor{ID: x.Actor.ID.String(), DisplayName: x.Actor.DisplayName, Kind: x.Actor.Kind}}
	switch x.Kind {
	case code.ActivityPushed, code.ActivityMerged:
		out.Refs = mapAll(x.Refs, func(u git.RefUpdate) *RefUpdate { return &RefUpdate{Name: u.Name, Old: u.Old, New: u.New} })
		out.Commits = &x.Commits
		if x.Kind == code.ActivityMerged {
			out.Merged = mapAll(x.Merged, func(h git.Head) *MergedHead { return &MergedHead{Branch: h.Branch, Sha: h.SHA} })
		}
	case code.ActivityReviewed:
		state := reviewStates[x.Review]
		out.Sha, out.Review = &x.SHA, &state
		if x.Branch != "" {
			out.Branch = &x.Branch
		}
	case code.ActivityDelivered:
		out.Sha = nonEmpty(x.SHA)
		d := x.Delivery
		out.Delivery = &ActivityDelivery{Number: int(d.Number), Kind: DeliveryKind(strings.ToUpper(string(d.Kind))), Environment: d.Environment,
			State: DeliveryState(strings.ToUpper(string(d.State)))}
	case code.ActivityCreated:
	}
	return out
}
