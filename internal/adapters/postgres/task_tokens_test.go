package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/agents"
	"cobranch.dev/internal/services/auth"
)

func TestAuthStoreTaskTokens(t *testing.T) {
	t.Parallel()
	f := newAgentFixture(t)
	ctx := context.Background()
	s := NewAuthStore(f.pool)
	task := f.assigned(t, "Look around", false, uuid.Nil)
	live, stale := auth.Hash(auth.NewSecret(auth.PrefixTaskToken)), auth.Hash(auth.NewSecret(auth.PrefixTaskToken))
	expires := time.Now().Add(time.Hour).UTC().Truncate(time.Microsecond)
	if err := s.CreateTaskToken(ctx, task.ID, live, expires); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateTaskToken(ctx, task.ID, stale, time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateTaskToken(ctx, uuid.New(), auth.Hash(auth.NewSecret(auth.PrefixTaskToken)), expires); !errors.Is(err, auth.ErrNotFound) {
		t.Errorf("CreateTaskToken(no task) = %v, want %v", err, auth.ErrNotFound)
	}
	want := auth.TaskGrant{ID: task.ID, OrgID: f.acme.ID, AssignerID: f.max}
	c, agent, err := s.TaskToken(ctx, live)
	if err != nil || c.Kind != auth.CredentialTaskToken || c.Task != want || c.PrincipalID != f.scout.ID || !c.ExpiresAt.Equal(expires) ||
		agent.ID != f.scout.ID || agent.Kind != auth.KindAgent {
		t.Fatalf("TaskToken() = %+v, %+v, %v", c, agent, err)
	}
	if _, err := f.pool.Exec(ctx, "UPDATE principals SET suspended = true WHERE id = $1", f.max); err != nil {
		t.Fatal(err)
	}
	if c, _, err := s.TaskToken(ctx, live); err != nil || c.Task.AssignerID != uuid.Nil {
		t.Errorf("TaskToken(assigner suspended) = %+v, %v, want no assigner", c.Task, err)
	}
	if n, err := s.DeleteExpiredTaskTokens(ctx, time.Now()); err != nil || n != 1 {
		t.Errorf("DeleteExpiredTaskTokens() = %d, %v, want the stale one", n, err)
	}
	if _, err := f.a.Cancel(ctx, f.olivia, f.acme.ID, task.Number, agents.AnyVersion); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.TaskToken(ctx, live); !errors.Is(err, auth.ErrNotFound) {
		t.Errorf("TaskToken(task ended) = %v, want %v", err, auth.ErrNotFound)
	}
	if err := s.CreateTaskToken(ctx, task.ID, auth.Hash(auth.NewSecret(auth.PrefixTaskToken)), expires); !errors.Is(err, auth.ErrNotFound) {
		t.Errorf("CreateTaskToken(task ended) = %v, want %v", err, auth.ErrNotFound)
	}
	if n, err := s.DeleteExpiredTaskTokens(ctx, time.Now()); err != nil || n != 1 {
		t.Errorf("DeleteExpiredTaskTokens(task ended) = %d, %v, want the live one", n, err)
	}
}
