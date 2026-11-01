package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"cobranch.dev/internal/adapters/postgres/queries"
	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/git"
)

// headJSON is how a push's row keeps a branch whose head it merged into the default branch.
type headJSON struct {
	Branch string `json:"branch"`
	SHA    string `json:"sha"`
}

// mergedJSON is what a push's row keeps of the merges it made, [] for none.
func mergedJSON(push git.Push) ([]byte, error) {
	heads := make([]headJSON, 0, len(push.Merges()))
	for _, h := range push.Merges() {
		heads = append(heads, headJSON{Branch: h.Branch, SHA: h.SHA})
	}
	encoded, err := json.Marshal(heads)
	if err != nil {
		return nil, fmt.Errorf("encoding the merges: %w", err)
	}
	return encoded, nil
}

// checkMerges checks each branch the push moves against its merge requirements, as they are before the push applies
// (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/branch-status.md, "By a push"), and
// returns what the push breaks of them, by branch: a *git.RefusedError for the first break, unless the pusher may
// bypass them.
func checkMerges(ctx context.Context, q *queries.Queries, push git.Push) (map[string][]string, error) {
	broken := map[string][]string{}
	first := ""
	for _, u := range push.Updates {
		name, ok := git.Branch(u.Name)
		if !ok {
			continue
		}
		violations, err := violations(ctx, q, push, name, u.New == git.ZeroSHA)
		if err != nil {
			return nil, err
		}
		if len(violations) > 0 {
			broken[name] = violations
			if first == "" {
				first = violations[0]
			}
		}
	}
	if first != "" && !push.Bypass {
		return nil, &git.RefusedError{Message: first}
	}
	return broken, nil
}

// violations lists what the push's move of branch name breaks of its merge requirements: none when it has none.
func violations(ctx context.Context, q *queries.Queries, push git.Push, name string, deletes bool) ([]string, error) {
	reqs, err := q.MergeRequirementsOf(ctx, push.RepoID, name)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading the merge requirements of branch %s: %w", name, err)
	}
	if deletes {
		return code.Violations(name, reqs.LinearHistory, nil, nil), nil
	}
	brought := git.Brought{Branch: name}
	if i := slices.IndexFunc(push.Brought, func(b git.Brought) bool { return b.Branch == name }); i >= 0 {
		brought = push.Brought[i]
	}
	live, err := incoming(ctx, q, push.RepoID, brought)
	if err != nil {
		return nil, err
	}
	return code.Violations(name, reqs.LinearHistory, &brought, live), nil
}

// incoming finds the branches whose heads brought brings, as they stand before the push applies, by name.
func incoming(ctx context.Context, q *queries.Queries, repoID uuid.UUID, brought git.Brought) (map[string]code.Incoming, error) {
	if len(brought.Heads) == 0 {
		return nil, nil
	}
	names := make([]string, 0, len(brought.Heads))
	for _, h := range brought.Heads {
		names = append(names, h.Branch)
	}
	rows, err := q.IncomingBranches(ctx, repoID, names)
	if err != nil {
		return nil, fmt.Errorf("finding the branches the push brings into %s: %w", brought.Branch, err)
	}
	out := make(map[string]code.Incoming, len(rows))
	for _, r := range rows {
		checks, err := checksOf(r.Checks)
		if err != nil {
			return nil, err
		}
		in := code.Incoming{Head: r.Head, Checks: checks}
		if r.Base != nil {
			in.Base = *r.Base
		}
		if r.Status != nil {
			in.Status = code.BranchStatus(*r.Status)
		}
		out[r.Name] = in
	}
	return out, nil
}

// recordBypasses appends event branch.bypassed for each branch whose merge requirements the push broke, its pusher
// holding repo.bypass, naming the push and what it broke.
func recordBypasses(ctx context.Context, q *queries.Queries, orgID uuid.UUID, push git.Push, broken map[string][]string) error {
	for _, name := range slices.Sorted(maps.Keys(broken)) {
		if err := orgEvent(ctx, q, &orgID, push.PusherID, "branch.bypassed", push.RepoID, map[string]any{
			"push": push.ID, "branch": name, "broken": broken[name],
		}); err != nil {
			return err
		}
	}
	return nil
}

// recordMerges records, in the push worker's transaction, the merges the claimed push made into the default branch:
// each branch it merged keeps the head merged, its pusher and when the push was recorded.
func recordMerges(ctx context.Context, q *queries.Queries, claimed queries.ClaimPushRow) error {
	var heads []headJSON
	if err := json.Unmarshal(claimed.Merged, &heads); err != nil {
		return fmt.Errorf("reading the push's merges: %w", err)
	}
	if len(heads) == 0 {
		return nil
	}
	rows := make([]queries.RecordMergeParams, 0, len(heads))
	for _, h := range heads {
		rows = append(rows, queries.RecordMergeParams{Head: h.SHA, MergedBy: claimed.PusherID, MergedAt: claimed.CommittedAt,
			RepoID: claimed.RepoID, Name: h.Branch})
	}
	var failed error
	q.RecordMerge(ctx, rows).Exec(func(i int, err error) {
		if err != nil && failed == nil {
			failed = fmt.Errorf("recording the merge of branch %s: %w", rows[i].Name, err)
		}
	})
	return failed
}
