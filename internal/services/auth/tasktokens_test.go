package auth_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/auth/authtest"
)

func TestTaskTokens(t *testing.T) {
	agent := auth.Principal{ID: uuid.New(), Kind: auth.KindAgent, DisplayName: "Scout"}
	grant := auth.TaskGrant{ID: uuid.New(), OrgID: uuid.New(), AssignerID: uuid.New()}
	tests := []struct {
		name string
		// change changes the store once the token is issued.
		change    func(*authtest.Store)
		at        time.Time
		token     string // instead of the issued one
		wantErr   error
		wantSwept int64
	}{
		{name: "a fresh token acts as the agent, for its task", at: now},
		{name: "a token near its end still acts", at: now.Add(auth.TaskTokenLifetime - time.Second)},
		{name: "a token at its end", at: now.Add(auth.TaskTokenLifetime), wantErr: auth.ErrUnauthenticated},
		{name: "an expired token", at: now.Add(auth.TaskTokenLifetime + time.Second), wantErr: auth.ErrUnauthenticated, wantSwept: 1},
		{name: "a token of a task that ended", change: func(s *authtest.Store) { s.Tasks[grant.ID].Ended = true }, at: now,
			wantErr: auth.ErrUnauthenticated, wantSwept: 1},
		{name: "a suspended agent", change: func(s *authtest.Store) { s.Tasks[grant.ID].Agent.Suspended = true }, at: now,
			wantErr: auth.ErrUnauthenticated},
		{name: "a token nobody issued", token: auth.NewSecret(auth.PrefixTaskToken), at: now, wantErr: auth.ErrUnauthenticated},
		{name: "a personal token's shape", token: auth.NewSecret(auth.PrefixPersonalToken), at: now, wantErr: auth.ErrUnauthenticated},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			s, store, _ := newService(auth.Settings{}, alice)
			store.Tasks[grant.ID] = &authtest.Task{Grant: grant, Agent: agent}
			secret, expiresAt, err := s.IssueTaskToken(ctx, grant.ID)
			if err != nil || !auth.ValidSecret(auth.PrefixTaskToken, secret) || !expiresAt.Equal(now.Add(auth.TaskTokenLifetime)) {
				t.Fatalf("IssueTaskToken() = %q, %v, %v", secret, expiresAt, err)
			}
			if tt.change != nil {
				tt.change(store)
			}
			s.SetClock(func() time.Time { return tt.at })
			if tt.token != "" {
				secret = tt.token
			}
			got, err := s.Authenticate(ctx, secret)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Authenticate() error = %v, want %v", err, tt.wantErr)
			}
			if err == nil && (got.User.ID != agent.ID || got.Credential.Kind != auth.CredentialTaskToken || got.Credential.Task != grant ||
				got.Session.ID != uuid.Nil) {
				t.Errorf("Authenticate() = %+v", got)
			}
			if swept, err := s.DeleteExpiredTaskTokens(ctx); err != nil || swept != tt.wantSwept {
				t.Errorf("DeleteExpiredTaskTokens() = %d, %v, want %d", swept, err, tt.wantSwept)
			}
		})
	}
}

func TestIssueTaskTokenRefusesAnEndedTask(t *testing.T) {
	ended := uuid.New()
	tests := []struct {
		name string
		task uuid.UUID
	}{
		{name: "a task that ended", task: ended},
		{name: "a task that isn't there", task: uuid.New()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, store, _ := newService(auth.Settings{}, alice)
			store.Tasks[ended] = &authtest.Task{Grant: auth.TaskGrant{ID: ended}, Ended: true}
			if _, _, err := s.IssueTaskToken(context.Background(), tt.task); !errors.Is(err, auth.ErrNotFound) {
				t.Errorf("IssueTaskToken() error = %v, want %v", err, auth.ErrNotFound)
			}
		})
	}
}
