package chats

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

// The kinds of the tasks a message asks an agent for: in a branch's chat or a private conversation, and in an issue's
// chat, whose tasks carry its comments and so show only to viewers who read the organization's issues.
const (
	KindTask      = "chat"
	KindIssueTask = "issue_chat"
)

// Agents is the agents service, as a chat asks one of its organization's agents: Prepare checks that the viewer may
// assign the agent the task and answers it, to record, and Guard shows tasks of a kind only to viewers who hold a
// permission.
type Agents interface {
	Prepare(ctx context.Context, v Viewer, orgID, agentID uuid.UUID, in agents.TaskInput) (agents.Task, error)
	Guard(kind, permission string)
}

// Tasks is the agents' store, as a chat records the tasks its messages ask for: called with the context of the chats
// store's Transact, it finds the task that talks in a thread, or records one, in that transaction.
type Tasks interface {
	// ActiveTask answers the number of a task that talks in the thread and has yet to end, or orgs.ErrNotFound.
	ActiveTask(ctx context.Context, threadID uuid.UUID) (int64, error)
	// Assign numbers the task in its organization and records it for actor, Queued, and answers it.
	Assign(ctx context.Context, actor uuid.UUID, t agents.Task) (agents.Task, error)
}

// WithAgents has the service's chats ask agents through a and t, and answers it. Without them, a message that asks an
// agent is refused. A task asked in an issue's chat carries its comments, so a shows it only to viewers who hold
// issues.read, as the chat itself.
func (s *Service) WithAgents(a Agents, t Tasks) *Service {
	a.Guard(KindIssueTask, permIssueRead)
	s.agents, s.tasks = a, t
	return s
}

// asking answers the task a message in the subject's chat asks agentID for, to record with the message, or nil when
// agentID is uuid.Nil: assigned by v, which takes agents.assign at the organization, working in the chat's repository,
// on its branch for a branch's chat, and private in a private conversation. The agent must read the chat, where the
// task talks.
func (s *Service) asking(ctx context.Context, v Viewer, subj Subject, agentID uuid.UUID, body string) (*agents.Task, error) {
	if agentID == uuid.Nil {
		return nil, nil
	}
	if s.agents == nil || s.tasks == nil {
		return nil, &orgs.ValidationError{Field: "ask", Code: "ask.unavailable", Message: "this installation's chats ask no agents"}
	}
	in := agents.TaskInput{Title: taskTitle(body), Instructions: cut(body, agents.MaxInstructions)}
	switch subj.Kind() {
	case KindBranch:
		in.RepoID, in.BranchID = subj.RepoID, subj.BranchID
	case KindRepository:
		in.RepoID = subj.RepoID
	}
	t, err := s.agents.Prepare(ctx, v, subj.OrgID, agentID, in)
	var verr *orgs.ValidationError
	if errors.As(err, &verr) && verr.Field == "agentId" {
		return nil, &orgs.ValidationError{Field: "ask", Code: "ask.invalid", Message: verr.Message}
	}
	if err != nil {
		return nil, fmt.Errorf("asking the agent: %w", err)
	}
	if err := s.agentReads(ctx, agentID, subj); err != nil {
		return nil, err
	}
	t.Kind, t.Private = KindTask, subj.Private()
	if subj.OfIssue() {
		t.Kind = KindIssueTask
	}
	return &t, nil
}

// agentReads refuses to ask agentID in the subject's chat unless the agent reads it by its own grants, as its task
// must, which its assigner's permissions cap further.
func (s *Service) agentReads(ctx context.Context, agentID uuid.UUID, subj Subject) error {
	r, scopeID := rulesOf(subj)
	held, err := s.authz.Holds(ctx, agentID, r.scope, scopeID)
	if err != nil {
		return fmt.Errorf("reading the agent's permissions: %w", err)
	}
	if !slices.Contains(held, r.view) || !slices.Contains(held, r.read) {
		return &orgs.ValidationError{Field: "ask", Code: "ask.unreadable", Message: "the agent can't read this chat, where its task would talk"}
	}
	return nil
}

// record runs write, which starts a thread or posts a message with task's ID as what it asks, and then, when write
// made the message and task is set, records task, talking in the message's thread: all in one transaction of the
// store's, unless task is nil. A thread where another task has yet to end is refused, since its task reads what is
// posted there.
func (s *Service) record(ctx context.Context, v Viewer, task *agents.Task, write func(ctx context.Context) (uuid.UUID, bool, error)) error {
	if task == nil {
		_, _, err := write(ctx)
		return err
	}
	err := s.store.Transact(ctx, v.ID, func(ctx context.Context) error {
		threadID, created, err := write(ctx)
		if err != nil || !created {
			return err
		}
		number, err := s.tasks.ActiveTask(ctx, threadID)
		switch {
		case err == nil:
			return &orgs.ValidationError{Field: "ask", Code: "ask.busy",
				Message: fmt.Sprintf("task #%d still talks in this thread, and reads what is posted there; ask once it ends", number)}
		case !errors.Is(err, orgs.ErrNotFound):
			return fmt.Errorf("finding the thread's task: %w", err)
		}
		task.ThreadID = threadID
		if _, err := s.tasks.Assign(ctx, v.ID, *task); err != nil {
			return fmt.Errorf("giving the agent its task: %w", err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("recording the message with the task it asks for: %w", err)
	}
	return nil
}

// asker is v as the poster of a message that asks for task, when it is set.
func asker(ctx context.Context, v Viewer, task *agents.Task) Poster {
	by := poster(ctx, v)
	if task != nil {
		by.Asks = task.ID
	}
	return by
}

// holdsForTask returns what v may do in another's conversation, under the token of a task its owner asked v for
// there: post in the task's thread alone, without moderating, while v and the owner both read what the conversation
// is about. Anyone else is repos.ErrNotFound, as for a conversation that isn't there.
func (s *Service) holdsForTask(ctx context.Context, v Viewer, subj Subject) (access, error) {
	g, ok := auth.TaskOf(ctx, v.ID)
	if !ok || !g.Private || g.AssignerID != subj.OwnerID || g.ThreadID == uuid.Nil {
		return access{}, repos.ErrNotFound
	}
	r, scopeID := rulesOf(subj)
	can, err := s.holdsBy(ctx, v, r, scopeID)
	if err != nil {
		return access{}, err
	}
	can.moderate, can.thread = false, g.ThreadID
	return can, nil
}

// taskTitle is the title of the task a message asks for: its first line that holds anything, without Markdown's
// marks of a heading or a quote, cut to what a task's title holds.
func taskTitle(body string) string {
	for line := range strings.Lines(body) {
		line = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "#> "))
		if line != "" {
			return cut(line, agents.MaxTitle)
		}
	}
	return "Asked in a chat"
}

// cut is s cut to its first n characters, its trailing spaces trimmed.
func cut(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return strings.TrimRight(string([]rune(s)[:n]), " \t\n")
}
