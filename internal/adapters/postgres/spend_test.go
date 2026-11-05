package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/agents"
	"cobranch.dev/internal/services/orgs"
)

func TestModelStore(t *testing.T) {
	t.Parallel()
	f := newAgentFixture(t)
	ctx := context.Background()
	if _, _, err := f.a.SetModelProvider(ctx, f.olivia, f.acme.ID, "anthropic", f.addSecret(t, uuid.Nil, "key"), agents.NoVersion); err != nil {
		t.Fatal(err)
	}
	priced := agents.ModelInput{InputPrice: 3_000_000, OutputPrice: 15_000_000, Cap: 8_000_000, Default: true}
	steps := []struct {
		name        string
		provider    string
		model       string
		in          agents.ModelInput
		version     int64
		want        error
		wantCode    string
		wantCreated bool
		wantVersion int64
	}{
		{name: "add", provider: "anthropic", model: "a-model", in: priced, version: agents.NoVersion, wantCreated: true, wantVersion: 1},
		{name: "add again", provider: "anthropic", model: "a-model", in: priced, version: agents.NoVersion, want: agents.ErrChanged},
		{name: "a provider not added", provider: "openai", model: "a-model", in: priced, version: agents.NoVersion, wantCode: "model.provider_invalid"},
		{name: "change at its version", provider: "anthropic", model: "a-model", in: agents.ModelInput{InputPrice: 1, Default: true}, version: 1, wantVersion: 2},
		{name: "change from a stale version", provider: "anthropic", model: "a-model", in: priced, version: 1, want: agents.ErrChanged},
		{name: "change at any version", provider: "anthropic", model: "a-model", in: priced, version: agents.AnyVersion, wantVersion: 3},
		{name: "change one not added", provider: "anthropic", model: "b-model", in: priced, version: agents.AnyVersion, want: agents.ErrChanged},
		{name: "another default", provider: "anthropic", model: "b-model", in: agents.ModelInput{Default: true}, version: agents.NoVersion,
			wantCreated: true, wantVersion: 1},
	}
	for _, s := range steps {
		m, created, err := f.a.PutModel(ctx, f.olivia, f.acme.ID, s.provider, s.model, s.in, s.version)
		var verr *orgs.ValidationError
		switch {
		case s.wantCode != "":
			if !errors.As(err, &verr) || verr.Code != s.wantCode {
				t.Fatalf("%s: PutModel() = %v, want %s", s.name, err, s.wantCode)
			}
		case !errors.Is(err, s.want):
			t.Fatalf("%s: PutModel() = %v, want %v", s.name, err, s.want)
		case err == nil && (created != s.wantCreated || m.Version != s.wantVersion || m.InputPrice != s.in.InputPrice ||
			m.OutputPrice != s.in.OutputPrice || m.Cap != s.in.Cap || m.Default != s.in.Default || m.CreatedBy == nil || m.CreatedBy.ID != f.olivia):
			t.Errorf("%s: PutModel() = %+v, %v", s.name, m, created)
		}
	}
	finder := f.created(t, "Finder")
	if finder.Provider != "anthropic" || finder.Model != "b-model" {
		t.Errorf("a new agent runs on %q %q, want the default, anthropic b-model", finder.Provider, finder.Model)
	}
	list, err := f.a.Models(ctx, f.acme.ID)
	if err != nil || len(list) != 2 || list[0].Name != "a-model" || list[0].Default || list[0].Cap != 8_000_000 || len(list[0].Agents) != 0 ||
		list[1].Name != "b-model" || !list[1].Default || len(list[1].Agents) != 1 || list[1].Agents[0].ID != finder.ID {
		t.Fatalf("Models() = %+v, %v; want a-model, and b-model the default with Finder on it", list, err)
	}
	if err := f.a.RemoveModel(ctx, f.olivia, f.acme.ID, "anthropic", "b-model", agents.AnyVersion); !errors.Is(err, agents.ErrModelInUse) {
		t.Errorf("RemoveModel() while Finder runs on it = %v, want %v", err, agents.ErrModelInUse)
	}
	if err := f.a.RemoveModel(ctx, f.olivia, f.acme.ID, "anthropic", "a-model", 2); !errors.Is(err, agents.ErrChanged) {
		t.Errorf("RemoveModel() from a stale version = %v, want %v", err, agents.ErrChanged)
	}
	// b-model took the default from a-model, which changed it.
	if err := f.a.RemoveModel(ctx, f.olivia, f.acme.ID, "anthropic", "a-model", 4); err != nil {
		t.Fatalf("RemoveModel() = %v", err)
	}
	if _, err := f.a.Model(ctx, f.acme.ID, "anthropic", "a-model"); !errors.Is(err, orgs.ErrNotFound) {
		t.Errorf("Model() once removed = %v, want %v", err, orgs.ErrNotFound)
	}
	if n := count(t, f.pool, "SELECT count(*) FROM events WHERE kind LIKE 'model.%' AND org_id = $1", f.acme.ID); n != 5 {
		t.Errorf("model events = %d, want 5", n)
	}
	if n := count(t, f.pool, "SELECT count(*) FROM audit_log WHERE table_name = 'models' AND principal_id = $1", f.olivia); n == 0 {
		t.Error("the audit log has no change of the models in Olivia's name")
	}
}

func TestAgentCapStore(t *testing.T) {
	t.Parallel()
	f := newTaskPodFixture(t)
	ctx := context.Background()
	if f.scout, _ = f.a.Agent(ctx, f.acme.ID, f.scout.ID); f.scout.ModelCap != 1_000_000 || f.scout.Cap != 0 || f.scout.EffectiveCap() != 1_000_000 {
		t.Fatalf("Agent() = cap %d of its model's %d, want none of its own under $1", f.scout.Cap, f.scout.ModelCap)
	}
	task := f.assigned(t, "Look around", false, uuid.Nil)
	steps := []struct {
		name    string
		cap     *agents.USD
		wantCap agents.USD
	}{
		{name: "a lower one of its own", cap: new(agents.USD(250_000)), wantCap: 250_000},
		{name: "a name alone leaves it", wantCap: 250_000},
		{name: "none of its own", cap: new(agents.USD(0)), wantCap: 0},
	}
	for _, s := range steps {
		name := "Scout"
		a, err := f.a.UpdateAgent(ctx, f.olivia, f.acme.ID, f.scout.ID, agents.AnyVersion, agents.Change{Name: &name, Cap: s.cap})
		if err != nil || a.Cap != s.wantCap || a.ModelCap != 1_000_000 {
			t.Fatalf("%s: UpdateAgent() = cap %d of its model's %d, %v; want %d", s.name, a.Cap, a.ModelCap, err, s.wantCap)
		}
		got, err := f.a.Task(ctx, f.acme.ID, task.Number)
		if want := agents.Lowest(s.wantCap, 1_000_000); err != nil || got.Cap != want {
			t.Errorf("%s: Task() = cap %d, %v; want %d", s.name, got.Cap, err, want)
		}
	}
}

func TestRecordSpendStore(t *testing.T) {
	t.Parallel()
	f := newTaskPodFixture(t)
	ctx := context.Background()
	task := f.assigned(t, "Look around", false, uuid.Nil)
	f.claim(t, task.ID)
	if _, err := f.s.MarkRunning(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.a.PutModel(ctx, f.olivia, f.acme.ID, "anthropic", "small-model", agents.ModelInput{InputPrice: 1_000_000}, agents.NoVersion); err != nil {
		t.Fatal(err)
	}
	first := uuid.New()
	steps := []struct {
		name  string
		task  uuid.UUID
		usage agents.Usage
		want  error
		spend agents.Spend
	}{
		{name: "a call", task: task.ID, usage: agents.Usage{ID: first, Model: "test-model", Input: 100_000, Output: 10_000},
			spend: agents.Spend{Spent: 450_000, Input: 100_000, Output: 10_000, Cap: 1_000_000}},
		{name: "a repeat counts once", task: task.ID, usage: agents.Usage{ID: first, Model: "test-model", Input: 100_000, Output: 10_000},
			spend: agents.Spend{Spent: 450_000, Input: 100_000, Output: 10_000, Cap: 1_000_000}},
		{name: "another model of the organization's, at its prices", task: task.ID, usage: agents.Usage{ID: uuid.New(), Model: "small-model", Input: 50_000},
			spend: agents.Spend{Spent: 500_000, Input: 150_000, Output: 10_000, Cap: 1_000_000}},
		{name: "a model it hasn't added, at its agent's model's", task: task.ID, usage: agents.Usage{ID: uuid.New(), Model: "other-model", Output: 20_000},
			spend: agents.Spend{Spent: 800_000, Input: 150_000, Output: 30_000, Cap: 1_000_000}},
		{name: "the call that reaches the cap ends the task", task: task.ID, usage: agents.Usage{ID: uuid.New(), Model: "test-model", Input: 70_000},
			spend: agents.Spend{Spent: 1_010_000, Input: 220_000, Output: 30_000, Cap: 1_000_000, Ended: true}},
		{name: "a call once it ended", task: task.ID, usage: agents.Usage{ID: uuid.New(), Model: "test-model", Input: 1}, want: agents.ErrEnded},
		{name: "a task that isn't there", task: uuid.New(), usage: agents.Usage{ID: uuid.New(), Model: "test-model", Input: 1}, want: orgs.ErrNotFound},
	}
	for _, s := range steps {
		got, err := f.a.RecordSpend(ctx, s.task, s.usage)
		if !errors.Is(err, s.want) || (err == nil && got != s.spend) {
			t.Fatalf("%s: RecordSpend() = %+v, %v; want %+v, %v", s.name, got, err, s.spend, s.want)
		}
	}
	ended, err := f.a.Task(ctx, f.acme.ID, task.Number)
	if err != nil || ended.State != agents.TaskFailed || ended.Reason != "It spent $1.01, reaching its cap of $1.00 a task." ||
		ended.Spent != 1_010_000 || ended.InputTokens != 220_000 || ended.OutputTokens != 30_000 || ended.Cap != 1_000_000 {
		t.Errorf("Task() = %+v, %v; want it failed at its cap, with what it spent", ended, err)
	}
	entries, err := f.a.Entries(ctx, task.ID, 0, 10)
	if err != nil || len(entries) == 0 {
		t.Fatalf("Entries() = %+v, %v", entries, err)
	}
	var change agents.StateChange
	last := entries[len(entries)-1]
	if err := json.Unmarshal(last.Body, &change); err != nil || last.Kind != agents.EntryState || change.State != agents.TaskFailed || change.Reason != ended.Reason {
		t.Errorf("the transcript ends %+v, want the failure", last)
	}
	if n := count(t, f.pool, "SELECT count(*) FROM events WHERE kind = 'task.failed' AND subject_id = $1 AND actor_id = $2", task.ID, f.scout.ID); n != 1 {
		t.Errorf("task.failed events in Scout's name = %d, want 1", n)
	}
	if n := count(t, f.pool, "SELECT count(*) FROM task_spend WHERE task_id = $1", task.ID); n != 4 {
		t.Errorf("calls recorded = %d, want 4", n)
	}
}
