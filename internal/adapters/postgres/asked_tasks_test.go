package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/agents"
	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/chats"
	"cobranch.dev/internal/services/issues"
	"cobranch.dev/internal/services/orgs"
)

// ask has Max post body in one transaction of the chats store's, asking Scout for a task in its thread: a new thread of
// subj when thread is uuid.Nil, a reply in thread otherwise. Unless fail is set, the task is recorded, talking in the
// thread, in the same transaction; fail is then its error, which rolls both back. It answers the thread and the task's
// number.
func (f *agentFixture) ask(ctx context.Context, subj chats.Subject, thread uuid.UUID, task agents.Task, fail error) (uuid.UUID, int64, error) {
	by := chats.Poster{ID: f.max, Asks: task.ID}
	var number int64
	err := f.s.Transact(ctx, f.max, func(ctx context.Context) error {
		if thread == uuid.Nil {
			th, _, err := f.s.StartThread(ctx, by, subj, v7(), v7(), nil, "@Scout, have a look")
			if err != nil {
				return err
			}
			thread = th.ID
		} else if _, _, err := f.s.Post(ctx, by, subj, thread, v7(), "@Scout, again"); err != nil {
			return err
		}
		if fail != nil {
			return fail
		}
		task.ThreadID = thread
		got, err := f.a.Assign(ctx, f.max, task)
		number = got.Number
		return err
	})
	return thread, number, err
}

// chatTask is a task of Scout's that Max asks for in a chat, private or not.
func (f *agentFixture) chatTask(private bool) agents.Task {
	return agents.Task{ID: v7(), OrgID: f.acme.ID, Agent: agents.Principal{ID: f.scout.ID}, Kind: "chat", AssignedBy: &agents.Principal{ID: f.max},
		Title: "Have a look", Instructions: "@Scout, have a look", Private: private}
}

// TestChatStoreAsk has Max ask Scout in a thread of feature's chat: the message and the task commit together, the
// message names the task and Scout, and the thread has an active task until it ends. A transaction that fails keeps
// neither, and one that names a task it never records fails.
func TestChatStoreAsk(t *testing.T) {
	t.Parallel()
	f := newAgentFixture(t)
	ctx := context.Background()
	first := f.chatTask(false)
	thread, number, err := f.ask(ctx, f.feature, uuid.Nil, first, nil)
	if err != nil {
		t.Fatal(err)
	}
	f.checkAsked(t, thread, 0, first.ID, number)
	if got, err := f.a.ActiveTask(ctx, thread); err != nil || got != number {
		t.Errorf("ActiveTask() = %d, %v; want %d", got, err, number)
	}

	boom := errors.New("refused")
	if _, _, err := f.ask(ctx, f.feature, thread, f.chatTask(false), boom); !errors.Is(err, boom) {
		t.Fatalf("ask(failing) = %v, want %v", err, boom)
	}
	err = f.s.Transact(ctx, f.max, func(ctx context.Context) error {
		_, _, err := f.s.Post(ctx, chats.Poster{ID: f.max, Asks: uuid.New()}, f.feature, thread, v7(), "@Scout?")
		return err
	})
	if err == nil {
		t.Error("a message asking for a task never recorded committed")
	}
	if n := count(t, f.pool, "SELECT count(*) FROM chat_messages WHERE thread_id = $1", thread); n != 1 {
		t.Errorf("the thread's messages = %d, want the first alone", n)
	}
	if n := count(t, f.pool, "SELECT count(*) FROM agent_tasks WHERE org_id = $1", f.acme.ID); n != 1 {
		t.Errorf("tasks = %d, want the first alone", n)
	}

	if _, err := f.a.Cancel(ctx, f.max, f.acme.ID, number, agents.AnyVersion); err != nil {
		t.Fatal(err)
	}
	if _, err := f.a.ActiveTask(ctx, thread); !errors.Is(err, orgs.ErrNotFound) {
		t.Errorf("ActiveTask(the task ended) = %v, want %v", err, orgs.ErrNotFound)
	}
	second := f.chatTask(false)
	if _, number, err = f.ask(ctx, f.feature, thread, second, nil); err != nil {
		t.Fatal(err)
	}
	f.checkAsked(t, thread, 1, second.ID, number)
	if got, err := f.a.ActiveTask(ctx, thread); err != nil || got != number {
		t.Errorf("ActiveTask(asked again) = %d, %v; want %d", got, err, number)
	}
}

// checkAsked checks that the thread's message at i asked Scout for task id, numbered number.
func (f *agentFixture) checkAsked(t *testing.T, thread uuid.UUID, i int, id uuid.UUID, number int64) {
	t.Helper()
	th, err := f.s.Thread(context.Background(), f.feature, thread)
	if err != nil || len(th.Messages) <= i {
		t.Fatalf("Thread() = %+v, %v", th, err)
	}
	a := th.Messages[i].Asked
	if a == nil || a.ID != id || a.Number != number || a.Agent.ID != f.scout.ID || a.Agent.DisplayName != "Scout" {
		t.Errorf("message %d asked %+v; want task %d of Scout's", i, a, number)
	}
}

// TestAgentStorePrivateTask records a private task: it reads back private, alone and listed, and its token says so,
// with the thread it talks in.
func TestAgentStorePrivateTask(t *testing.T) {
	t.Parallel()
	f := newAgentFixture(t)
	ctx := context.Background()
	subj, err := f.s.Conversation(ctx, f.max, f.acme.ID, uuid.Nil, f.scout.ID)
	if err != nil {
		t.Fatal(err)
	}
	thread, number, err := f.ask(ctx, subj, uuid.Nil, f.chatTask(true), nil)
	if err != nil {
		t.Fatal(err)
	}
	f.assigned(t, "Out in the open", false, uuid.Nil)
	if got, err := f.a.Task(ctx, f.acme.ID, number); err != nil || !got.Private || got.ThreadID != thread {
		t.Errorf("Task() = %+v, %v; want private, in the conversation's thread", got, err)
	}
	listed, err := f.a.Tasks(ctx, f.acme.ID, agents.TaskFilter{}, nil, nil, 10)
	if err != nil || len(listed) != 2 || listed[0].Private || !listed[1].Private {
		t.Errorf("Tasks() = %+v, %v; want the open task, then the private one", listed, err)
	}
	s := NewAuthStore(f.pool)
	secret := auth.Hash(auth.NewSecret(auth.PrefixTaskToken))
	if err := s.CreateTaskToken(ctx, listed[1].ID, secret, now.AddDate(1, 0, 0)); err != nil {
		t.Fatal(err)
	}
	if c, _, err := s.TaskToken(ctx, secret); err != nil || !c.Task.Private || c.Task.ThreadID != thread || c.Task.AssignerID != f.max {
		t.Errorf("TaskToken() = %+v, %v; want a private task's grant, in its thread", c.Task, err)
	}
}

// TestAgentStoreTaskByIDWhere finds the work of tasks asked in each kind of chat: where they talk, for the harness.
func TestAgentStoreTaskByIDWhere(t *testing.T) {
	t.Parallel()
	f := newAgentFixture(t)
	ctx := context.Background()
	issue, err := NewIssueStore(f.pool).Create(ctx, f.olivia, issues.Issue{ID: v7(), OrgID: f.acme.ID, Title: "Safari", State: issues.StateTodo,
		CreatedBy: &issues.Principal{ID: f.olivia}, CreatedAt: now, UpdatedAt: now, Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	issueChat, err := f.s.Issue(ctx, f.acme.ID, issue.Number)
	if err != nil {
		t.Fatal(err)
	}
	conversation := func(repo, agent uuid.UUID) chats.Subject {
		subj, err := f.s.Conversation(ctx, f.max, f.acme.ID, repo, agent)
		if err != nil {
			t.Fatal(err)
		}
		return subj
	}
	tests := []struct {
		name  string
		subj  chats.Subject
		issue int64
		want  *agents.Conversation
	}{
		{name: "a branch's chat", subj: f.feature},
		{name: "an issue's chat", subj: issueChat, issue: int64(issue.Number)},
		{name: "a conversation about the organization", subj: conversation(uuid.Nil, uuid.Nil), want: &agents.Conversation{}},
		{name: "a conversation about a repository", subj: conversation(f.website.ID, uuid.Nil), want: &agents.Conversation{RepoSlug: "website"}},
		{name: "a conversation with an agent", subj: conversation(uuid.Nil, f.scout.ID), want: &agents.Conversation{AgentID: f.scout.ID}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			task := f.chatTask(tt.subj.Private())
			if _, _, err := f.ask(ctx, tt.subj, uuid.Nil, task, nil); err != nil {
				t.Fatal(err)
			}
			w, err := f.a.TaskByID(ctx, task.ID)
			if err != nil || w.IssueNumber != tt.issue || (w.Conversation == nil) != (tt.want == nil) || tt.want != nil && *w.Conversation != *tt.want {
				t.Errorf("TaskByID() = issue %d, conversation %+v, %v; want issue %d, conversation %+v", w.IssueNumber, w.Conversation, err, tt.issue, tt.want)
			}
		})
	}
}
