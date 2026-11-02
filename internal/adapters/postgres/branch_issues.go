package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"cobranch.dev/internal/adapters/postgres/queries"
	"cobranch.dev/internal/services/issues"
)

// The branches linked to issues
// (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/issues.md, "Branches"): a branch's link is a
// row of branch_issues, made by its name, by the push worker and by the creation of the issue it names, or by hand.
// Each link moves its issue from Backlog or Todo to In progress, and the merge of a linked branch moves its issue to
// Done, both for whoever linked or pushed.

// ByID finds an issue by its ID, or issues.ErrNotFound (issues.Store).
func (s *IssueStore) ByID(ctx context.Context, issueID uuid.UUID) (issues.Issue, error) {
	row, err := s.q.IssueByID(ctx, issueID)
	if errors.Is(err, pgx.ErrNoRows) {
		return issues.Issue{}, issues.ErrNotFound
	}
	if err != nil {
		return issues.Issue{}, fmt.Errorf("finding issue %s: %w", issueID, err)
	}
	return issueOf(queries.IssueByNumberRow(row)), nil
}

// Branches lists the branches linked to the issue, the live ones first, by repository and name (issues.Store).
func (s *IssueStore) Branches(ctx context.Context, issueID uuid.UUID) ([]issues.LinkedBranch, error) {
	rows, err := s.q.IssueBranches(ctx, &issueID)
	if err != nil {
		return nil, fmt.Errorf("listing the issue's branches: %w", err)
	}
	out := make([]issues.LinkedBranch, 0, len(rows))
	for _, r := range rows {
		b := issues.LinkedBranch{Repository: issues.Repository{ID: r.RepoID, Slug: r.RepoSlug, Name: r.RepoName}, Name: r.Name, Head: r.Head,
			Deleted: r.DeletedAt != nil, LinkedBy: issues.LinkedBy(r.LinkedBy), LinkedAt: r.LinkedAt}
		if r.Status != nil {
			b.Status = *r.Status
		}
		out = append(out, b)
	}
	return out, nil
}

// Branch finds a live branch of the organization's repository by name, with its link, or issues.ErrBranchNotFound
// (issues.Store).
func (s *IssueStore) Branch(ctx context.Context, orgID, repoID uuid.UUID, name string) (issues.Branch, error) {
	row, err := s.q.LinkedLiveBranch(ctx, orgID, repoID, name)
	if errors.Is(err, pgx.ErrNoRows) {
		return issues.Branch{}, issues.ErrBranchNotFound
	}
	if err != nil {
		return issues.Branch{}, fmt.Errorf("finding branch %s: %w", name, err)
	}
	b := issues.Branch{ID: row.ID, OrgID: row.OrgID, Repository: issues.Repository{ID: row.RepoID, Slug: row.RepoSlug, Name: row.RepoName},
		Name: row.Name}
	if row.IssueID != nil {
		b.IssueID = *row.IssueID
	}
	if row.LinkedBy != nil {
		b.LinkedBy = issues.LinkedBy(*row.LinkedBy)
	}
	return b, nil
}

// Link links a live branch to an issue by hand, or unlinks it by hand for nil, in place of the issue it works on, in
// one transaction that acts for actor (issues.Store).
func (s *IssueStore) Link(ctx context.Context, actor uuid.UUID, branch issues.Branch, issueID *uuid.UUID) error {
	return transact(ctx, s.pool, s.q, "linking the branch", actor, func(ctx context.Context, q *queries.Queries) error {
		current, err := q.LockBranchLink(ctx, branch.ID)
		if errors.Is(err, pgx.ErrNoRows) {
			return issues.ErrBranchNotFound
		}
		if err != nil {
			return fmt.Errorf("locking the branch: %w", err)
		}
		if err := q.SetBranchIssue(ctx, queries.SetBranchIssueParams{BranchID: branch.ID, OrgID: branch.OrgID, IssueID: issueID,
			LinkedBy: string(issues.LinkedByHand)}); err != nil {
			return fmt.Errorf("writing the branch's link: %w", err)
		}
		ref := branchRef{orgID: branch.OrgID, repoID: branch.Repository.ID, repo: branch.Repository.Slug, name: branch.Name}
		if current != nil && (issueID == nil || *current != *issueID) {
			if err := linkEvent(ctx, q, actor, "issue.branch_unlinked", *current, ref, issues.LinkedByHand); err != nil {
				return err
			}
		}
		if issueID == nil || (current != nil && *current == *issueID) {
			return nil
		}
		if err := linkEvent(ctx, q, actor, "issue.branch_linked", *issueID, ref, issues.LinkedByHand); err != nil {
			return err
		}
		return moveIssue(ctx, q, branch.OrgID, actor, *issueID, issues.StateInProgress, issues.StartsFrom)
	})
}

// branchRef is the branch a link names, as its events record it.
type branchRef struct {
	orgID, repoID uuid.UUID
	repo, name    string
}

// linkEvent appends event kind, issue.branch_linked or issue.branch_unlinked, about the issue, naming the branch and
// how the link was made or undone, as actor.
func linkEvent(ctx context.Context, q *queries.Queries, actor uuid.UUID, kind string, issueID uuid.UUID, b branchRef, by issues.LinkedBy) error {
	return issueEvent(ctx, q, b.orgID, actor, kind, issueID, map[string]any{"branch": b.name, "repoId": b.repoID, "repo": b.repo, "by": by})
}

// linkByName links a branch that has no link to the issue its name names, as actor, and starts the issue: false when
// the branch got a link meanwhile.
func linkByName(ctx context.Context, q *queries.Queries, actor uuid.UUID, b branchRef, branchID, issueID uuid.UUID) (bool, error) {
	linked, err := q.LinkBranchByName(ctx, branchID, b.orgID, &issueID)
	if err != nil {
		return false, fmt.Errorf("linking branch %s: %w", b.name, err)
	}
	if linked == 0 {
		return false, nil
	}
	if err := linkEvent(ctx, q, actor, "issue.branch_linked", issueID, b, issues.LinkedByName); err != nil {
		return false, err
	}
	return true, moveIssue(ctx, q, b.orgID, actor, issueID, issues.StateInProgress, issues.StartsFrom)
}

// moveIssue moves the issue to state to, as actor, when it is in one of the states from, and appends event
// issue.changed with the change; it leaves an issue in any other state as it is.
func moveIssue(ctx context.Context, q *queries.Queries, orgID, actor, issueID uuid.UUID, to issues.State, from []issues.State) error {
	states := make([]string, 0, len(from))
	for _, s := range from {
		states = append(states, string(s))
	}
	old, err := q.MoveIssue(ctx, string(to), issueID, states)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("moving the issue to %s: %w", to, err)
	}
	return issueEvent(ctx, q, orgID, actor, "issue.changed", issueID,
		map[string]any{"changes": []change{{Field: "state", From: old, To: string(to)}}})
}

// linkNaming links the organization's live branches that have no link and whose names name the issue, which was just
// created, for actor: true when it linked any, which started the issue.
func linkNaming(ctx context.Context, q *queries.Queries, actor uuid.UUID, issue issues.Issue) (bool, error) {
	rows, err := q.UnlinkedBranchesNaming(ctx, issue.OrgID, issue.Key+"-"+strconv.Itoa(issue.Number))
	if err != nil {
		return false, fmt.Errorf("finding the branches that name the issue: %w", err)
	}
	started := false
	for _, r := range rows {
		if n, ok := issues.Named(r.Name, issue.Key); !ok || n != issue.Number {
			continue
		}
		linked, err := linkByName(ctx, q, actor, branchRef{orgID: r.OrgID, repoID: r.RepoID, repo: r.RepoSlug, name: r.Name}, r.ID, issue.ID)
		if err != nil {
			return false, err
		}
		started = started || linked
	}
	return started, nil
}

// linkPushed links, in the push worker's transaction, the live branches the claimed push created, moved or merged
// that have no link yet to the issues their names name, for the pusher, and starts those issues. A push whose pusher
// is gone links nothing, since an event names its actor: the branch's next push links it.
func linkPushed(ctx context.Context, q *queries.Queries, claimed queries.ClaimPushRow) error {
	if claimed.PusherID == nil {
		return nil
	}
	rows, err := q.UnlinkedPushBranches(ctx, claimed.RepoID, claimed.ID)
	if err != nil {
		return fmt.Errorf("finding the push's unlinked branches: %w", err)
	}
	for _, r := range rows {
		number, ok := issues.Named(r.Name, r.IssueKey)
		if !ok {
			continue
		}
		issue, err := q.IssueByNumber(ctx, r.OrgID, narrow(number))
		if errors.Is(err, pgx.ErrNoRows) {
			continue // its issue's creation links it
		}
		if err != nil {
			return fmt.Errorf("finding the issue branch %s names: %w", r.Name, err)
		}
		b := branchRef{orgID: r.OrgID, repoID: r.RepoID, repo: r.RepoSlug, name: r.Name}
		if _, err := linkByName(ctx, q, *claimed.PusherID, b, r.ID, issue.Issue.ID); err != nil {
			return err
		}
	}
	return nil
}

// finishMerged moves, in the push worker's transaction, the open issues linked to the branches the claimed push
// merged to Done, for the pusher, once each, after recordMerges recorded the merges: a branch by the ID the push kept,
// so a merge finishes its issue though the branch is deleted before the worker gets to it. A push whose pusher is gone
// moves none, since an event names its actor.
func finishMerged(ctx context.Context, q *queries.Queries, claimed queries.ClaimPushRow) error {
	var heads []headJSON
	if err := json.Unmarshal(claimed.Merged, &heads); err != nil {
		return fmt.Errorf("reading the push's merges: %w", err)
	}
	if len(heads) == 0 || claimed.PusherID == nil {
		return nil
	}
	var ids []uuid.UUID
	var names []string
	for _, h := range heads {
		if h.ID != nil {
			ids = append(ids, *h.ID)
		} else {
			names = append(names, h.Branch)
		}
	}
	rows, err := q.MergedIssues(ctx, claimed.RepoID, ids, names)
	if err != nil {
		return fmt.Errorf("finding the issues the push's merges finish: %w", err)
	}
	done := map[uuid.UUID]bool{}
	for _, r := range rows {
		if done[r.IssueID] {
			continue
		}
		done[r.IssueID] = true
		if err := moveIssue(ctx, q, r.OrgID, *claimed.PusherID, r.IssueID, issues.StateDone, issues.FinishesFrom); err != nil {
			return err
		}
	}
	return nil
}
