package agents_test

import (
	"context"
	"math"
	"strings"
	"testing"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/agents"
	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/orgs"
)

func TestParseUSD(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in      string
		want    agents.USD
		wantErr bool
	}{
		{in: "8", want: 8_000_000},
		{in: "8.00", want: 8_000_000},
		{in: " $0.42 ", want: 420_000},
		{in: "0.000150", want: 150},
		{in: "0.000001", want: 1},
		{in: "1000000", want: agents.MaxCap},
		{in: "0", want: 0},
		{in: "0.0000001", wantErr: true},
		{in: "-1", wantErr: true},
		{in: "", wantErr: true},
		{in: ".5", wantErr: true},
		{in: "1.2.3", wantErr: true},
		{in: "1e3", wantErr: true},
		{in: "1,000", wantErr: true},
		{in: "1000000000000", wantErr: true},
	}
	for _, tt := range tests {
		got, err := agents.ParseUSD(tt.in)
		if (err != nil) != tt.wantErr || (err == nil && got != tt.want) {
			t.Errorf("ParseUSD(%q) = %d, %v; want %d, error %v", tt.in, got, err, tt.want, tt.wantErr)
		}
	}
}

func TestUSDString(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in   agents.USD
		want string
	}{
		{in: 0, want: "0.00"},
		{in: 8_000_000, want: "8.00"},
		{in: 420_000, want: "0.42"},
		{in: 150, want: "0.00015"},
		{in: 1, want: "0.000001"},
		{in: 12_345_678, want: "12.345678"},
		{in: -1_500_000, want: "-1.50"},
	}
	for _, tt := range tests {
		if got := tt.in.String(); got != tt.want {
			t.Errorf("USD(%d).String() = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestLowest(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		caps []agents.USD
		want agents.USD
	}{
		{name: "none", want: 0},
		{name: "none set", caps: []agents.USD{0, 0}, want: 0},
		{name: "the model's alone", caps: []agents.USD{0, 8_000_000}, want: 8_000_000},
		{name: "the agent's lower", caps: []agents.USD{2_000_000, 8_000_000}, want: 2_000_000},
		{name: "the model's lower", caps: []agents.USD{9_000_000, 8_000_000}, want: 8_000_000},
	}
	for _, tt := range tests {
		if got := agents.Lowest(tt.caps...); got != tt.want {
			t.Errorf("%s: Lowest(%v) = %d, want %d", tt.name, tt.caps, got, tt.want)
		}
	}
}

func TestCost(t *testing.T) {
	t.Parallel()
	priced := agents.Model{InputPrice: 3_000_000, OutputPrice: 15_000_000} // $3 and $15 a million tokens
	tests := []struct {
		name          string
		model         agents.Model
		input, output int64
		want          agents.USD
	}{
		{name: "nothing", model: priced, want: 0},
		{name: "a million of each", model: priced, input: 1_000_000, output: 1_000_000, want: 18_000_000},
		{name: "a call", model: priced, input: 12_000, output: 800, want: 48_000},
		{name: "a token rounds up", model: priced, input: 1, want: 3},
		{name: "a part of a millionth rounds up", model: agents.Model{InputPrice: 1}, input: 1, want: 1},
		{name: "unpriced", model: agents.Model{}, input: 1_000_000, output: 1_000_000, want: 0},
		{name: "the most", model: agents.Model{InputPrice: agents.MaxPrice, OutputPrice: agents.MaxPrice}, input: agents.MaxTokens,
			output: agents.MaxTokens, want: 2_000_000_000_000},
		{name: "past what an amount holds", model: agents.Model{InputPrice: math.MaxInt64}, input: math.MaxInt64, want: math.MaxInt64},
	}
	for _, tt := range tests {
		if got := tt.model.Cost(tt.input, tt.output); got != tt.want {
			t.Errorf("%s: Cost(%d, %d) = %d, want %d", tt.name, tt.input, tt.output, got, tt.want)
		}
	}
}

func TestEffectiveCap(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		agent agents.Agent
		want  agents.USD
	}{
		{name: "neither", agent: agents.Agent{}, want: 0},
		{name: "the model's", agent: agents.Agent{ModelCap: 8_000_000}, want: 8_000_000},
		{name: "the agent's own, lower", agent: agents.Agent{Cap: 2_000_000, ModelCap: 8_000_000}, want: 2_000_000},
		{name: "the agent's own on a model without one", agent: agents.Agent{Cap: 2_000_000}, want: 2_000_000},
		{name: "the model's, lower than the agent's", agent: agents.Agent{Cap: 9_000_000, ModelCap: 8_000_000}, want: 8_000_000},
	}
	for _, tt := range tests {
		if got := tt.agent.EffectiveCap(); got != tt.want {
			t.Errorf("%s: EffectiveCap() = %d, want %d", tt.name, got, tt.want)
		}
	}
}

func TestPutModel(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	key, _, _ := f.withProviders()
	ctx := context.Background()
	if _, _, err := f.service.SetModelProvider(ctx, f.olive, f.org, "anthropic", key, agents.NoVersion); err != nil {
		t.Fatal(err)
	}
	priced := agents.ModelInput{InputPrice: 3_000_000, OutputPrice: 15_000_000, Cap: 8_000_000}
	tests := []struct {
		name            string
		viewer          agents.Viewer
		provider, model string
		in              agents.ModelInput
		version         int64
		want            error
		wantCreated     bool
		wantVersion     int64
	}{
		{name: "add", viewer: f.olive, provider: " anthropic ", model: " a-model ", in: priced, version: agents.NoVersion, wantCreated: true, wantVersion: 1},
		{name: "add again", viewer: f.olive, provider: "anthropic", model: "a-model", in: priced, version: agents.NoVersion, want: agents.ErrChanged},
		{name: "change at its version", viewer: f.olive, provider: "anthropic", model: "a-model", in: priced, version: 1, wantVersion: 2},
		{name: "change from a stale version", viewer: f.olive, provider: "anthropic", model: "a-model", in: priced, version: 1, want: agents.ErrChanged},
		{name: "change at any version", viewer: f.olive, provider: "anthropic", model: "a-model", in: priced, version: agents.AnyVersion, wantVersion: 3},
		{name: "change one not added", viewer: f.olive, provider: "anthropic", model: "b-model", in: priced, version: agents.AnyVersion, want: agents.ErrChanged},
		{name: "a provider the organization hasn't added", viewer: f.olive, provider: "openai", model: "a-model", in: priced, version: agents.NoVersion,
			want: invalid("model.provider_invalid")},
		{name: "without a provider", viewer: f.olive, model: "a-model", in: priced, version: agents.NoVersion, want: invalid("model.name_invalid")},
		{name: "a name too long", viewer: f.olive, provider: "anthropic", model: strings.Repeat("m", agents.MaxModel+1), in: priced, version: agents.NoVersion,
			want: invalid("model.name_invalid")},
		{name: "without a name", viewer: f.olive, provider: "anthropic", in: priced, version: agents.NoVersion, want: invalid("model.name_invalid")},
		{name: "a price past the most", viewer: f.olive, provider: "anthropic", model: "b-model", in: agents.ModelInput{InputPrice: agents.MaxPrice + 1},
			version: agents.NoVersion, want: invalid("model.price_invalid")},
		{name: "a price below 0", viewer: f.olive, provider: "anthropic", model: "b-model", in: agents.ModelInput{OutputPrice: -1},
			version: agents.NoVersion, want: invalid("model.price_invalid")},
		{name: "a cap past the most", viewer: f.olive, provider: "anthropic", model: "b-model", in: agents.ModelInput{InputPrice: 1, Cap: agents.MaxCap + 1},
			version: agents.NoVersion, want: invalid("model.cap_invalid")},
		{name: "a cap without prices", viewer: f.olive, provider: "anthropic", model: "b-model", in: agents.ModelInput{Cap: 8_000_000},
			version: agents.NoVersion, want: invalid("model.cap_unpriced")},
		{name: "unpriced, without a cap", viewer: f.olive, provider: "anthropic", model: "b-model", version: agents.NoVersion, wantCreated: true, wantVersion: 1},
		{name: "as a steward, without org.manage", viewer: f.stu, provider: "anthropic", model: "c-model", in: priced, version: agents.NoVersion,
			want: auth.ErrForbidden},
		{name: "as an outsider", viewer: f.nia, provider: "anthropic", model: "c-model", in: priced, version: agents.NoVersion, want: orgs.ErrNotFound},
	}
	for _, tt := range tests {
		m, created, err := f.service.PutModel(ctx, tt.viewer, f.org, tt.provider, tt.model, tt.in, tt.version)
		if !is(err, tt.want) {
			t.Fatalf("%s: PutModel() = %v, want %v", tt.name, err, tt.want)
		}
		if err == nil && (created != tt.wantCreated || m.Version != tt.wantVersion || m.Provider != "anthropic" || m.InputPrice != tt.in.InputPrice ||
			m.Cap != tt.in.Cap) {
			t.Errorf("%s: PutModel() = %+v, %v", tt.name, m, created)
		}
	}
}

func TestDefaultModel(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	key, _, _ := f.withProviders()
	ctx := context.Background()
	if _, _, err := f.service.SetModelProvider(ctx, f.olive, f.org, "anthropic", key, agents.NoVersion); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a-model", "b-model"} {
		if _, _, err := f.service.PutModel(ctx, f.olive, f.org, "anthropic", name, agents.ModelInput{Default: true}, agents.NoVersion); err != nil {
			t.Fatal(err)
		}
	}
	list, err := f.service.Models(ctx, f.max, f.org)
	if err != nil || len(list) != 2 || list[0].Default || !list[1].Default {
		t.Fatalf("Models() = %+v, %v; want b-model alone the default", list, err)
	}
	scout := f.agent(t, f.mel, "Scout")
	if scout.Provider != "anthropic" || scout.Model != "b-model" {
		t.Errorf("a new agent runs on %s %s, want the default, anthropic b-model", scout.Provider, scout.Model)
	}
	m, err := f.service.Model(ctx, f.max, f.org, "anthropic", "b-model")
	if err != nil || len(m.Agents) != 1 || m.Agents[0].ID != scout.ID {
		t.Errorf("Model() = %+v, %v; want Scout on it", m, err)
	}
	if _, err := f.service.Model(ctx, f.nia, f.org, "anthropic", "b-model"); !is(err, orgs.ErrNotFound) {
		t.Errorf("Model() as an outsider = %v, want it not found", err)
	}
}

func TestRemoveModel(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	key, _, _ := f.withProviders()
	ctx := context.Background()
	if _, _, err := f.service.SetModelProvider(ctx, f.olive, f.org, "anthropic", key, agents.NoVersion); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a-model", "b-model"} {
		if _, _, err := f.service.PutModel(ctx, f.olive, f.org, "anthropic", name, agents.ModelInput{}, agents.NoVersion); err != nil {
			t.Fatal(err)
		}
	}
	scout := f.agent(t, f.olive, "Scout")
	if _, err := f.service.SetModel(ctx, f.olive, f.org, scout.ID, "anthropic", "a-model", agents.AnyVersion); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		viewer  agents.Viewer
		model   string
		version int64
		want    error
	}{
		{name: "as a member", viewer: f.mel, model: "b-model", version: agents.AnyVersion, want: auth.ErrForbidden},
		{name: "from a stale version", viewer: f.olive, model: "b-model", version: 2, want: agents.ErrChanged},
		{name: "one an agent runs on", viewer: f.olive, model: "a-model", version: agents.AnyVersion, want: agents.ErrModelInUse},
		{name: "at its version", viewer: f.olive, model: "b-model", version: 1},
		{name: "one not there", viewer: f.olive, model: "b-model", version: agents.AnyVersion, want: orgs.ErrNotFound},
	}
	for _, tt := range tests {
		if err := f.service.RemoveModel(ctx, tt.viewer, f.org, "anthropic", tt.model, tt.version); !is(err, tt.want) {
			t.Errorf("%s: RemoveModel() = %v, want %v", tt.name, err, tt.want)
		}
	}
}

func TestAgentCap(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	f.store.AddModel(agents.Model{OrgID: f.org, Provider: "anthropic", Name: "a-model", InputPrice: 1, Cap: 8_000_000, Default: true})
	scout := f.agent(t, f.olive, "Scout")
	cap := func(u agents.USD) *agents.USD { return &u }
	tests := []struct {
		name          string
		cap           *agents.USD
		want          error
		wantCap       agents.USD
		wantEffective agents.USD
	}{
		{name: "the model's alone", cap: nil, wantEffective: 8_000_000},
		{name: "a lower one of its own", cap: cap(2_000_000), wantCap: 2_000_000, wantEffective: 2_000_000},
		{name: "a higher one leaves the model's", cap: cap(9_000_000), wantCap: 9_000_000, wantEffective: 8_000_000},
		{name: "none of its own", cap: cap(0), wantEffective: 8_000_000},
		{name: "below 0", cap: cap(-1), want: invalid("agent.cap_invalid")},
		{name: "past the most", cap: cap(agents.MaxCap + 1), want: invalid("agent.cap_invalid")},
	}
	for _, tt := range tests {
		a, err := f.service.UpdateAgent(ctx, f.olive, f.org, scout.ID, agents.AnyVersion, agents.Change{Cap: tt.cap})
		if !is(err, tt.want) {
			t.Fatalf("%s: UpdateAgent() = %v, want %v", tt.name, err, tt.want)
		}
		if err == nil && (a.Cap != tt.wantCap || a.EffectiveCap() != tt.wantEffective) {
			t.Errorf("%s: UpdateAgent() = cap %d, effective %d; want %d, %d", tt.name, a.Cap, a.EffectiveCap(), tt.wantCap, tt.wantEffective)
		}
	}
}

func TestRecordSpend(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.store.AddModel(agents.Model{OrgID: f.org, Provider: "anthropic", Name: "a-model", InputPrice: 3_000_000, OutputPrice: 15_000_000,
		Cap: 1_000_000, Default: true})
	f.store.AddModel(agents.Model{OrgID: f.org, Provider: "anthropic", Name: "b-model", InputPrice: 1_000_000, OutputPrice: 5_000_000})
	scout := f.agent(t, f.olive, "Scout")
	task, other := f.assign(t, f.mel, scout, "Tidy up", uuid.Nil), f.assign(t, f.mel, scout, "Sweep", uuid.Nil)
	f.store.SetState(f.org, task.Number, agents.TaskRunning)
	mine, theirs := working(context.Background(), task), working(context.Background(), other)
	agent := agentViewer(task)
	first, second, third := uuid.New(), uuid.New(), uuid.New()
	tests := []struct {
		name      string
		ctx       context.Context
		viewer    agents.Viewer
		usage     agents.Usage
		want      error
		wantSpend agents.Spend
	}{
		{name: "a call", ctx: mine, viewer: agent, usage: agents.Usage{ID: first, Model: "a-model", Input: 100_000, Output: 10_000},
			wantSpend: agents.Spend{Spent: 450_000, Input: 100_000, Output: 10_000, Cap: 1_000_000}},
		{name: "a repeat counts once", ctx: mine, viewer: agent, usage: agents.Usage{ID: first, Model: "a-model", Input: 100_000, Output: 10_000},
			wantSpend: agents.Spend{Spent: 450_000, Input: 100_000, Output: 10_000, Cap: 1_000_000}},
		{name: "another model, at its prices", ctx: mine, viewer: agent, usage: agents.Usage{ID: second, Model: "b-model", Input: 100_000},
			wantSpend: agents.Spend{Spent: 550_000, Input: 200_000, Output: 10_000, Cap: 1_000_000}},
		{name: "without an ID", ctx: mine, viewer: agent, usage: agents.Usage{Model: "a-model", Input: 1}, want: invalid("spend.id_invalid")},
		{name: "without a model", ctx: mine, viewer: agent, usage: agents.Usage{ID: third, Input: 1}, want: invalid("spend.model_invalid")},
		{name: "tokens below 0", ctx: mine, viewer: agent, usage: agents.Usage{ID: third, Model: "a-model", Input: -1}, want: invalid("spend.tokens_invalid")},
		{name: "tokens past the most", ctx: mine, viewer: agent, usage: agents.Usage{ID: third, Model: "a-model", Output: agents.MaxTokens + 1},
			want: invalid("spend.tokens_invalid")},
		{name: "a person", ctx: context.Background(), viewer: f.mel, usage: agents.Usage{ID: third, Model: "a-model", Input: 1}, want: auth.ErrForbidden},
		{name: "the call that reaches the cap ends the task", ctx: mine, viewer: agent, usage: agents.Usage{ID: third, Model: "unknown", Input: 150_000},
			wantSpend: agents.Spend{Spent: 1_000_000, Input: 350_000, Output: 10_000, Cap: 1_000_000, Ended: true}},
		{name: "a call once it ended", ctx: mine, viewer: agent, usage: agents.Usage{ID: uuid.New(), Model: "a-model", Input: 1}, want: agents.ErrEnded},
		{name: "another task's token reports for its own", ctx: theirs, viewer: agent, usage: agents.Usage{ID: first, Model: "a-model", Input: 1},
			wantSpend: agents.Spend{Spent: 3, Input: 1, Cap: 1_000_000}},
	}
	for _, tt := range tests {
		got, err := f.service.RecordSpend(tt.ctx, tt.viewer, tt.usage)
		if !is(err, tt.want) {
			t.Fatalf("%s: RecordSpend() = %v, want %v", tt.name, err, tt.want)
		}
		if err == nil && got != tt.wantSpend {
			t.Errorf("%s: RecordSpend() = %+v, want %+v", tt.name, got, tt.wantSpend)
		}
	}
	ended, err := f.service.Task(context.Background(), f.mel, f.org, task.Number)
	if err != nil || ended.State != agents.TaskFailed || ended.Reason != "It spent $1.00, reaching its cap of $1.00 a task." || ended.Spent != 1_000_000 {
		t.Errorf("Task() = %+v, %v; want it failed at its cap", ended, err)
	}
}
