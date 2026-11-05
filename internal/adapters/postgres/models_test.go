package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/agents"
	"cobranch.dev/internal/services/orgs"
	"cobranch.dev/internal/services/secrets"
)

// addSecret adds one of Acme's secrets named name, of the repository repoID or Acme's own with uuid.Nil.
func (f *agentFixture) addSecret(t *testing.T, repoID uuid.UUID, name string) uuid.UUID {
	t.Helper()
	s, err := NewSecretStore(f.pool).Create(context.Background(), f.olivia, secrets.Secret{ID: uuid.New(), OrgID: f.acme.ID, RepoID: repoID, Name: name}, []byte("sealed"))
	if err != nil {
		t.Fatal(err)
	}
	return s.ID
}

func TestModelProviderStore(t *testing.T) {
	t.Parallel()
	f := newAgentFixture(t)
	ctx := context.Background()
	key, other := f.addSecret(t, uuid.Nil, "anthropic-key"), f.addSecret(t, uuid.Nil, "spare-key")
	steps := []struct {
		name        string
		secret      uuid.UUID
		version     int64
		want        error
		wantCreated bool
		wantVersion int64
		wantSecret  string
	}{
		{name: "add", secret: key, version: agents.NoVersion, wantCreated: true, wantVersion: 1, wantSecret: "anthropic-key"},
		{name: "add again", secret: key, version: agents.NoVersion, want: agents.ErrChanged},
		{name: "change at its version", secret: other, version: 1, wantVersion: 2, wantSecret: "spare-key"},
		{name: "change from a stale version", secret: key, version: 1, want: agents.ErrChanged},
		{name: "change at any version", secret: key, version: agents.AnyVersion, wantVersion: 3, wantSecret: "anthropic-key"},
	}
	for _, s := range steps {
		p, created, err := f.a.SetModelProvider(ctx, f.olivia, f.acme.ID, "anthropic", s.secret, s.version)
		if !errors.Is(err, s.want) {
			t.Fatalf("%s: SetModelProvider() = %v, want %v", s.name, err, s.want)
		}
		if err == nil && (created != s.wantCreated || p.Version != s.wantVersion || p.SecretName != s.wantSecret || p.CreatedBy == nil || p.CreatedBy.ID != f.olivia) {
			t.Errorf("%s: SetModelProvider() = %+v, %v", s.name, p, created)
		}
	}
	if _, _, err := f.a.SetModelProvider(ctx, f.olivia, f.acme.ID, "openai", key, agents.AnyVersion); !errors.Is(err, agents.ErrChanged) {
		t.Errorf("SetModelProvider(openai, any version) = %v, want %v for one not added", err, agents.ErrChanged)
	}
	if n := count(t, f.pool, "SELECT count(*) FROM audit_log WHERE table_name = 'model_providers' AND principal_id = $1", f.olivia); n != 3 {
		t.Errorf("model_providers audit rows = %d, want 3", n)
	}
	if n := count(t, f.pool, "SELECT count(*) FROM events WHERE kind IN ('model_provider.added', 'model_provider.changed') AND org_id = $1", f.acme.ID); n != 3 {
		t.Errorf("model provider events = %d, want 3", n)
	}
	if err := NewSecretStore(f.pool).Delete(ctx, f.olivia, key, secrets.AnyVersion); err != nil {
		t.Fatal(err)
	}
	list, err := f.a.ModelProviders(ctx, f.acme.ID)
	if err != nil || len(list) != 1 || !list[0].Broken() || list[0].SecretName != "" {
		t.Errorf("ModelProviders() after its secret went = %+v, %v; want it broken", list, err)
	}
	if _, err := f.a.ModelProvider(ctx, f.acme.ID, "openai"); !errors.Is(err, orgs.ErrNotFound) {
		t.Errorf("ModelProvider(openai) = %v, want %v", err, orgs.ErrNotFound)
	}
}

func TestAgentModelStore(t *testing.T) {
	t.Parallel()
	f := newAgentFixture(t)
	ctx := context.Background()
	if _, _, err := f.a.SetModelProvider(ctx, f.olivia, f.acme.ID, "anthropic", f.addSecret(t, uuid.Nil, "key"), agents.NoVersion); err != nil {
		t.Fatal(err)
	}
	var verr *orgs.ValidationError
	if _, err := f.a.SetModel(ctx, f.olivia, f.acme.ID, f.scout.ID, 1, "openai", "a-model"); !errors.As(err, &verr) || verr.Code != "agent.provider_invalid" {
		t.Errorf("SetModel(openai) = %v, want agent.provider_invalid", err)
	}
	if _, err := f.a.SetModel(ctx, f.olivia, f.acme.ID, f.scout.ID, 1, "anthropic", "a-model"); !errors.As(err, &verr) || verr.Code != "agent.model_invalid" {
		t.Errorf("SetModel() of a model the organization hasn't added = %v, want agent.model_invalid", err)
	}
	if _, _, err := f.a.PutModel(ctx, f.olivia, f.acme.ID, "anthropic", "a-model", agents.ModelInput{}, agents.NoVersion); err != nil {
		t.Fatal(err)
	}
	a, err := f.a.SetModel(ctx, f.olivia, f.acme.ID, f.scout.ID, 1, "anthropic", "a-model")
	if err != nil || a.Provider != "anthropic" || a.Model != "a-model" || a.Version != 2 || len(a.Grants) != 2 {
		t.Fatalf("SetModel() = %+v, %v", a, err)
	}
	if _, err := f.a.SetModel(ctx, f.olivia, f.acme.ID, f.scout.ID, 1, "", ""); !errors.Is(err, agents.ErrChanged) {
		t.Errorf("SetModel() from a stale version = %v, want %v", err, agents.ErrChanged)
	}
	if err := f.a.RemoveModelProvider(ctx, f.olivia, f.acme.ID, "anthropic", agents.AnyVersion); !errors.Is(err, agents.ErrProviderInUse) {
		t.Errorf("RemoveModelProvider() while Scout runs on it = %v, want %v", err, agents.ErrProviderInUse)
	}
	if a, err = f.a.SetModel(ctx, f.olivia, f.acme.ID, f.scout.ID, agents.AnyVersion, "", ""); err != nil || a.Provider != "" || a.Model != "" {
		t.Fatalf("SetModel(none) = %+v, %v", a, err)
	}
	if err := f.a.RemoveModelProvider(ctx, f.olivia, f.acme.ID, "anthropic", 2); !errors.Is(err, agents.ErrChanged) {
		t.Errorf("RemoveModelProvider() from a stale version = %v, want %v", err, agents.ErrChanged)
	}
	if err := f.a.RemoveModelProvider(ctx, f.olivia, f.acme.ID, "anthropic", 1); err != nil {
		t.Fatalf("RemoveModelProvider() = %v", err)
	}
	if err := f.a.RemoveModelProvider(ctx, f.olivia, f.acme.ID, "anthropic", agents.AnyVersion); !errors.Is(err, orgs.ErrNotFound) {
		t.Errorf("RemoveModelProvider() again = %v, want %v", err, orgs.ErrNotFound)
	}
	if n := count(t, f.pool, "SELECT count(*) FROM events WHERE kind IN ('agent.model_set', 'model_provider.removed') AND org_id = $1", f.acme.ID); n != 3 {
		t.Errorf("model events = %d, want 3", n)
	}
}

func TestAgentSecretsStore(t *testing.T) {
	t.Parallel()
	f := newAgentFixture(t)
	ctx := context.Background()
	own, repos := f.addSecret(t, uuid.Nil, "token"), f.addSecret(t, f.website.ID, "deploy")
	for _, tt := range []struct {
		name   string
		secret uuid.UUID
		want   uuid.UUID
		err    error
	}{
		{name: "the organization's own", secret: own},
		{name: "a repository's", secret: repos, want: f.website.ID},
		{name: "none of the organization's", secret: uuid.New(), err: orgs.ErrNotFound},
	} {
		if got, err := f.a.SecretRepository(ctx, f.acme.ID, tt.secret); !errors.Is(err, tt.err) || got != tt.want {
			t.Errorf("%s: SecretRepository() = %v, %v; want %v, %v", tt.name, got, err, tt.want, tt.err)
		}
	}
	named := map[string]uuid.UUID{"TOKEN": own, "DEPLOY_KEY": repos}
	a, err := f.a.SetSecrets(ctx, f.olivia, f.acme.ID, f.scout.ID, 1, named)
	if err != nil || len(a.Secrets) != 2 || a.Secrets["TOKEN"] != own || a.Secrets["DEPLOY_KEY"] != repos || a.Version != 2 {
		t.Fatalf("SetSecrets() = %+v, %v", a, err)
	}
	if _, err := f.a.SetSecrets(ctx, f.olivia, f.acme.ID, f.scout.ID, 1, nil); !errors.Is(err, agents.ErrChanged) {
		t.Errorf("SetSecrets() from a stale version = %v, want %v", err, agents.ErrChanged)
	}
	if a, err = f.a.SetSecrets(ctx, f.olivia, f.acme.ID, f.scout.ID, agents.AnyVersion, nil); err != nil || len(a.Secrets) != 0 || a.Secrets == nil {
		t.Errorf("SetSecrets(none) = %+v, %v", a, err)
	}
	if n := count(t, f.pool, "SELECT count(*) FROM audit_log WHERE table_name = 'agents' AND principal_id = $1 AND action = 'update'", f.olivia); n != 2 {
		t.Errorf("agents audit rows of the changes = %d, want 2", n)
	}
}
