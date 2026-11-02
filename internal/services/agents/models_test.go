package agents_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/agents"
	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/orgs"
)

// withProviders gives the fixture's service the providers the installation knows, and acme a secret of its own, one
// of website's and one of another organization's.
func (f *fixture) withProviders() (key, repoKey, foreign uuid.UUID) {
	f.service.WithProviders("openai", "anthropic")
	return f.store.AddSecret(f.org, uuid.Nil, "anthropic-key"), f.store.AddSecret(f.org, f.website, "deploy-key"), f.store.AddSecret(uuid.New(), uuid.Nil, "theirs")
}

func TestSetModelProvider(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	key, repoKey, foreign := f.withProviders()
	ctx := context.Background()
	if got := f.service.ProviderNames(); fmt.Sprint(got) != "[anthropic openai]" {
		t.Errorf("ProviderNames() = %v", got)
	}
	tests := []struct {
		name        string
		viewer      agents.Viewer
		provider    string
		secret      uuid.UUID
		version     int64
		want        error
		wantCreated bool
		wantVersion int64
	}{
		{name: "add", viewer: f.olive, provider: "anthropic", secret: key, version: agents.NoVersion, wantCreated: true, wantVersion: 1},
		{name: "add again", viewer: f.olive, provider: "anthropic", secret: key, version: agents.NoVersion, want: agents.ErrChanged},
		{name: "change at its version", viewer: f.olive, provider: "anthropic", secret: key, version: 1, wantVersion: 2},
		{name: "change from a stale version", viewer: f.olive, provider: "anthropic", secret: key, version: 1, want: agents.ErrChanged},
		{name: "change at any version", viewer: f.olive, provider: "anthropic", secret: key, version: agents.AnyVersion, wantVersion: 3},
		{name: "change one not added", viewer: f.olive, provider: "openai", secret: key, version: agents.AnyVersion, want: agents.ErrChanged},
		{name: "a provider the installation doesn't know", viewer: f.olive, provider: "acme-ai", secret: key, version: agents.NoVersion, want: orgs.ErrNotFound},
		{name: "a repository's secret", viewer: f.olive, provider: "openai", secret: repoKey, version: agents.NoVersion, want: invalid("model_provider.secret_invalid")},
		{name: "another organization's secret", viewer: f.olive, provider: "openai", secret: foreign, version: agents.NoVersion, want: invalid("model_provider.secret_invalid")},
		{name: "as a steward, without org.manage", viewer: f.stu, provider: "openai", secret: key, version: agents.NoVersion, want: auth.ErrForbidden},
		{name: "as an outsider", viewer: f.nia, provider: "openai", secret: key, version: agents.NoVersion, want: orgs.ErrNotFound},
	}
	for _, tt := range tests {
		p, created, err := f.service.SetModelProvider(ctx, tt.viewer, f.org, tt.provider, tt.secret, tt.version)
		if !is(err, tt.want) {
			t.Fatalf("%s: SetModelProvider() = %v, want %v", tt.name, err, tt.want)
		}
		if err == nil && (created != tt.wantCreated || p.Version != tt.wantVersion || p.SecretID != key || p.SecretName != "anthropic-key") {
			t.Errorf("%s: SetModelProvider() = %+v, %v", tt.name, p, created)
		}
	}
}

func TestModelProviders(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	key, _, _ := f.withProviders()
	ctx := context.Background()
	if _, _, err := f.service.SetModelProvider(ctx, f.olive, f.org, "anthropic", key, agents.NoVersion); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name       string
		viewer     agents.Viewer
		want       error
		wantSecret string
	}{
		{name: "an owner sees the secret's name", viewer: f.olive, wantSecret: "anthropic-key"},
		{name: "a member sees the provider alone", viewer: f.max},
		{name: "an outsider sees nothing", viewer: f.nia, want: orgs.ErrNotFound},
	}
	for _, tt := range tests {
		list, err := f.service.ModelProviders(ctx, tt.viewer, f.org)
		if !is(err, tt.want) {
			t.Fatalf("%s: ModelProviders() = %v, want %v", tt.name, err, tt.want)
		}
		one, oneErr := f.service.ModelProvider(ctx, tt.viewer, f.org, "anthropic")
		if !is(oneErr, tt.want) {
			t.Fatalf("%s: ModelProvider() = %v, want %v", tt.name, oneErr, tt.want)
		}
		if err == nil && (len(list) != 1 || list[0].SecretName != tt.wantSecret || one.SecretName != tt.wantSecret || list[0].Broken()) {
			t.Errorf("%s: ModelProviders() = %+v, ModelProvider() = %+v", tt.name, list, one)
		}
	}
	if _, err := f.service.ModelProvider(ctx, f.olive, f.org, "openai"); !is(err, orgs.ErrNotFound) {
		t.Errorf("ModelProvider(openai) = %v, want it not found", err)
	}
	f.store.DeleteSecret(key)
	if p, err := f.service.ModelProvider(ctx, f.olive, f.org, "anthropic"); err != nil || !p.Broken() {
		t.Errorf("ModelProvider() after its secret went = %+v, %v; want it broken", p, err)
	}
}

func TestRemoveModelProvider(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	key, _, _ := f.withProviders()
	ctx := context.Background()
	for _, name := range []string{"anthropic", "openai"} {
		if _, _, err := f.service.SetModelProvider(ctx, f.olive, f.org, name, key, agents.NoVersion); err != nil {
			t.Fatal(err)
		}
	}
	scout := f.agent(t, f.olive, "Scout")
	if _, err := f.service.SetModel(ctx, f.olive, f.org, scout.ID, "anthropic", "a-model", agents.AnyVersion); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name     string
		viewer   agents.Viewer
		provider string
		version  int64
		want     error
	}{
		{name: "as a member", viewer: f.mel, provider: "openai", version: agents.AnyVersion, want: auth.ErrForbidden},
		{name: "from a stale version", viewer: f.olive, provider: "openai", version: 2, want: agents.ErrChanged},
		{name: "one an agent runs on", viewer: f.olive, provider: "anthropic", version: agents.AnyVersion, want: agents.ErrProviderInUse},
		{name: "at its version", viewer: f.olive, provider: "openai", version: 1},
		{name: "one not there", viewer: f.olive, provider: "openai", version: agents.AnyVersion, want: orgs.ErrNotFound},
	}
	for _, tt := range tests {
		if err := f.service.RemoveModelProvider(ctx, tt.viewer, f.org, tt.provider, tt.version); !is(err, tt.want) {
			t.Errorf("%s: RemoveModelProvider() = %v, want %v", tt.name, err, tt.want)
		}
	}
}

func TestSetModel(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	key, _, _ := f.withProviders()
	ctx := context.Background()
	if _, _, err := f.service.SetModelProvider(ctx, f.olive, f.org, "anthropic", key, agents.NoVersion); err != nil {
		t.Fatal(err)
	}
	scout := f.agent(t, f.mel, "Scout")
	long := fmt.Sprintf("%0201d", 0)
	tests := []struct {
		name                    string
		viewer                  agents.Viewer
		provider, model         string
		version                 int64
		want                    error
		wantProvider, wantModel string
	}{
		{name: "as its creator, a member", viewer: f.mel, provider: "anthropic", model: "a-model", version: 1, want: auth.ErrForbidden},
		{name: "as an agent", viewer: f.bot, provider: "anthropic", model: "a-model", version: 1, want: auth.ErrForbidden},
		{name: "a provider the organization hasn't added", viewer: f.stu, provider: "openai", model: "a-model", version: 1, want: invalid("agent.provider_invalid")},
		{name: "a model without a provider", viewer: f.stu, model: "a-model", version: 1, want: invalid("agent.model_invalid")},
		{name: "a model's name too long", viewer: f.stu, provider: "anthropic", model: long, version: 1, want: invalid("agent.model_invalid")},
		{name: "as a steward", viewer: f.stu, provider: " anthropic ", model: " a-model ", version: 1, wantProvider: "anthropic", wantModel: "a-model"},
		{name: "from a stale version", viewer: f.stu, provider: "anthropic", model: "b-model", version: 1, want: agents.ErrChanged},
		{name: "none", viewer: f.olive, version: agents.AnyVersion},
	}
	for _, tt := range tests {
		a, err := f.service.SetModel(ctx, tt.viewer, f.org, scout.ID, tt.provider, tt.model, tt.version)
		if !is(err, tt.want) {
			t.Fatalf("%s: SetModel() = %v, want %v", tt.name, err, tt.want)
		}
		if err == nil && (a.Provider != tt.wantProvider || a.Model != tt.wantModel) {
			t.Errorf("%s: SetModel() = %q %q", tt.name, a.Provider, a.Model)
		}
	}
}

func TestSetSecrets(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	key, repoKey, foreign := f.withProviders()
	ctx := context.Background()
	scout := f.agent(t, f.olive, "Scout")
	many := map[string]uuid.UUID{}
	for i := range agents.MaxSecrets + 1 {
		many[fmt.Sprintf("KEY_%d", i)] = key
	}
	tests := []struct {
		name   string
		viewer agents.Viewer
		named  map[string]uuid.UUID
		want   error
	}{
		{name: "as a member", viewer: f.mel, named: map[string]uuid.UUID{"TOKEN": key}, want: auth.ErrForbidden},
		{name: "as an agent", viewer: f.bot, named: map[string]uuid.UUID{"TOKEN": key}, want: auth.ErrForbidden},
		{name: "not a variable's name", viewer: f.stu, named: map[string]uuid.UUID{"1TOKEN": key}, want: invalid("agent.secrets_invalid")},
		{name: "a variable the harness sets", viewer: f.stu, named: map[string]uuid.UUID{"Path": key}, want: invalid("agent.secrets_invalid")},
		{name: "a prefix the harness sets", viewer: f.stu, named: map[string]uuid.UUID{"git_dir": key}, want: invalid("agent.secrets_invalid")},
		{name: "Cobranch's own", viewer: f.stu, named: map[string]uuid.UUID{"COBRANCH_TOKEN": key}, want: invalid("agent.secrets_invalid")},
		{name: "too many", viewer: f.stu, named: many, want: invalid("agent.secrets_invalid")},
		{name: "another organization's secret", viewer: f.stu, named: map[string]uuid.UUID{"TOKEN": foreign}, want: invalid("agent.secrets_invalid")},
		{name: "the organization's and a repository's", viewer: f.stu, named: map[string]uuid.UUID{"TOKEN": key, "DEPLOY_KEY": repoKey}},
		{name: "from a stale version", viewer: f.stu, named: map[string]uuid.UUID{}, want: agents.ErrChanged},
	}
	for _, tt := range tests {
		a, err := f.service.SetSecrets(ctx, tt.viewer, f.org, scout.ID, tt.named, 1)
		if !is(err, tt.want) {
			t.Fatalf("%s: SetSecrets() = %v, want %v", tt.name, err, tt.want)
		}
		if err == nil && (len(a.Secrets) != 2 || a.Secrets["DEPLOY_KEY"] != repoKey || a.Version != 2) {
			t.Errorf("%s: SetSecrets() = %+v", tt.name, a)
		}
	}
}
