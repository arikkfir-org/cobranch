package agents_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/agents"
	"cobranch.dev/internal/services/orgs"
)

func TestMonthIn(t *testing.T) {
	t.Parallel()
	lateNovember := time.Date(2026, 11, 30, 23, 30, 0, 0, time.UTC)
	tests := []struct {
		name        string
		month, zone string
		want        agents.Month
		wantSince   string
		wantUntil   string
		wantErrCode string
	}{
		{name: "a month in UTC", month: "2026-11", zone: "UTC", want: agents.Month{Name: "2026-11", Zone: "UTC"},
			wantSince: "2026-11-01T00:00:00Z", wantUntil: "2026-12-01T00:00:00Z"},
		{name: "UTC by default", month: "2026-02", want: agents.Month{Name: "2026-02", Zone: "UTC"},
			wantSince: "2026-02-01T00:00:00Z", wantUntil: "2026-03-01T00:00:00Z"},
		{name: "a month in a zone east of UTC", month: "2026-12", zone: "Asia/Jerusalem", want: agents.Month{Name: "2026-12", Zone: "Asia/Jerusalem"},
			wantSince: "2026-11-30T22:00:00Z", wantUntil: "2026-12-31T22:00:00Z"},
		{name: "a month the clocks change in", month: "2026-03", zone: "Europe/London", want: agents.Month{Name: "2026-03", Zone: "Europe/London"},
			wantSince: "2026-03-01T00:00:00Z", wantUntil: "2026-03-31T23:00:00Z"},
		{name: "this month in UTC", want: agents.Month{Name: "2026-11", Zone: "UTC"},
			wantSince: "2026-11-01T00:00:00Z", wantUntil: "2026-12-01T00:00:00Z"},
		{name: "this month where it is already the next", zone: "Asia/Jerusalem", want: agents.Month{Name: "2026-12", Zone: "Asia/Jerusalem"},
			wantSince: "2026-11-30T22:00:00Z", wantUntil: "2026-12-31T22:00:00Z"},
		{name: "a thirteenth month", month: "2026-13", wantErrCode: "spend.month_invalid"},
		{name: "a month without its leading zero", month: "2026-1", wantErrCode: "spend.month_invalid"},
		{name: "a day", month: "2026-11-01", wantErrCode: "spend.month_invalid"},
		{name: "a two-digit year", month: "26-11", wantErrCode: "spend.month_invalid"},
		{name: "a zone that isn't one", month: "2026-11", zone: "Mars/Olympus", wantErrCode: "spend.zone_invalid"},
		{name: "the server's own zone", month: "2026-11", zone: "Local", wantErrCode: "spend.zone_invalid"},
	}
	for _, tt := range tests {
		got, err := agents.MonthIn(tt.month, tt.zone, lateNovember)
		if tt.wantErrCode != "" {
			if !is(err, invalid(tt.wantErrCode)) {
				t.Errorf("%s: MonthIn(%q, %q) = %v, want %s", tt.name, tt.month, tt.zone, err, tt.wantErrCode)
			}
			continue
		}
		if err != nil || got.Name != tt.want.Name || got.Zone != tt.want.Zone ||
			got.Since.UTC().Format(time.RFC3339) != tt.wantSince || got.Until.UTC().Format(time.RFC3339) != tt.wantUntil {
			t.Errorf("%s: MonthIn(%q, %q) = %+v, %v; want %s in %s from %s until %s", tt.name, tt.month, tt.zone, got, err,
				tt.want.Name, tt.want.Zone, tt.wantSince, tt.wantUntil)
		}
	}
}

// spendFixture is newFixture's organization with models a-model and b-model of anthropic, agent Scout on a-model,
// capped at $1.00 a task, and Quill on b-model, uncapped. In October Scout took Old, which spent $0.30 then and $0.03
// in November. In November Scout took Tidy up, which failed on its cap at $1.20, Patch the vault, on vault, which
// failed on its cap at $1.50, and Sweep, which succeeded without a call; Quill took Write, still running, which spent
// $0.03 on a-model and $0.005 on c-model, which the organization doesn't price, so Quill's b-model priced it.
func spendFixture(t *testing.T) (f *fixture, scout, quill agents.Agent, tasks map[string]agents.Task) {
	t.Helper()
	f = newFixture(t)
	f.store.AddModel(agents.Model{OrgID: f.org, Provider: "anthropic", Name: "a-model", InputPrice: 3_000_000, OutputPrice: 15_000_000})
	f.store.AddModel(agents.Model{OrgID: f.org, Provider: "anthropic", Name: "b-model", InputPrice: 1_000_000, OutputPrice: 5_000_000})
	scout = f.store.AddAgent(agents.Agent{OrgID: f.org, Name: "Scout", Provider: "anthropic", Model: "a-model", Cap: 1_000_000})
	quill = f.store.AddAgent(agents.Agent{OrgID: f.org, Name: "Quill", Provider: "anthropic", Model: "b-model"})
	tasks = map[string]agents.Task{}
	take := func(a agents.Agent, title string, repo uuid.UUID, calls map[string]int64) {
		task := f.assign(t, f.olive, a, title, repo)
		tasks[title] = task
		f.store.SetState(f.org, task.Number, agents.TaskRunning)
		for model, input := range calls {
			if _, err := f.service.RecordSpend(working(context.Background(), task), agentViewer(task), agents.Usage{ID: uuid.New(), Model: model, Input: input}); err != nil {
				t.Fatalf("RecordSpend(%s, %s) = %v", title, model, err)
			}
		}
	}
	f.store.Now = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	take(scout, "Old", uuid.Nil, map[string]int64{"a-model": 100_000})
	f.store.Now = now
	if _, err := f.service.RecordSpend(working(context.Background(), tasks["Old"]), agentViewer(tasks["Old"]), agents.Usage{ID: uuid.New(), Model: "a-model", Input: 10_000}); err != nil {
		t.Fatal(err)
	}
	take(scout, "Tidy up", uuid.Nil, map[string]int64{"a-model": 400_000})
	take(scout, "Patch the vault", f.vault, map[string]int64{"b-model": 1_500_000})
	take(scout, "Sweep", uuid.Nil, nil)
	f.store.SetState(f.org, tasks["Sweep"].Number, agents.TaskSucceeded)
	take(quill, "Write", uuid.Nil, map[string]int64{"a-model": 10_000, "c-model": 5_000})
	return f, scout, quill, tasks
}

func TestSpend(t *testing.T) {
	t.Parallel()
	f, scout, quill, tasks := spendFixture(t)
	november, october := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	principal := func(a agents.Agent) agents.Principal { return agents.Principal{ID: a.ID, DisplayName: a.Name} }
	costliest := func(title string, spent agents.USD) *agents.TaskCost {
		return &agents.TaskCost{Number: tasks[title].Number, Title: title, Spent: spent}
	}
	novemberScout := agents.AgentSpend{Agent: principal(scout), Provider: "anthropic", Model: "a-model", Cap: 1_000_000, Tasks: 3, FailedOnCap: 2,
		Spent: 2_730_000, Costliest: costliest("Patch the vault", 1_500_000)}
	novemberQuill := agents.AgentSpend{Agent: principal(quill), Provider: "anthropic", Model: "b-model", Tasks: 1, Active: 1, Spent: 35_000,
		Costliest: costliest("Write", 35_000)}
	novemberModels := []agents.ModelSpend{{Provider: "anthropic", Model: "a-model", Tasks: 3, Input: 420_000, Spent: 1_260_000},
		{Provider: "anthropic", Model: "b-model", Tasks: 1, Input: 1_500_000, Spent: 1_500_000},
		{Provider: "anthropic", Model: "c-model", Tasks: 1, Input: 5_000, Spent: 5_000}}
	unseen := novemberScout
	unseen.Costliest = costliest("Tidy up", 1_200_000)
	tests := []struct {
		name         string
		viewer       agents.Viewer
		since, until time.Time
		want         agents.Report
		wantErr      error
	}{
		{name: "an owner sees every task", viewer: f.olive, since: november, until: november.AddDate(0, 1, 0),
			want: agents.Report{Since: november, Until: november.AddDate(0, 1, 0), Agents: []agents.AgentSpend{novemberQuill, novemberScout}, Models: novemberModels}},
		{name: "a member counts what they don't read, but names only what they do", viewer: f.mel, since: november, until: november.AddDate(0, 1, 0),
			want: agents.Report{Since: november, Until: november.AddDate(0, 1, 0), Agents: []agents.AgentSpend{novemberQuill, unseen}, Models: novemberModels}},
		{name: "a month before", viewer: f.olive, since: october, until: november,
			want: agents.Report{Since: october, Until: november, Agents: []agents.AgentSpend{{Agent: principal(quill), Provider: "anthropic", Model: "b-model"},
				{Agent: principal(scout), Provider: "anthropic", Model: "a-model", Cap: 1_000_000, Tasks: 1, Active: 1, Spent: 300_000, Costliest: costliest("Old", 330_000)}},
				Models: []agents.ModelSpend{{Provider: "anthropic", Model: "a-model", Tasks: 1, Input: 100_000, Spent: 300_000}, {Provider: "anthropic", Model: "b-model"}}}},
		{name: "no member", viewer: f.nia, since: november, until: november.AddDate(0, 1, 0), wantErr: orgs.ErrNotFound},
		{name: "a span that ends before it starts", viewer: f.olive, since: november, until: october, wantErr: invalid("spend.span_invalid")},
		{name: "a span that ends as it starts", viewer: f.olive, since: november, until: november, wantErr: invalid("spend.span_invalid")},
		{name: "a span past a year", viewer: f.olive, since: october, until: october.Add(agents.MaxSpan + time.Hour), wantErr: invalid("spend.span_invalid")},
	}
	for _, tt := range tests {
		got, err := f.service.Spend(context.Background(), tt.viewer, f.org, tt.since, tt.until)
		if !is(err, tt.wantErr) {
			t.Fatalf("%s: Spend() = %v, want %v", tt.name, err, tt.wantErr)
		}
		if err == nil && !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s: Spend() =\n%+v\nwant\n%+v", tt.name, got, tt.want)
		}
	}
}

// TestSpendHidesPrivateTasks has Mel ask Scout for a private task that spends $0.60 and Olive assign it Look, which
// spends $0.30: the report counts both for everyone, names the private one Scout's costliest to Mel, who asked for it,
// and names Look to Olive, who owns the organization but may not see Mel's private task.
func TestSpendHidesPrivateTasks(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.store.AddModel(agents.Model{OrgID: f.org, Provider: "anthropic", Name: "a-model", InputPrice: 3_000_000, OutputPrice: 15_000_000})
	scout := f.store.AddAgent(agents.Agent{OrgID: f.org, Name: "Scout", Provider: "anthropic", Model: "a-model"})
	spend := func(task agents.Task, input int64) {
		f.store.SetState(f.org, task.Number, agents.TaskRunning)
		if _, err := f.service.RecordSpend(working(context.Background(), task), agentViewer(task), agents.Usage{ID: uuid.New(), Model: "a-model", Input: input}); err != nil {
			t.Fatal(err)
		}
	}
	asked, err := f.store.Assign(context.Background(), f.mel.ID, agents.Task{OrgID: f.org, Agent: agents.Principal{ID: scout.ID, DisplayName: "Scout"}, Kind: "chat",
		AssignedBy: &agents.Principal{ID: f.mel.ID}, Title: "Mel's own question", Private: true, State: agents.TaskQueued})
	if err != nil {
		t.Fatal(err)
	}
	spend(asked, 200_000)
	look := f.assign(t, f.olive, scout, "Look", uuid.Nil)
	spend(look, 100_000)
	for _, tt := range []struct {
		name   string
		viewer agents.Viewer
		want   *agents.TaskCost
	}{
		{name: "its asker sees it named", viewer: f.mel, want: &agents.TaskCost{Number: asked.Number, Title: "Mel's own question", Spent: 600_000}},
		{name: "an owner sees the costliest of the rest", viewer: f.olive, want: &agents.TaskCost{Number: look.Number, Title: "Look", Spent: 300_000}},
	} {
		got, err := f.service.Spend(context.Background(), tt.viewer, f.org, now.AddDate(0, 0, -1), now.AddDate(0, 0, 1))
		if err != nil {
			t.Fatalf("%s: Spend() = %v", tt.name, err)
		}
		if len(got.Agents) != 1 || got.Agents[0].Tasks != 2 || got.Agents[0].Spent != 900_000 || !reflect.DeepEqual(got.Agents[0].Costliest, tt.want) {
			t.Errorf("%s: Spend() agents = %+v, want Scout's 2 tasks, $0.90 and costliest %+v", tt.name, got.Agents, tt.want)
		}
	}
}
