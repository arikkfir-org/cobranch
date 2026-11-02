package agents

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/orgs"
	"cobranch.dev/internal/services/repos"
)

// visible answers orgs.ErrNotFound for a task that names a repository v doesn't read, as for a task that isn't there.
func (s *Service) visible(ctx context.Context, v Viewer, t Task) error {
	if t.RepoID == uuid.Nil {
		return nil
	}
	ok, err := s.reads(ctx, v, t.RepoID)
	switch {
	case err != nil:
		return err
	case !ok:
		return orgs.ErrNotFound
	}
	return nil
}

// Tasks lists a page of at most limit, 1 to MaxPage, of the organization's tasks that match the filter, newest first,
// leaving out those on repositories v doesn't read; it takes org.read.
func (s *Service) Tasks(ctx context.Context, v Viewer, orgID uuid.UUID, f TaskFilter, limit int) (TaskPage, error) {
	ctx, span := orgSpan(ctx, "Tasks", orgID)
	defer span.End()
	if limit < 1 || limit > MaxPage {
		return TaskPage{}, failed(span, "listing the tasks", invalid("limit", "limit.invalid", fmt.Sprintf("a page holds 1 to %d tasks", MaxPage)))
	}
	for _, state := range f.States {
		if !slices.Contains(TaskStates, state) {
			return TaskPage{}, failed(span, "listing the tasks", invalid("state", "task.state_invalid", "not a task state"))
		}
	}
	if _, err := s.held(ctx, v, orgID); err != nil {
		return TaskPage{}, failed(span, "listing the tasks", err)
	}
	readable, err := s.authz.Repositories(ctx, v.ID, orgID, repos.PermRead)
	if err != nil {
		return TaskPage{}, failed(span, "listing the repositories the viewer reads", err)
	}
	tasks, err := s.store.Tasks(ctx, orgID, f, readable, limit+1)
	if err != nil {
		return TaskPage{}, failed(span, "listing the tasks", err)
	}
	page := TaskPage{Tasks: tasks}
	if len(tasks) > limit {
		page.Tasks = tasks[:limit]
		page.Next = page.Tasks[limit-1].Number
	}
	return page, nil
}

// Task finds one of the organization's tasks by its number; it takes org.read, and repo.read on the repository it
// names.
func (s *Service) Task(ctx context.Context, v Viewer, orgID uuid.UUID, number int64) (Task, error) {
	ctx, span := orgSpan(ctx, "Task", orgID, attribute.Int64("cobranch.task.number", number))
	defer span.End()
	if _, err := s.held(ctx, v, orgID); err != nil {
		return Task{}, failed(span, "finding the task", err)
	}
	t, err := s.store.Task(ctx, orgID, number)
	if err != nil {
		return Task{}, failed(span, "finding the task", err)
	}
	if err := s.visible(ctx, v, t); err != nil {
		return Task{}, failed(span, "finding the task", err)
	}
	return t, nil
}

// Prepare checks that v may assign the task to the agent and answers the task to record, Queued, without its number:
// v holds agents.assign at the agent's organization and repo.read on the repository the task names, the branch is
// one of that repository's live branches and the thread is on that branch. Assign records what it answers; a slice
// that assigns tasks in a transaction of its own records it there. An agent assigns no tasks.
func (s *Service) Prepare(ctx context.Context, v Viewer, orgID, agentID uuid.UUID, in TaskInput) (Task, error) {
	if err := person(v, "assigns no tasks"); err != nil {
		return Task{}, err
	}
	if _, err := s.require(ctx, v, orgID, PermAssign); err != nil {
		return Task{}, err
	}
	in, err := ValidateTask(in)
	if err != nil {
		return Task{}, err
	}
	agent, err := s.store.Agent(ctx, orgID, agentID)
	if errors.Is(err, orgs.ErrNotFound) {
		return Task{}, invalid("agentId", "agent.invalid", "not an agent of this organization")
	}
	if err != nil {
		return Task{}, fmt.Errorf("finding the agent: %w", err)
	}
	t := Task{OrgID: orgID, Agent: Principal{ID: agent.ID, DisplayName: agent.Name}, Kind: KindAssigned,
		AssignedBy: &Principal{ID: v.ID, DisplayName: v.DisplayName}, Title: in.Title, Instructions: in.Instructions, State: TaskQueued, Version: 1}
	if err := s.where(ctx, v, orgID, in, &t); err != nil {
		return Task{}, err
	}
	if t.ID, err = uuid.NewV7(); err != nil {
		return Task{}, fmt.Errorf("making the task's ID: %w", err)
	}
	return t, nil
}

// where fills in where the task works: the repository v reads, its live branch and the branch's thread that in names.
func (s *Service) where(ctx context.Context, v Viewer, orgID uuid.UUID, in TaskInput, t *Task) error {
	if in.RepoID == uuid.Nil {
		return nil
	}
	slug, err := s.store.Repository(ctx, orgID, in.RepoID)
	if err != nil && !errors.Is(err, orgs.ErrNotFound) {
		return fmt.Errorf("finding the repository: %w", err)
	}
	ok := err == nil
	if ok {
		if ok, err = s.reads(ctx, v, in.RepoID); err != nil {
			return err
		}
	}
	if !ok {
		return invalid("repositoryId", "repository.invalid", "not a repository of this organization")
	}
	t.RepoID, t.RepoSlug = in.RepoID, slug
	if in.BranchID == uuid.Nil && in.Branch == "" {
		return nil
	}
	t.BranchID, t.Branch, err = s.store.Branch(ctx, in.RepoID, in.BranchID, in.Branch)
	if errors.Is(err, orgs.ErrNotFound) {
		return invalid("branch", "branch.invalid", "not a live branch of this repository")
	}
	if err != nil {
		return fmt.Errorf("finding the branch: %w", err)
	}
	if in.ThreadID == uuid.Nil {
		return nil
	}
	branchID, err := s.store.ThreadBranch(ctx, in.ThreadID)
	if errors.Is(err, orgs.ErrNotFound) || (err == nil && branchID != t.BranchID) {
		return invalid("threadId", "thread.invalid", "not a thread of this branch's chat")
	}
	if err != nil {
		return fmt.Errorf("finding the thread: %w", err)
	}
	t.ThreadID = in.ThreadID
	return nil
}

// Assign assigns a task to one of the organization's agents, Queued until the controller runs it, capped by v's
// permissions as they are whenever it acts; it takes agents.assign, and repo.read on the repository it names. An agent
// assigns no tasks.
func (s *Service) Assign(ctx context.Context, v Viewer, orgID, agentID uuid.UUID, in TaskInput) (Task, error) {
	ctx, span := orgSpan(ctx, "Assign", orgID, attribute.String("cobranch.agent.id", agentID.String()))
	defer span.End()
	t, err := s.Prepare(ctx, v, orgID, agentID, in)
	if err != nil {
		return Task{}, failed(span, "assigning the task", err)
	}
	t, err = s.store.Assign(ctx, v.ID, t)
	if err != nil {
		return Task{}, failed(span, "recording the task", err)
	}
	span.SetAttributes(attribute.Int64("cobranch.task.number", t.Number))
	return t, nil
}

// CancelTask cancels one of the organization's tasks that has yet to end, from version, or from any version for
// AnyVersion; it takes being its assigner, or org.principals.manage. A task that has ended is ErrEnded.
func (s *Service) CancelTask(ctx context.Context, v Viewer, orgID uuid.UUID, number, version int64) (Task, error) {
	ctx, span := orgSpan(ctx, "CancelTask", orgID, attribute.Int64("cobranch.task.number", number))
	defer span.End()
	held, err := s.held(ctx, v, orgID)
	if err != nil {
		return Task{}, failed(span, "cancelling the task", err)
	}
	t, err := s.store.Task(ctx, orgID, number)
	if err != nil {
		return Task{}, failed(span, "finding the task", err)
	}
	if err := s.visible(ctx, v, t); err != nil {
		return Task{}, failed(span, "finding the task", err)
	}
	mine := t.AssignedBy != nil && t.AssignedBy.ID == v.ID
	if !mine && !slices.Contains(held, orgs.PermPrincipalsManage) {
		return Task{}, failed(span, "cancelling the task", fmt.Errorf("%w: cancelling another's task takes %s", auth.ErrForbidden, orgs.PermPrincipalsManage))
	}
	if !t.State.Active() {
		return Task{}, failed(span, "cancelling the task", ErrEnded)
	}
	t, err = s.store.Cancel(ctx, v.ID, orgID, number, version)
	return t, failed(span, "cancelling the task", err)
}
