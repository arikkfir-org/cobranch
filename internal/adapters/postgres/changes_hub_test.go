package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/agents"
	"cobranch.dev/internal/services/secrets"
)

func TestChangesHub(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		// change changes what Settings · Models shows of Acme through the stores, key being the secret that holds
		// anthropic's key.
		change func(ctx context.Context, t *testing.T, f *agentFixture, key uuid.UUID)
	}{
		{name: "a provider connected", change: func(ctx context.Context, t *testing.T, f *agentFixture, key uuid.UUID) {
			if _, _, err := f.a.SetModelProvider(ctx, f.olivia, f.acme.ID, "openai", key, agents.NoVersion); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "a model added", change: func(ctx context.Context, t *testing.T, f *agentFixture, _ uuid.UUID) {
			if _, _, err := f.a.PutModel(ctx, f.olivia, f.acme.ID, "anthropic", "b-model", agents.ModelInput{}, agents.NoVersion); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "an agent put on a model", change: func(ctx context.Context, t *testing.T, f *agentFixture, _ uuid.UUID) {
			if _, err := f.a.SetModel(ctx, f.olivia, f.acme.ID, f.scout.ID, f.scout.Version, "anthropic", "a-model"); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "the provider's secret deleted", change: func(ctx context.Context, t *testing.T, f *agentFixture, key uuid.UUID) {
			if err := NewSecretStore(f.pool).Delete(ctx, f.olivia, key, secrets.AnyVersion); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "the provider removed", change: func(ctx context.Context, t *testing.T, f *agentFixture, _ uuid.UUID) {
			if err := f.a.RemoveModelProvider(ctx, f.olivia, f.acme.ID, "anthropic", agents.AnyVersion); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			hearChangeOf(t, tt.change)
		})
	}
}

// hearChangeOf connects anthropic to Acme with a secret of its own and adds a-model, watches Acme and another
// organization on a hub of the fixture's database, makes change, and checks that it wakes Acme's watcher alone.
func hearChangeOf(t *testing.T, change func(ctx context.Context, t *testing.T, f *agentFixture, key uuid.UUID)) {
	t.Helper()
	f := newAgentFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	key := f.addSecret(t, uuid.Nil, "anthropic-key")
	if _, _, err := f.a.SetModelProvider(ctx, f.olivia, f.acme.ID, "anthropic", key, agents.NoVersion); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.a.PutModel(ctx, f.olivia, f.acme.ID, "anthropic", "a-model", agents.ModelInput{}, agents.NoVersion); err != nil {
		t.Fatal(err)
	}
	hub := ListenChanges(ctx, f.pool)
	acme, stopAcme := hub.Watch(f.acme.ID)
	defer stopAcme()
	other, stopOther := hub.Watch(uuid.New())
	defer stopOther()
	// Every watcher wakes once the hub starts to listen.
	for _, wake := range []<-chan struct{}{acme, other} {
		select {
		case <-wake:
		case <-time.After(10 * time.Second):
			t.Fatal("no wake once the hub listened")
		}
	}
	change(ctx, t, f, key)
	select {
	case <-acme:
	case <-time.After(10 * time.Second):
		t.Fatal("the change didn't wake the organization's watcher")
	}
	select {
	case <-other:
		t.Error("a change of Acme's woke another organization's watcher")
	case <-time.After(200 * time.Millisecond):
	}
}
