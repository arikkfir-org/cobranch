package postgres

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/agents"
)

// spendTasks has Scout of newTaskPodFixture take Look around, which spends $0.30 on test-model and $0.30 on
// other-model, priced at test-model's prices, Look again, on website, which fails on its cap at $1.20, and Look later,
// which stays queued; and makes Finder, on no model, which takes none.
func spendTasks(t *testing.T, f *taskPodFixture) (around, again, later agents.Task, finder agents.Agent) {
	t.Helper()
	run := func(title string, onFeature bool, calls ...agents.Usage) agents.Task {
		task := f.assigned(t, title, onFeature, uuid.Nil)
		f.claim(t, task.ID)
		if _, err := f.s.MarkRunning(context.Background(), task.ID); err != nil {
			t.Fatal(err)
		}
		for _, u := range calls {
			u.ID = uuid.New()
			if _, err := f.a.RecordSpend(context.Background(), task.ID, u); err != nil {
				t.Fatalf("RecordSpend(%s) = %v", title, err)
			}
		}
		return task
	}
	around = run("Look around", false, agents.Usage{Model: "test-model", Input: 100_000}, agents.Usage{Model: "other-model", Output: 20_000})
	again = run("Look again", true, agents.Usage{Model: "test-model", Input: 400_000})
	later = f.assigned(t, "Look later", false, uuid.Nil)
	return around, again, later, f.created(t, "Finder")
}

// spendStep is a report TestSpendStore asks for: its span, the repositories the viewer reads, the kinds hidden from
// them and whose private records they read, whether the calls and the task of moveBack move back, Scout's cap is
// raised to $5.00 and Look again becomes private first, and what Scout and the models should have spent.
type spendStep struct {
	name         string
	since, until time.Time
	readable     []uuid.UUID
	hidden       []string
	privateFor   uuid.UUID
	moved        bool
	raised       bool
	private      bool
	wantScout    agents.AgentSpend
	wantModels   []agents.ModelSpend
}

// spendSteps are TestSpendStore's steps over spendTasks' tasks, in order.
func spendSteps(f *taskPodFixture, around, again, later agents.Task) []spendStep {
	now := time.Now()
	hour, earlier := now.Add(-time.Hour), now.AddDate(0, 0, -40)
	scout := agents.AgentSpend{Agent: agents.Principal{ID: f.scout.ID, DisplayName: "Scout"}, Provider: "anthropic", Model: "test-model", Cap: 1_000_000,
		Tasks: 3, FailedOnCap: 1, Active: 2, Spent: 1_800_000, Costliest: &agents.TaskCost{Number: again.Number, Title: "Look again", Spent: 1_200_000}}
	unread, hidden := scout, scout
	unread.Costliest, hidden.Costliest = &agents.TaskCost{Number: around.Number, Title: "Look around", Spent: 600_000}, nil
	models := []agents.ModelSpend{{Provider: "anthropic", Model: "other-model", Tasks: 1, Output: 20_000, Spent: 300_000},
		{Provider: "anthropic", Model: "test-model", Tasks: 2, Input: 500_000, Spent: 1_500_000}}
	stayed := agents.AgentSpend{Agent: scout.Agent, Provider: "anthropic", Model: "test-model", Cap: 1_000_000, Tasks: 2, FailedOnCap: 1, Active: 1,
		Spent: 1_500_000, Costliest: scout.Costliest}
	raised := stayed
	raised.Cap = 5_000_000
	private := raised
	private.Costliest = &agents.TaskCost{Number: around.Number, Title: "Look around", Spent: 600_000}
	after := []agents.ModelSpend{{Provider: "anthropic", Model: "test-model", Tasks: 2, Input: 500_000, Spent: 1_500_000}}
	return []spendStep{
		{name: "every task the viewer reads", since: hour, until: now.Add(time.Hour), readable: []uuid.UUID{f.website.ID}, wantScout: scout, wantModels: models},
		{name: "the costliest on a repository the viewer doesn't read isn't named", since: hour, until: now.Add(time.Hour), wantScout: unread, wantModels: models},
		{name: "nor one of a hidden kind", since: hour, until: now.Add(time.Hour), readable: []uuid.UUID{f.website.ID}, hidden: []string{agents.KindAssigned},
			wantScout: hidden, wantModels: models},
		{name: "a span before them", since: earlier.Add(-time.Hour), until: earlier.Add(time.Hour),
			wantScout:  agents.AgentSpend{Agent: scout.Agent, Provider: "anthropic", Model: "test-model", Cap: 1_000_000},
			wantModels: []agents.ModelSpend{{Provider: "anthropic", Model: "test-model"}}},
		{name: "a call and a task moved back count there", since: earlier.Add(-time.Hour), until: earlier.Add(time.Hour), moved: true,
			wantScout: agents.AgentSpend{Agent: scout.Agent, Provider: "anthropic", Model: "test-model", Cap: 1_000_000, Tasks: 1, Active: 1, Spent: 300_000,
				Costliest: &agents.TaskCost{Number: later.Number, Title: "Look later"}},
			wantModels: []agents.ModelSpend{{Provider: "anthropic", Model: "other-model", Tasks: 1, Output: 20_000, Spent: 300_000}, {Provider: "anthropic", Model: "test-model"}}},
		{name: "and no longer here", since: hour, until: now.Add(time.Hour), readable: []uuid.UUID{f.website.ID}, moved: true, wantScout: stayed,
			wantModels: []agents.ModelSpend{{Provider: "anthropic", Model: "test-model", Tasks: 2, Input: 500_000, Spent: 1_500_000}}},
		{name: "a raised cap still counts the task that reached the old one", since: hour, until: now.Add(time.Hour), readable: []uuid.UUID{f.website.ID},
			raised: true, wantScout: raised, wantModels: after},
		{name: "another's private task counts but isn't named", since: hour, until: now.Add(time.Hour), readable: []uuid.UUID{f.website.ID},
			privateFor: uuid.New(), private: true, wantScout: private, wantModels: after},
		{name: "its assigner sees it named", since: hour, until: now.Add(time.Hour), readable: []uuid.UUID{f.website.ID}, privateFor: f.max,
			wantScout: raised, wantModels: after},
	}
}

func TestSpendStore(t *testing.T) {
	t.Parallel()
	f := newTaskPodFixture(t)
	ctx := context.Background()
	around, again, later, finder := spendTasks(t, f)
	for _, s := range spendSteps(f, around, again, later) {
		s.prepare(t, f, around, again, later)
		got, err := f.a.Spend(ctx, f.acme.ID, s.since, s.until, agents.Shown{Readable: s.readable, Hidden: append([]string{}, s.hidden...), PrivateFor: s.privateFor})
		if err != nil {
			t.Fatalf("%s: Spend() = %v", s.name, err)
		}
		bySpender := map[uuid.UUID]agents.AgentSpend{}
		for _, a := range got.Agents {
			bySpender[a.Agent.ID] = a
		}
		if !reflect.DeepEqual(bySpender[f.scout.ID], s.wantScout) || !reflect.DeepEqual(got.Models, s.wantModels) ||
			!reflect.DeepEqual(bySpender[finder.ID], agents.AgentSpend{Agent: agents.Principal{ID: finder.ID, DisplayName: "Finder"}}) {
			t.Errorf("%s: Spend() =\n%+v\nwant Scout %+v, Finder idle and models %+v", s.name, got, s.wantScout, s.wantModels)
		}
		for i := 1; i < len(got.Agents); i++ {
			if got.Agents[i-1].Agent.DisplayName > got.Agents[i].Agent.DisplayName {
				t.Errorf("%s: Spend() lists %s before %s, want them by name", s.name, got.Agents[i-1].Agent.DisplayName, got.Agents[i].Agent.DisplayName)
			}
		}
	}
}

// prepare changes the tasks of spendTasks as step s says, before its report: once done, each change stays.
func (s spendStep) prepare(t *testing.T, f *taskPodFixture, around, again, later agents.Task) {
	t.Helper()
	ctx := context.Background()
	if s.moved {
		moveBack(t, f, around.ID, later.ID)
	}
	if s.raised {
		if _, err := f.pool.Exec(ctx, "UPDATE agents SET cap = 5000000 WHERE principal_id = $1", f.scout.ID); err != nil {
			t.Fatal(err)
		}
	}
	if s.private {
		if _, err := f.pool.Exec(ctx, "UPDATE agent_tasks SET private = true WHERE id = $1", again.ID); err != nil {
			t.Fatal(err)
		}
	}
}

// moveBack moves the call of other-model of task callOf, and task queuedOf, 40 days back, once.
func moveBack(t *testing.T, f *taskPodFixture, callOf, queuedOf uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	if _, err := f.pool.Exec(ctx, "UPDATE task_spend SET created_at = now() - interval '40 days' WHERE task_id = $1 AND model = 'other-model' AND created_at > now() - interval '1 day'", callOf); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, "UPDATE agent_tasks SET created_at = now() - interval '40 days' WHERE id = $1 AND created_at > now() - interval '1 day'", queuedOf); err != nil {
		t.Fatal(err)
	}
}
