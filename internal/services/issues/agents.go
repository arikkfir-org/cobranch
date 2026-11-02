package issues

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/agents"
	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/orgs"
	"cobranch.dev/internal/services/repos"
)

// KindTask is the kind of the task an issue's assignment to an agent gives it.
const KindTask = "issue"

// Agents is the agents service, as the issues hand one of theirs to an agent: Prepare checks that the viewer may
// assign the agent the task and answers it, to record, and Guard shows tasks of a kind only to viewers who hold a
// permission.
type Agents interface {
	Prepare(ctx context.Context, v Viewer, orgID, agentID uuid.UUID, in agents.TaskInput) (agents.Task, error)
	Guard(kind, permission string)
}

// Tasks is the agents' store, as the issues record the tasks they give agents: called with the context of the
// issues store's Transact, it records the task, or cancels it, in that transaction.
type Tasks interface {
	Assign(ctx context.Context, actor uuid.UUID, t agents.Task) (agents.Task, error)
	Cancel(ctx context.Context, actor, orgID uuid.UUID, number, version int64) (agents.Task, error)
}

// WithAgents has the service hand issues to agents through a and t, and answers it. Without them, an issue assigned
// to an agent gives it no task. A task of an issue carries its title and description, so a shows it only to viewers
// who hold issues.read, as the issue itself.
func (s *Service) WithAgents(a Agents, t Tasks) *Service {
	a.Guard(KindTask, PermRead)
	s.agents, s.tasks = a, t
	return s
}

// save records next, an issue that was cur (the zero Issue when write creates it), through write, and answers it as
// kept. When next is assigned to an agent cur wasn't, the agent gets a task of it, which takes agents.assign; when its
// assignee changes from the agent of an active task, or write deletes it, that task is cancelled. The task changes
// commit with the issue, or neither does.
func (s *Service) save(ctx context.Context, v Viewer, cur, next Issue, write func(ctx context.Context) (Issue, error)) (Issue, error) {
	moved := idOf(cur.Assignee) != idOf(next.Assignee)
	handover := moved && next.Assignee != nil && next.Assignee.Agent && s.agents != nil
	withdraw := moved && cur.Task != nil && cur.Task.State.Active() && s.tasks != nil
	if !handover && !withdraw {
		return write(ctx)
	}
	var task *agents.Task
	if handover {
		t, err := s.handOver(ctx, v, next)
		if err != nil {
			return Issue{}, err
		}
		task = &t
	}
	var withdrawn *Task
	if withdraw {
		withdrawn = cur.Task
	}
	var out Issue
	err := s.store.Transact(ctx, v.ID, func(ctx context.Context) error {
		var err error
		out, err = s.record(ctx, v, cur.OrgID, write, withdrawn, task)
		return err
	})
	if err != nil {
		return Issue{}, fmt.Errorf("saving the issue with its agent's task: %w", err)
	}
	return out, nil
}

// handOver checks that v may hand next to the agent it is assigned to, and answers the agent's task, to record.
func (s *Service) handOver(ctx context.Context, v Viewer, next Issue) (agents.Task, error) {
	if err := s.readsFor(ctx, v, next); err != nil {
		return agents.Task{}, err
	}
	task, err := s.agents.Prepare(ctx, v, next.OrgID, next.Assignee.ID, taskInput(next))
	if err != nil {
		return agents.Task{}, fmt.Errorf("assigning the agent: %w", err)
	}
	return task, nil
}

// record writes the issue through write, then cancels withdrawn, the task of the agent it was assigned to, unless it
// has ended, and records task, the task of the agent it is assigned to now, each when there is one, all with ctx's
// transaction.
func (s *Service) record(ctx context.Context, v Viewer, orgID uuid.UUID, write func(ctx context.Context) (Issue, error), withdrawn *Task, task *agents.Task) (Issue, error) {
	out, err := write(ctx)
	if err != nil {
		return Issue{}, err
	}
	if withdrawn != nil {
		_, err := s.tasks.Cancel(ctx, v.ID, orgID, withdrawn.Number, agents.AnyVersion)
		if err != nil && !errors.Is(err, agents.ErrEnded) {
			return Issue{}, fmt.Errorf("cancelling task %d: %w", withdrawn.Number, err)
		}
		out.Task = nil
	}
	if task != nil {
		task.Kind, task.IssueID, task.Title = KindTask, out.ID, taskTitle(out)
		recorded, err := s.tasks.Assign(ctx, v.ID, *task)
		if err != nil {
			return Issue{}, fmt.Errorf("giving the agent its task: %w", err)
		}
		out.Task = &Task{Number: recorded.Number, State: recorded.State, RepoID: recorded.RepoID}
	}
	return out, nil
}

// readsFor refuses to hand issue to an agent unless v reads the repository it is about, where the agent's task works
// within v's permissions.
func (s *Service) readsFor(ctx context.Context, v Viewer, issue Issue) error {
	if issue.Repository == nil {
		return nil
	}
	held, err := s.authz.Holds(ctx, v.ID, auth.ScopeRepository, issue.Repository.ID)
	if err != nil {
		return fmt.Errorf("reading the viewer's permissions on the repository: %w", err)
	}
	if !slices.Contains(held, repos.PermRead) {
		return &orgs.ValidationError{Field: "assigneeId", Code: "assignee.invalid",
			Message: "an agent works in the issue's repository, within your permissions, and you don't read it"}
	}
	return nil
}

// taskInput is the task an issue gives the agent assigned to it: its title, its description as instructions, cut to
// what a task holds, and its repository.
func taskInput(issue Issue) agents.TaskInput {
	in := agents.TaskInput{Title: issue.Title, Instructions: issue.Description}
	if utf8.RuneCountInString(in.Instructions) > agents.MaxInstructions {
		const more = "\n\n…the description goes on: read the issue for the rest."
		in.Instructions = cut(in.Instructions, agents.MaxInstructions-utf8.RuneCountInString(more)) + more
	}
	if issue.Repository != nil {
		in.RepoID = issue.Repository.ID
	}
	return in
}

// taskTitle is the title of the task an issue gives its agent: its identifier and title, cut to what a task's title
// holds.
func taskTitle(issue Issue) string {
	return cut(issue.Identifier()+" "+issue.Title, agents.MaxTitle)
}

// cut is s cut to its first n characters, its trailing spaces trimmed.
func cut(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return strings.TrimRight(string([]rune(s)[:n]), " \t\n")
}
