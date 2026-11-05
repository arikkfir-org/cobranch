package agents_test

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/agents"
	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/orgs"
)

func TestAssign(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	scout := f.agent(t, f.olive, "Scout")
	tests := []struct {
		name   string
		viewer agents.Viewer
		agent  uuid.UUID
		in     agents.TaskInput
		want   error
		where  agents.Task
	}{
		{name: "anywhere", viewer: f.mel, in: agents.TaskInput{Title: " Tidy up ", Instructions: "Everything"}},
		{name: "a repository", viewer: f.max, in: agents.TaskInput{Title: "Read", RepoID: f.website},
			where: agents.Task{RepoID: f.website, RepoSlug: "website"}},
		{name: "a branch by name, in a thread", viewer: f.mel, in: agents.TaskInput{Title: "Fix", RepoID: f.website, Branch: "refs/heads/feature", ThreadID: f.thread},
			where: agents.Task{RepoID: f.website, RepoSlug: "website", BranchID: f.feature, Branch: "feature", ThreadID: f.thread}},
		{name: "a branch by ID", viewer: f.mel, in: agents.TaskInput{Title: "Fix", RepoID: f.website, BranchID: f.other},
			where: agents.Task{RepoID: f.website, RepoSlug: "website", BranchID: f.other, Branch: "other"}},
		{name: "a repository the assigner doesn't read", viewer: f.mel, in: agents.TaskInput{Title: "Peek", RepoID: f.vault}, want: invalid("repository.invalid")},
		{name: "another organization's repository", viewer: f.olive, in: agents.TaskInput{Title: "Peek", RepoID: uuid.New()}, want: invalid("repository.invalid")},
		{name: "a deleted branch", viewer: f.mel, in: agents.TaskInput{Title: "Fix", RepoID: f.website, Branch: "gone"}, want: invalid("branch.invalid")},
		{name: "a branch not there", viewer: f.mel, in: agents.TaskInput{Title: "Fix", RepoID: f.website, BranchID: uuid.New()}, want: invalid("branch.invalid")},
		{name: "another branch's thread", viewer: f.mel, in: agents.TaskInput{Title: "Fix", RepoID: f.website, BranchID: f.other, ThreadID: f.thread}, want: invalid("thread.invalid")},
		{name: "a thread not there", viewer: f.mel, in: agents.TaskInput{Title: "Fix", RepoID: f.website, Branch: "feature", ThreadID: uuid.New()}, want: invalid("thread.invalid")},
		{name: "a branch without its repository", viewer: f.mel, in: agents.TaskInput{Title: "Fix", Branch: "feature"}, want: invalid("task.branch_invalid")},
		{name: "a thread without its branch", viewer: f.mel, in: agents.TaskInput{Title: "Fix", RepoID: f.website, ThreadID: f.thread}, want: invalid("task.thread_invalid")},
		{name: "no title", viewer: f.mel, in: agents.TaskInput{Title: " "}, want: invalid("task.title_invalid")},
		{name: "an agent not there", viewer: f.mel, agent: uuid.New(), in: agents.TaskInput{Title: "Fix"}, want: invalid("agent.invalid")},
		{name: "a guest", viewer: f.gus, in: agents.TaskInput{Title: "Fix"}, want: auth.ErrForbidden},
		{name: "an agent", viewer: f.bot, in: agents.TaskInput{Title: "Fix"}, want: auth.ErrForbidden},
		{name: "no member", viewer: f.nia, in: agents.TaskInput{Title: "Fix"}, want: orgs.ErrNotFound},
	}
	number := int64(0)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agent := cmp.Or(tt.agent, scout.ID)
			task, err := f.service.Assign(context.Background(), tt.viewer, f.org, agent, tt.in)
			if !is(err, tt.want) {
				t.Fatalf("Assign() = %v, want %v", err, tt.want)
			}
			if err != nil {
				return
			}
			number++
			got := agents.Task{RepoID: task.RepoID, RepoSlug: task.RepoSlug, BranchID: task.BranchID, Branch: task.Branch, ThreadID: task.ThreadID}
			if got != tt.where {
				t.Errorf("Assign() works at %+v, want %+v", got, tt.where)
			}
			if task.Number != number || task.State != agents.TaskQueued || task.Kind != agents.KindAssigned || task.Agent.ID != scout.ID ||
				task.AssignedBy == nil || task.AssignedBy.ID != tt.viewer.ID || task.Title != strings.TrimSpace(tt.in.Title) || task.ByCobranch() {
				t.Errorf("Assign() = %+v, want number %d, queued, assigned by %s", task, number, tt.viewer.DisplayName)
			}
		})
	}
}

// taskFixture has Scout and Archer hold tasks: 1 by Mel anywhere, 2 by Mel on website, 3 by Olive on vault, which Mel
// doesn't read, 4 by Max to Archer on website, and 5 by Olive anywhere, which ended.
func taskFixture(t *testing.T) (*fixture, agents.Agent, agents.Agent) {
	t.Helper()
	f := newFixture(t)
	scout, archer := f.agent(t, f.olive, "Scout"), f.agent(t, f.olive, "Archer")
	f.assign(t, f.mel, scout, "One", uuid.Nil)
	f.assign(t, f.mel, scout, "Two", f.website)
	f.assign(t, f.olive, scout, "Three", f.vault)
	f.assign(t, f.max, archer, "Four", f.website)
	f.assign(t, f.olive, scout, "Five", uuid.Nil)
	f.store.SetState(f.org, 5, agents.TaskSucceeded)
	return f, scout, archer
}

func numbers(tasks []agents.Task) []int64 {
	var out []int64
	for _, t := range tasks {
		out = append(out, t.Number)
	}
	return out
}

func TestTasks(t *testing.T) {
	t.Parallel()
	f, scout, archer := taskFixture(t)
	tests := []struct {
		name   string
		viewer agents.Viewer
		filter agents.TaskFilter
		limit  int
		want   []int64
		next   int64
		err    error
	}{
		{name: "an owner reads all", viewer: f.olive, limit: 10, want: []int64{5, 4, 3, 2, 1}},
		{name: "a member reads those on repositories they read", viewer: f.mel, limit: 10, want: []int64{5, 4, 2, 1}},
		{name: "a guest reads those on no repository", viewer: f.gus, limit: 10, want: []int64{5, 1}},
		{name: "a page", viewer: f.olive, limit: 2, want: []int64{5, 4}, next: 4},
		{name: "the next page", viewer: f.olive, filter: agents.TaskFilter{Before: 4}, limit: 2, want: []int64{3, 2}, next: 2},
		{name: "the last page", viewer: f.olive, filter: agents.TaskFilter{Before: 2}, limit: 2, want: []int64{1}},
		{name: "an agent's", viewer: f.olive, filter: agents.TaskFilter{AgentID: archer.ID}, limit: 10, want: []int64{4}},
		{name: "a repository's", viewer: f.mel, filter: agents.TaskFilter{RepoID: f.website, AgentID: scout.ID}, limit: 10, want: []int64{2}},
		{name: "active", viewer: f.olive, filter: agents.TaskFilter{States: []agents.TaskState{agents.TaskQueued, agents.TaskRunning}}, limit: 10, want: []int64{4, 3, 2, 1}},
		{name: "a state not there", viewer: f.olive, filter: agents.TaskFilter{States: []agents.TaskState{"lost"}}, limit: 10, err: invalid("task.state_invalid")},
		{name: "no limit", viewer: f.olive, limit: 0, err: invalid("limit.invalid")},
		{name: "a limit past a page", viewer: f.olive, limit: agents.MaxPage + 1, err: invalid("limit.invalid")},
		{name: "no member", viewer: f.nia, limit: 10, err: orgs.ErrNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			page, err := f.service.Tasks(context.Background(), tt.viewer, f.org, tt.filter, tt.limit)
			if !is(err, tt.err) {
				t.Fatalf("Tasks() = %v, want %v", err, tt.err)
			}
			if got := numbers(page.Tasks); !slices.Equal(got, tt.want) || page.Next != tt.next {
				t.Errorf("Tasks() = %v, next %d; want %v, next %d", got, page.Next, tt.want, tt.next)
			}
		})
	}
}

func TestTask(t *testing.T) {
	t.Parallel()
	f, _, _ := taskFixture(t)
	tests := []struct {
		name   string
		viewer agents.Viewer
		number int64
		want   error
	}{
		{name: "on no repository", viewer: f.gus, number: 1},
		{name: "on a repository the viewer reads", viewer: f.max, number: 2},
		{name: "on a repository the viewer doesn't read", viewer: f.mel, number: 3, want: orgs.ErrNotFound},
		{name: "not there", viewer: f.olive, number: 9, want: orgs.ErrNotFound},
		{name: "no member", viewer: f.nia, number: 1, want: orgs.ErrNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			task, err := f.service.Task(context.Background(), tt.viewer, f.org, tt.number)
			if !errors.Is(err, tt.want) || (err == nil && task.Number != tt.number) {
				t.Errorf("Task(%d) = %d, %v; want %v", tt.number, task.Number, err, tt.want)
			}
		})
	}
}

func TestCancelTask(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		viewer  func(*fixture) agents.Viewer
		number  int64
		version int64
		want    error
	}{
		{name: "its assigner", viewer: func(f *fixture) agents.Viewer { return f.mel }, number: 2, version: 1},
		{name: "a principals manager", viewer: func(f *fixture) agents.Viewer { return f.stu }, number: 1, version: agents.AnyVersion},
		{name: "another member", viewer: func(f *fixture) agents.Viewer { return f.max }, number: 2, version: 1, want: auth.ErrForbidden},
		{name: "on a repository the viewer doesn't read", viewer: func(f *fixture) agents.Viewer { return f.mel }, number: 3, version: 1, want: orgs.ErrNotFound},
		{name: "a stale version", viewer: func(f *fixture) agents.Viewer { return f.mel }, number: 1, version: 2, want: agents.ErrChanged},
		{name: "ended", viewer: func(f *fixture) agents.Viewer { return f.olive }, number: 5, version: agents.AnyVersion, want: agents.ErrEnded},
		{name: "not there", viewer: func(f *fixture) agents.Viewer { return f.olive }, number: 9, version: 1, want: orgs.ErrNotFound},
		{name: "no member", viewer: func(f *fixture) agents.Viewer { return f.nia }, number: 1, version: 1, want: orgs.ErrNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f, _, _ := taskFixture(t)
			v := tt.viewer(f)
			task, err := f.service.CancelTask(context.Background(), v, f.org, tt.number, tt.version)
			if !errors.Is(err, tt.want) {
				t.Fatalf("CancelTask(%d) = %v, want %v", tt.number, err, tt.want)
			}
			if err == nil && (task.State != agents.TaskCancelled || task.CancelledBy == nil || task.CancelledBy.ID != v.ID || task.EndedAt.IsZero()) {
				t.Errorf("CancelTask(%d) = %+v, want cancelled by %s", tt.number, task, v.DisplayName)
			}
		})
	}
}

// TestGuardedTasks guards the kind issue with issues.read: Gus, a guest who reads website, and Stu, a steward, who
// manages the principals but reads no issues, find no task of an issue, on website or on no repository, where Max, a
// member who reads website, finds both.
func TestGuardedTasks(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.service.Guard("issue", "issues.read")
	f.grants.Grant(f.gus.ID, auth.RoleReader, auth.ScopeRepository, f.website)
	scout := f.agent(t, f.olive, "Scout")
	f.assign(t, f.mel, scout, "Plain", f.website)
	for _, repo := range []uuid.UUID{f.website, uuid.Nil} {
		task := agents.Task{OrgID: f.org, Agent: agents.Principal{ID: scout.ID}, Kind: "issue", AssignedBy: &agents.Principal{ID: f.mel.ID},
			Title: "ACME-1 What the issue says", Instructions: "Only issues.read reads this", RepoID: repo, State: agents.TaskQueued}
		if _, err := f.store.Assign(context.Background(), f.mel.ID, task); err != nil {
			t.Fatal(err)
		}
	}
	tests := []struct {
		name   string
		viewer agents.Viewer
		want   []int64
	}{
		{name: "a member who reads the repository", viewer: f.max, want: []int64{3, 2, 1}},
		{name: "a guest who reads the repository", viewer: f.gus, want: []int64{1}},
		{name: "a steward who reads no issues", viewer: f.stu},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			page, err := f.service.Tasks(context.Background(), tt.viewer, f.org, agents.TaskFilter{}, 10)
			if err != nil || !slices.Equal(numbers(page.Tasks), tt.want) {
				t.Errorf("Tasks() = %v, %v; want %v", numbers(page.Tasks), err, tt.want)
			}
			findsOnly(t, f, tt.viewer, 3, tt.want)
			if _, err := f.service.CancelTask(context.Background(), tt.viewer, f.org, 3, agents.AnyVersion); !slices.Contains(tt.want, 3) && !errors.Is(err, orgs.ErrNotFound) {
				t.Errorf("CancelTask(3) = %v, want %v", err, orgs.ErrNotFound)
			}
		})
	}
}

// findsOnly checks that v finds, of the tasks numbered 1 to last, those of want and no others.
func findsOnly(t *testing.T, f *fixture, v agents.Viewer, last int64, want []int64) {
	t.Helper()
	for number := int64(1); number <= last; number++ {
		_, err := f.service.Task(context.Background(), v, f.org, number)
		if found := slices.Contains(want, number); found != (err == nil) || (err != nil && !errors.Is(err, orgs.ErrNotFound)) {
			t.Errorf("Task(%d) = %v, want found %t", number, err, found)
		}
	}
}

// TestPrivateTasks has Mel ask Scout for two private tasks, one on website: Mel finds them whole, Olive, who owns acme,
// lists them redacted, holding their places in Scout's queue, and finds neither; a listing of website's tasks leaves
// out the one there, which would tell where it works. Bot, an agent, under the token of a private task of Mel's,
// works for Mel and finds both; under a token of a task that isn't private, it works for itself and finds neither.
func TestPrivateTasks(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.grants.Grant(f.bot.ID, auth.RoleReader, auth.ScopeRepository, f.website)
	scout := f.agent(t, f.olive, "Scout")
	f.assign(t, f.mel, scout, "Public", uuid.Nil)
	for _, repo := range []uuid.UUID{uuid.Nil, f.website} {
		task := agents.Task{OrgID: f.org, Agent: agents.Principal{ID: scout.ID, DisplayName: "Scout"}, Kind: "chat", AssignedBy: &agents.Principal{ID: f.mel.ID},
			Title: "Mel's own question", Instructions: "Only Mel reads this", Summary: "", RepoID: repo, Private: true, State: agents.TaskQueued}
		if _, err := f.store.Assign(context.Background(), f.mel.ID, task); err != nil {
			t.Fatal(err)
		}
	}
	token := func(private bool) context.Context {
		g := auth.TaskGrant{ID: uuid.New(), OrgID: f.org, AssignerID: f.mel.ID, Private: private}
		return auth.WithViewer(context.Background(), auth.Authenticated{User: f.bot, Credential: auth.Credential{Kind: auth.CredentialTaskToken, PrincipalID: f.bot.ID, Task: g}})
	}
	tests := []struct {
		name   string
		ctx    context.Context
		viewer agents.Viewer
		whole  []int64
		filter agents.TaskFilter
		listed []int64
	}{
		{name: "the assigner", ctx: context.Background(), viewer: f.mel, whole: []int64{3, 2, 1}, listed: []int64{3, 2, 1}},
		{name: "an owner", ctx: context.Background(), viewer: f.olive, whole: []int64{1}, listed: []int64{3, 2, 1}},
		{name: "an owner listing the repository's", ctx: context.Background(), viewer: f.olive, whole: []int64{1}, filter: agents.TaskFilter{RepoID: f.website}},
		{name: "a private task's token", ctx: token(true), viewer: f.bot, whole: []int64{3, 2, 1}, listed: []int64{3, 2, 1}},
		{name: "another task's token", ctx: token(false), viewer: f.bot, whole: []int64{1}, listed: []int64{3, 2, 1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			page, err := f.service.Tasks(tt.ctx, tt.viewer, f.org, tt.filter, 10)
			if err != nil || !slices.Equal(numbers(page.Tasks), tt.listed) {
				t.Fatalf("Tasks() = %v, %v; want %v", numbers(page.Tasks), err, tt.listed)
			}
			wholeOrRedacted(t, page.Tasks, tt.whole, scout.ID)
			for number := int64(1); number <= 3; number++ {
				_, err := f.service.Task(tt.ctx, tt.viewer, f.org, number)
				if found := slices.Contains(tt.whole, number); found != (err == nil) || (err != nil && !errors.Is(err, orgs.ErrNotFound)) {
					t.Errorf("Task(%d) = %v, want found %t", number, err, found)
				}
			}
		})
	}
}

// wholeOrRedacted checks that each of Scout's queued tasks listed is whole when whole numbers it, and redacted
// otherwise.
func wholeOrRedacted(t *testing.T, tasks []agents.Task, whole []int64, scout uuid.UUID) {
	t.Helper()
	for _, task := range tasks {
		wanted := slices.Contains(whole, task.Number)
		if redacted := task.Private && task.Title == "" && task.Instructions == "" && task.AssignedBy == nil && task.RepoID == uuid.Nil; redacted == wanted {
			t.Errorf("task %d = %+v; want whole %t", task.Number, task, wanted)
		}
		if task.Agent.ID != scout || task.State != agents.TaskQueued {
			t.Errorf("task %d = %+v; want Scout's, queued, in either case", task.Number, task)
		}
	}
}
