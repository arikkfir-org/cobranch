package chats_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/agents"
	"cobranch.dev/internal/services/agents/agentstest"
	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/chats"
	"cobranch.dev/internal/services/chats/chatstest"
	"cobranch.dev/internal/services/repos"
)

// askFixture is the conversations' fixture with the agents its chats ask: Builder, the agent of its conversations,
// which is a member of acme and develops website, and Scout, a guest there; and website's feature branch at commit 2
// and issue ACME-7, whose chats are shared.
type askFixture struct {
	*conversationFixture
	tasks          *agentstest.Store
	scout, reader  uuid.UUID
	feature, issue chats.Subject
}

func newAskFixture(t *testing.T) *askFixture {
	t.Helper()
	f := &askFixture{conversationFixture: newConversationFixture(t), tasks: agentstest.NewStore()}
	f.tasks.AddAgent(agents.Agent{ID: f.agent, OrgID: f.org, Name: "Builder"})
	f.scout = f.tasks.AddAgent(agents.Agent{OrgID: f.org, Name: "Scout"}).ID
	f.reader = f.tasks.AddAgent(agents.Agent{OrgID: f.org, Name: "Reader"}).ID
	f.o.Grant(f.agent, auth.RoleMember, auth.ScopeOrganization, f.org)
	f.o.Grant(f.agent, auth.RoleDeveloper, auth.ScopeRepository, f.repo)
	f.o.Grant(f.scout, auth.RoleGuest, auth.ScopeOrganization, f.org)
	f.o.Grant(f.reader, auth.RoleGuest, auth.ScopeOrganization, f.org)
	f.o.Grant(f.reader, auth.RoleReader, auth.ScopeRepository, f.repo)
	f.feature = f.store.SetBranch(chats.Subject{OrgID: f.org, RepoID: f.repo, Branch: "feature", Head: sha(2), Org: "acme", Repo: "website"})
	f.issue = f.store.SetIssue(chats.Subject{OrgID: f.org, Number: 7, Issue: "ACME-7", Org: "acme"})
	f.tasks.Repos[f.repo] = agentstest.Repository{Org: f.org, Slug: "website"}
	f.tasks.Branches[f.feature.BranchID] = agentstest.Branch{Repo: f.repo, Name: "feature"}
	f.tasks.Names[f.mem.ID] = "Mem"
	f.service.WithAgents(agents.NewService(f.tasks, auth.NewAuthorizer(f.grants)), chatstest.AgentTasks{Store: f.tasks, Chats: f.store})
	return f
}

// subject is the chat of kind Mem posts in: feature's, ACME-7's, or Mem's own conversation of that kind.
func (f *askFixture) subject(t *testing.T, kind chats.Kind) chats.Subject {
	t.Helper()
	switch kind {
	case chats.KindBranch:
		return f.feature
	case chats.KindIssue:
		return f.issue
	}
	return f.mine(t, f.mem, kind)
}

// only is the one task the agents' store keeps, or fails the test.
func (f *askFixture) only(t *testing.T) agents.Task {
	t.Helper()
	kept := f.tasks.KeptTasks()
	if len(kept) != 1 {
		t.Fatalf("tasks = %+v; want one", kept)
	}
	return kept[0]
}

func TestAskInEveryChat(t *testing.T) {
	t.Parallel()
	tests := []struct {
		kind    chats.Kind
		want    func(*askFixture) agents.Task
		private bool
	}{
		{kind: chats.KindBranch, want: func(f *askFixture) agents.Task {
			return agents.Task{Kind: chats.KindTask, RepoID: f.repo, BranchID: f.feature.BranchID}
		}},
		{kind: chats.KindIssue, want: func(*askFixture) agents.Task { return agents.Task{Kind: chats.KindIssueTask} }},
		{kind: chats.KindOrganization, want: func(*askFixture) agents.Task { return agents.Task{Kind: chats.KindTask, Private: true} }},
		{kind: chats.KindRepository, want: func(f *askFixture) agents.Task {
			return agents.Task{Kind: chats.KindTask, RepoID: f.repo, Private: true}
		}},
		{kind: chats.KindAgent, want: func(*askFixture) agents.Task { return agents.Task{Kind: chats.KindTask, Private: true} }},
	}
	for _, tc := range tests {
		t.Run(string(tc.kind), func(t *testing.T) {
			t.Parallel()
			f := newAskFixture(t)
			subj := f.subject(t, tc.kind)
			th, err := f.service.StartThread(context.Background(), f.mem, subj, chats.ThreadInput{Body: "## Fix the login\n\nIt **fails**.", Ask: f.agent})
			if err != nil {
				t.Fatal(err)
			}
			task, want := f.only(t), tc.want(f)
			if task.Kind != want.Kind || task.Private != want.Private || task.RepoID != want.RepoID || task.BranchID != want.BranchID ||
				task.ThreadID != th.ID || task.Title != "Fix the login" || task.Instructions != "## Fix the login\n\nIt **fails**." ||
				task.Agent.ID != f.agent || task.AssignedBy == nil || task.AssignedBy.ID != f.mem.ID || task.State != agents.TaskQueued {
				t.Errorf("task = %+v; want %+v in thread %s, by Mem", task, want, th.ID)
			}
			asked := th.Messages[0].Asked
			if asked == nil || asked.ID != task.ID || asked.Number != task.Number || asked.Agent.DisplayName != "Builder" {
				t.Errorf("Asked = %+v; want task %d of Builder", asked, task.Number)
			}
		})
	}
}

func TestAskByDefaultInAnAgentsConversation(t *testing.T) {
	t.Parallel()
	f := newAskFixture(t)
	ctx := context.Background()
	th, err := f.service.StartThread(ctx, f.mem, f.mine(t, f.mem, chats.KindAgent), chats.ThreadInput{Body: "What's in your queue?"})
	if err != nil {
		t.Fatal(err)
	}
	if task := f.only(t); task.Agent.ID != f.agent || task.ThreadID != th.ID {
		t.Errorf("task = %+v; want Builder's in thread %s", task, th.ID)
	}
	if _, err := f.service.StartThread(ctx, f.mem, f.mine(t, f.mem, chats.KindOrganization), chats.ThreadInput{Body: "A note"}); err != nil {
		t.Fatal(err)
	}
	if kept := f.tasks.KeptTasks(); len(kept) != 1 {
		t.Errorf("tasks after a thread about acme = %d; want 1: a thread asks nobody elsewhere", len(kept))
	}
}

func TestAskRefused(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		kind    chats.Kind
		viewer  func(*askFixture) chats.Viewer
		agent   func(*askFixture) uuid.UUID
		wantErr error
	}{
		{name: "a viewer who assigns no tasks", kind: chats.KindOrganization, viewer: func(f *askFixture) chats.Viewer { return f.gus },
			agent: func(f *askFixture) uuid.UUID { return f.agent }, wantErr: auth.ErrForbidden},
		{name: "no agent of the organization", kind: chats.KindBranch, viewer: func(f *askFixture) chats.Viewer { return f.mem },
			agent: func(*askFixture) uuid.UUID { return uuid.New() }, wantErr: invalid("ask.invalid")},
		{name: "an agent that can't read the repository", kind: chats.KindRepository, viewer: func(f *askFixture) chats.Viewer { return f.mem },
			agent: func(f *askFixture) uuid.UUID { return f.scout }, wantErr: invalid("ask.unreadable")},
		{name: "an agent that can't read the issues", kind: chats.KindIssue, viewer: func(f *askFixture) chats.Viewer { return f.mem },
			agent: func(f *askFixture) uuid.UUID { return f.scout }, wantErr: invalid("ask.unreadable")},
		{name: "an agent that reads the branch's chat but can't post there", kind: chats.KindBranch, viewer: func(f *askFixture) chats.Viewer { return f.mem },
			agent: func(f *askFixture) uuid.UUID { return f.reader }, wantErr: invalid("ask.readonly")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newAskFixture(t)
			v := tc.viewer(f)
			subj := f.subject(t, tc.kind)
			if v.ID != f.mem.ID {
				subj = f.mine(t, v, tc.kind)
			}
			_, err := f.service.StartThread(context.Background(), v, subj, chats.ThreadInput{Body: "Look", Ask: tc.agent(f)})
			if !is(err, tc.wantErr) {
				t.Fatalf("StartThread = %v; want %v", err, tc.wantErr)
			}
			if page, _ := f.service.Threads(context.Background(), v, subj, uuid.Nil, 10); len(page.Threads) != 0 || len(f.tasks.KeptTasks()) != 0 {
				t.Errorf("after a refusal: %d threads, %d tasks; want none", len(page.Threads), len(f.tasks.KeptTasks()))
			}
		})
	}
}

func TestAskOnceTheThreadsTaskEnds(t *testing.T) {
	t.Parallel()
	f := newAskFixture(t)
	ctx := context.Background()
	subj := f.mine(t, f.mem, chats.KindRepository)
	th, err := f.service.StartThread(ctx, f.mem, subj, chats.ThreadInput{Body: "Why is CI slow?", Ask: f.agent})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Post(ctx, f.mem, subj, th.ID, chats.MessageInput{Body: "And the tests?", Ask: f.agent}); !is(err, invalid("ask.busy")) {
		t.Fatalf("Post asking while task 1 talks = %v; want ask.busy", err)
	}
	if got, _ := f.service.Thread(ctx, f.mem, subj, th.ID); len(got.Messages) != 1 {
		t.Fatalf("messages after the refusal = %d; want 1: the refused one isn't posted", len(got.Messages))
	}
	m, err := f.service.Post(ctx, f.mem, subj, th.ID, chats.MessageInput{Body: "It's the cache, I think."})
	if err != nil || m.Asked != nil {
		t.Fatalf("Post asking nobody = %+v, %v; want a message", m.Asked, err)
	}
	f.tasks.SetState(f.org, 1, agents.TaskSucceeded)
	m, err = f.service.Post(ctx, f.mem, subj, th.ID, chats.MessageInput{Body: "Now the tests, please.", Ask: f.agent})
	if err != nil {
		t.Fatal(err)
	}
	kept := f.tasks.KeptTasks()
	if len(kept) != 2 || m.Asked == nil || m.Asked.Number != 2 || kept[1].ThreadID != th.ID || kept[1].Title != "Now the tests, please." {
		t.Errorf("tasks = %+v, Asked = %+v; want task 2 in the thread", kept, m.Asked)
	}
	again, err := f.service.Post(ctx, f.mem, subj, th.ID, chats.MessageInput{ID: m.ID, Body: "Now the tests, please.", Ask: f.agent})
	if err != nil || again.Asked == nil || again.Asked.Number != 2 || len(f.tasks.KeptTasks()) != 2 {
		t.Errorf("posting it again = %+v, %v, %d tasks; want the message as it is, and no third task", again.Asked, err, len(f.tasks.KeptTasks()))
	}
}

func TestAskWithoutAgents(t *testing.T) {
	t.Parallel()
	f := newConversationFixture(t)
	subj := f.mine(t, f.mem, chats.KindOrganization)
	if _, err := f.service.StartThread(context.Background(), f.mem, subj, chats.ThreadInput{Body: "Look", Ask: f.agent}); !is(err, invalid("ask.unavailable")) {
		t.Errorf("StartThread = %v; want ask.unavailable", err)
	}
	if _, err := f.service.StartThread(context.Background(), f.mem, f.mine(t, f.mem, chats.KindAgent), chats.ThreadInput{Body: "Hi"}); err != nil {
		t.Errorf("StartThread in an agent's conversation = %v; want a thread that asks nobody", err)
	}
}

// working is a context whose credential is the token of a task of agent's, as the servers' middleware sets it.
func working(agent uuid.UUID, g auth.TaskGrant) (context.Context, chats.Viewer) {
	v := chats.Viewer{Principal: auth.Principal{ID: agent, Kind: auth.KindAgent, DisplayName: "Builder"}}
	ctx := auth.WithViewer(context.Background(), auth.Authenticated{User: v, Credential: auth.Credential{Kind: auth.CredentialTaskToken, PrincipalID: agent, Task: g}})
	return ctx, v
}

func TestTaskInAPrivateConversation(t *testing.T) {
	t.Parallel()
	f := newAskFixture(t)
	ctx := context.Background()
	mine := f.mine(t, f.mem, chats.KindAgent)
	th, err := f.service.StartThread(ctx, f.mem, mine, chats.ThreadInput{Body: "Summarize your week"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := f.service.StartThread(ctx, f.mem, mine, chats.ThreadInput{Body: "Something else", Ask: f.scout})
	if err != nil {
		t.Fatal(err)
	}
	task := f.only2(t)
	actx, agent := working(f.agent, auth.TaskGrant{ID: task.ID, OrgID: f.org, AssignerID: f.mem.ID, Private: true, ThreadID: th.ID})

	subj, err := f.service.Agent(actx, agent, f.org, f.agent)
	if err != nil || subj.OwnerID != f.mem.ID || subj.ConversationID != mine.ConversationID {
		t.Fatalf("Agent = %+v, %v; want Mem's conversation", subj, err)
	}
	page, err := f.service.Threads(actx, agent, subj, uuid.Nil, 10)
	if err != nil || len(page.Threads) != 1 || page.Threads[0].ID != th.ID {
		t.Fatalf("Threads = %+v, %v; want the task's thread alone", page.Threads, err)
	}
	m, err := f.service.Post(actx, agent, subj, th.ID, chats.MessageInput{Body: "Three tasks, all done."})
	if err != nil || m.Task == nil || m.Task.ID != task.ID {
		t.Fatalf("Post = %+v, %v; want a message linking the task", m, err)
	}
	updates, err := f.service.Updates(actx, agent, subj, 0, 0)
	if err != nil || len(updates) != 2 || updates[0].Thread.ID != th.ID || updates[1].Thread.ID != th.ID {
		t.Errorf("Updates = %+v, %v; want the task's thread's two", updates, err)
	}

	if _, err := f.service.Thread(actx, agent, subj, other.ID); !errors.Is(err, repos.ErrNotFound) {
		t.Errorf("Thread of another = %v; want not found", err)
	}
	if _, err := f.service.Post(actx, agent, subj, other.ID, chats.MessageInput{Body: "Hi"}); !errors.Is(err, repos.ErrNotFound) {
		t.Errorf("Post to another = %v; want not found", err)
	}
	if _, err := f.service.Message(actx, agent, subj, other.ID, other.Messages[0].ID); !errors.Is(err, repos.ErrNotFound) {
		t.Errorf("Message of another = %v; want not found", err)
	}
	if _, err := f.service.StartThread(actx, agent, subj, chats.ThreadInput{Body: "New"}); !errors.Is(err, auth.ErrForbidden) {
		t.Errorf("StartThread = %v; want forbidden", err)
	}
	if _, err := f.service.Resolve(actx, agent, subj, th.ID, true, chats.AnyVersion); !errors.Is(err, auth.ErrForbidden) {
		t.Errorf("Resolve = %v; want forbidden: the task doesn't moderate", err)
	}
}

// only2 is the first of the two tasks the agents' store keeps, Builder's.
func (f *askFixture) only2(t *testing.T) agents.Task {
	t.Helper()
	kept := f.tasks.KeptTasks()
	if len(kept) != 2 || kept[0].Agent.ID != f.agent {
		t.Fatalf("tasks = %+v; want Builder's and Scout's", kept)
	}
	return kept[0]
}

func TestTaskTokenOutsideItsConversation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		grant func(f *askFixture, thread uuid.UUID) auth.TaskGrant
		lose  bool
	}{
		{name: "a task that isn't private", grant: func(f *askFixture, thread uuid.UUID) auth.TaskGrant {
			return auth.TaskGrant{ID: uuid.New(), OrgID: f.org, AssignerID: f.mem.ID, ThreadID: thread}
		}},
		{name: "another's task", grant: func(f *askFixture, thread uuid.UUID) auth.TaskGrant {
			return auth.TaskGrant{ID: uuid.New(), OrgID: f.org, AssignerID: f.gus.ID, Private: true, ThreadID: thread}
		}},
		{name: "an assigner who lost the organization", lose: true, grant: func(f *askFixture, thread uuid.UUID) auth.TaskGrant {
			return auth.TaskGrant{ID: uuid.New(), OrgID: f.org, AssignerID: f.mem.ID, Private: true, ThreadID: thread}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newAskFixture(t)
			mine := f.mine(t, f.mem, chats.KindOrganization)
			th, err := f.service.StartThread(context.Background(), f.mem, mine, chats.ThreadInput{Body: "Hi", Ask: f.agent})
			if err != nil {
				t.Fatal(err)
			}
			if tc.lose {
				f.grants.revoke(f.mem.ID)
			}
			actx, agent := working(f.agent, tc.grant(f, th.ID))
			if _, err := f.service.Thread(actx, agent, mine, th.ID); !errors.Is(err, repos.ErrNotFound) {
				t.Errorf("Thread = %v; want not found", err)
			}
		})
	}
}
