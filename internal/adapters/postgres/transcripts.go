package postgres

import (
	"context"
	"fmt"
	"math"

	"github.com/google/uuid"

	"cobranch.dev/internal/adapters/postgres/queries"
	"cobranch.dev/internal/services/agents"
	"cobranch.dev/internal/services/orgs"
)

// entry is the domain's view of a transcript's row.
func entry(r queries.AgentTaskEntry) agents.Entry {
	return agents.Entry{Seq: int(r.Seq), Kind: agents.EntryKind(r.Kind), Body: r.Body, CreatedAt: r.CreatedAt}
}

// TaskByID finds a task by its ID, with its organization's slug, its agent's standing instructions and email, and
// its issue's key and number (agents.Store).
func (s *AgentStore) TaskByID(ctx context.Context, id uuid.UUID) (agents.Work, error) {
	r, err := s.q.TaskByID(ctx, id)
	if err != nil {
		return agents.Work{}, fmt.Errorf("finding task %s: %w", id, notFound(err))
	}
	t := task(taskRow{t: r.AgentTask, agent: r.AgentName, assigner: r.AssignerName, canceller: r.CancellerName, repo: r.RepoSlug, branchOf: r.BranchName,
		spent: r.Spent, input: r.InputTokens, output: r.OutputTokens, cap: r.Cap})
	w := agents.Work{Task: t, OrgSlug: r.OrgSlug, AgentInstructions: r.AgentInstructions, AgentEmail: agents.Email(t.Agent.ID)}
	if r.IssueNumber != nil {
		w.IssueKey, w.IssueNumber = r.IssueKey, int64(*r.IssueNumber)
	}
	return w, nil
}

// Entries lists at most limit of a task's transcript entries numbered above after, in order (agents.Store).
func (s *AgentStore) Entries(ctx context.Context, taskID uuid.UUID, after, limit int) ([]agents.Entry, error) {
	rows, err := s.q.Entries(ctx, taskID, int32(min(after, math.MaxInt32)), int32(min(limit, agents.MaxEntriesPage))) //nolint:gosec // both bounded just above
	if err != nil {
		return nil, fmt.Errorf("listing the transcript: %w", err)
	}
	out := make([]agents.Entry, 0, len(rows))
	for _, r := range rows {
		out = append(out, entry(r))
	}
	return out, nil
}

// Append records entry e of a task for actor, the next of its transcript, and moves the task as change says, under
// the task's row lock, so entries are numbered one at a time; an entry already numbered e.Seq answers unchanged
// (agents.Store).
func (s *AgentStore) Append(ctx context.Context, actor, taskID uuid.UUID, e agents.Entry, change *agents.StateChange) (agents.Entry, bool, error) {
	var out agents.Entry
	created := false
	err := transact(ctx, s.pool, s.q, "appending to the transcript", actor, func(ctx context.Context, q *queries.Queries) error {
		t, err := q.LockTask(ctx, taskID)
		if err != nil {
			return fmt.Errorf("locking the task: %w", notFound(err))
		}
		last, err := q.LastEntry(ctx, taskID)
		if err != nil {
			return fmt.Errorf("reading the transcript's last entry: %w", err)
		}
		switch {
		case e.Seq <= int(last):
			row, err := q.TranscriptEntry(ctx, taskID, int32(e.Seq)) //nolint:gosec // at most last, an int32
			if err != nil {
				return fmt.Errorf("reading entry %d: %w", e.Seq, err)
			}
			out = entry(row)
			return nil
		case !agents.TaskState(t.State).Active():
			return agents.ErrEnded
		case e.Seq > int(last)+1:
			return &orgs.ValidationError{Field: "seq", Code: "transcript.seq_invalid", Message: fmt.Sprintf("the next entry is %d", last+1)}
		}
		row, err := q.InsertEntry(ctx, queries.InsertEntryParams{TaskID: taskID, Seq: last + 1, Kind: string(e.Kind), Body: e.Body})
		if err != nil {
			return fmt.Errorf("inserting entry %d: %w", e.Seq, err)
		}
		out, created = entry(row), true
		if change == nil {
			return nil
		}
		return moveTask(ctx, q, actor, taskID, t, *change)
	})
	return out, created, err
}

// moveTask moves the locked task t to the change's state, or answers agents.ErrTransition, and records its event; a
// review task's move changes the status of the branches at its head.
func moveTask(ctx context.Context, q *queries.Queries, actor, taskID uuid.UUID, t queries.LockTaskRow, change agents.StateChange) error {
	from := make([]string, 0, 2)
	for _, state := range change.From() {
		from = append(from, string(state))
	}
	n, err := q.MoveTask(ctx, queries.MoveTaskParams{State: string(change.State), Summary: change.Summary, Reason: change.Reason, ID: taskID,
		FromStates: from})
	switch {
	case err != nil:
		return fmt.Errorf("moving the task to %s: %w", change.State, err)
	case n == 0:
		return agents.ErrTransition
	}
	if err := orgEvent(ctx, q, &t.OrgID, actor, "task."+string(change.State), taskID, map[string]any{"number": t.Number}); err != nil {
		return err
	}
	return restatusReviewed(ctx, q, t.Kind, t.RepoID, t.Sha)
}
