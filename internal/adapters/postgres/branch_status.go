package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"cobranch.dev/internal/adapters/postgres/queries"
	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/git"
)

// statusBranch is a branch whose status a transaction works out, locked until it ends.
type statusBranch struct {
	ID       uuid.UUID
	Head     string
	Base     string
	Facts    git.Facts
	WaitsFor *uuid.UUID
}

// checkJSON is how a branch's row keeps a check behind its status.
type checkJSON struct {
	Kind   code.CheckKind        `json:"kind"`
	Name   string                `json:"name,omitempty"`
	Status code.AnnotationStatus `json:"status,omitempty"`
	Count  int                   `json:"count,omitempty"`
	Met    bool                  `json:"met"`
}

// recordFacts stores what the push's objects say of the repository's branches once it applies, in the push's
// transaction: when it moves the default branch, the merge requirements of its /cobranch.yaml replace the
// repository's, and every branch with a base waits for the push; otherwise those it moved, and those whose facts
// changed, do.
func recordFacts(ctx context.Context, q *queries.Queries, orgID uuid.UUID, push git.Push) error {
	movesDefault := push.MovesDefault()
	if movesDefault {
		if err := replaceRequirements(ctx, q, orgID, push); err != nil {
			return err
		}
	}
	if len(push.Facts) == 0 {
		return nil
	}
	moved := map[string]bool{}
	for _, u := range push.Updates {
		if name, ok := git.Branch(u.Name); ok {
			moved[name] = true
		}
	}
	rows := make([]queries.SetBranchFactsParams, 0, len(push.Facts))
	for _, f := range push.Facts {
		rows = append(rows, queries.SetBranchFactsParams{Base: git.DefaultBranch, Ahead: int32(f.Ahead), Behind: int32(f.Behind),
			Merges: int32Of(f.Merges), PushID: push.ID, RepoID: push.RepoID, Name: f.Branch, Force: movesDefault || moved[f.Branch]})
	}
	var failed error
	q.SetBranchFacts(ctx, rows).Exec(func(i int, err error) {
		if err != nil && failed == nil {
			failed = fmt.Errorf("storing the facts of branch %s: %w", rows[i].Name, err)
		}
	})
	return failed
}

// replaceRequirements replaces the repository's merge requirements with the push's.
func replaceRequirements(ctx context.Context, q *queries.Queries, orgID uuid.UUID, push git.Push) error {
	if err := q.DeleteMergeRequirements(ctx, push.RepoID); err != nil {
		return fmt.Errorf("dropping the merge requirements: %w", err)
	}
	if len(push.Requirements) == 0 {
		return nil
	}
	rows := make([]queries.InsertMergeRequirementsParams, 0, len(push.Requirements))
	for _, r := range push.Requirements {
		rows = append(rows, queries.InsertMergeRequirementsParams{RepoID: push.RepoID, Branch: r.Branch, OrgID: orgID,
			Annotations: nonNil(r.Annotations), LinearHistory: r.LinearHistory})
	}
	var failed error
	q.InsertMergeRequirements(ctx, rows).Exec(func(i int, err error) {
		if err != nil && failed == nil {
			failed = fmt.Errorf("storing the merge requirements of branch %s: %w", rows[i].Branch, err)
		}
	})
	return failed
}

// restatusWaiting works out the status of every live branch that waits for the push, in the push worker's
// transaction.
func restatusWaiting(ctx context.Context, q *queries.Queries, repoID, pushID uuid.UUID) error {
	rows, err := q.BranchesWaitingFor(ctx, &pushID)
	if err != nil {
		return fmt.Errorf("finding the branches that wait for the push: %w", err)
	}
	branches := make([]statusBranch, 0, len(rows))
	for _, r := range rows {
		branches = append(branches, branchOf(r.ID, r.Head, r.Base, r.Ahead, r.Behind, r.Merges, r.WaitsFor))
	}
	return restatus(ctx, q, repoID, branches)
}

// restatusAt works out again the status of the repository's live branches whose head is the commit, in the
// transaction of a change to its annotations, unless they wait for the push worker, which then works it out.
func restatusAt(ctx context.Context, q *queries.Queries, repoID uuid.UUID, sha string) error {
	rows, err := q.BranchesAt(ctx, repoID, sha)
	if err != nil {
		return fmt.Errorf("finding the branches at commit %s: %w", sha, err)
	}
	branches := make([]statusBranch, 0, len(rows))
	for _, r := range rows {
		if r.WaitsFor == nil {
			branches = append(branches, branchOf(r.ID, r.Head, r.Base, r.Ahead, r.Behind, r.Merges, r.WaitsFor))
		}
	}
	return restatus(ctx, q, repoID, branches)
}

// branchOf is a branch's row as restatus takes it.
func branchOf(id uuid.UUID, head string, base *string, ahead, behind, merges *int32, waitsFor *uuid.UUID) statusBranch {
	b := statusBranch{ID: id, Head: head, WaitsFor: waitsFor}
	if base != nil {
		b.Base = *base
	}
	if ahead != nil {
		b.Facts.Ahead = int(*ahead)
	}
	if behind != nil {
		b.Facts.Behind = int(*behind)
	}
	if merges != nil {
		n := int(*merges)
		b.Facts.Merges = &n
	}
	return b
}

// restatus works out each branch's status from its facts, its base's merge requirements and the annotations on its
// head, and stores it with the checks behind it, ending its wait for the push worker.
func restatus(ctx context.Context, q *queries.Queries, repoID uuid.UUID, branches []statusBranch) error {
	if len(branches) == 0 {
		return nil
	}
	heads := make([]string, 0, len(branches))
	for _, b := range branches {
		if !slices.Contains(heads, b.Head) {
			heads = append(heads, b.Head)
		}
	}
	annotations, err := annotationsAt(ctx, q, repoID, heads)
	if err != nil {
		return err
	}
	requirements := map[string]git.Requirements{}
	for _, b := range branches {
		req, ok := requirements[b.Base]
		if !ok {
			if req, err = requirementsOf(ctx, q, repoID, b.Base); err != nil {
				return err
			}
			requirements[b.Base] = req
		}
		status, checks := code.Evaluate(code.Standing{Requirements: req, Facts: b.Facts, Annotations: annotations[b.Head]})
		encoded, err := json.Marshal(checksJSON(checks))
		if err != nil {
			return fmt.Errorf("encoding the checks: %w", err)
		}
		if err := q.SetBranchStatus(ctx, string(status), encoded, b.ID); err != nil {
			return fmt.Errorf("storing the status of branch %s: %w", b.ID, err)
		}
	}
	return nil
}

// requirementsOf is what a merge into the repository's branch needs: nothing when /cobranch.yaml names no such branch.
func requirementsOf(ctx context.Context, q *queries.Queries, repoID uuid.UUID, branch string) (git.Requirements, error) {
	row, err := q.MergeRequirementsOf(ctx, repoID, branch)
	if errors.Is(err, pgx.ErrNoRows) {
		return git.Requirements{Branch: branch}, nil
	}
	if err != nil {
		return git.Requirements{}, fmt.Errorf("reading the merge requirements of branch %s: %w", branch, err)
	}
	return git.Requirements{Branch: branch, Annotations: row.Annotations, LinearHistory: row.LinearHistory}, nil
}

// annotationsAt lists the annotations on the repository's commits that shas name, by commit.
func annotationsAt(ctx context.Context, q *queries.Queries, repoID uuid.UUID, shas []string) (map[string][]code.Annotation, error) {
	rows, err := q.AnnotationsOf(ctx, repoID, shas)
	if err != nil {
		return nil, fmt.Errorf("reading the annotations: %w", err)
	}
	out := map[string][]code.Annotation{}
	for _, r := range rows {
		out[r.Annotation.Sha] = append(out[r.Annotation.Sha], annotationOf(r.Annotation, r.PrincipalDisplayName))
	}
	return out, nil
}

func checksJSON(checks []code.Check) []checkJSON {
	out := make([]checkJSON, 0, len(checks))
	for _, c := range checks {
		out = append(out, checkJSON(c))
	}
	return out
}

// checksOf reads the checks a branch's row keeps; none while its status was never worked out.
func checksOf(encoded []byte) ([]code.Check, error) {
	if len(encoded) == 0 {
		return nil, nil
	}
	var rows []checkJSON
	if err := json.Unmarshal(encoded, &rows); err != nil {
		return nil, fmt.Errorf("reading a branch's checks: %w", err)
	}
	out := make([]code.Check, 0, len(rows))
	for _, c := range rows {
		out = append(out, code.Check(c))
	}
	return out, nil
}

// int32Of is n as a nullable column takes it.
func int32Of(n *int) *int32 {
	if n == nil {
		return nil
	}
	v := int32(*n)
	return &v
}
