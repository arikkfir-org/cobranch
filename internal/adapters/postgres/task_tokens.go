package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
)

// CreateTaskToken records a token of task taskID, its secret hashed to hash, acting until expiresAt; auth.ErrNotFound
// for a task that isn't there or has ended. Cobranch makes it, so it names no actor.
func (s *AuthStore) CreateTaskToken(ctx context.Context, taskID uuid.UUID, hash []byte, expiresAt time.Time) error {
	n, err := s.q.InsertTaskToken(ctx, hash, expiresAt, taskID)
	if err != nil {
		return fmt.Errorf("recording the task token: %w", err)
	}
	if n == 0 {
		return auth.ErrNotFound
	}
	return nil
}

// TaskToken finds a task token by its secret's hash, as a credential on the task's agent, with the agent, or
// auth.ErrNotFound, also for a task that has ended. An assigner who is suspended counts as gone.
func (s *AuthStore) TaskToken(ctx context.Context, tokenHash []byte) (auth.Credential, auth.User, error) {
	row, err := s.q.TaskToken(ctx, tokenHash)
	if err != nil {
		return auth.Credential{}, auth.User{}, wrap("reading the task token", err)
	}
	assigner := orNil(row.AssignedBy)
	if row.AssignerSuspended != nil && *row.AssignerSuspended {
		assigner = uuid.Nil
	}
	agent, err := s.user(ctx, row.Principal)
	if err != nil {
		return auth.Credential{}, auth.User{}, err
	}
	return auth.Credential{Kind: auth.CredentialTaskToken, PrincipalID: agent.ID, CreatedAt: row.CreatedAt, ExpiresAt: row.ExpiresAt,
		Task: auth.TaskGrant{ID: row.TaskID, OrgID: row.OrgID, AssignerID: assigner}}, agent, nil
}

// DeleteExpiredTaskTokens deletes the task tokens that expired before now, and those of tasks that ended.
func (s *AuthStore) DeleteExpiredTaskTokens(ctx context.Context, now time.Time) (int64, error) {
	n, err := s.q.DeleteExpiredTaskTokens(ctx, now)
	if err != nil {
		return 0, fmt.Errorf("deleting expired task tokens: %w", err)
	}
	return n, nil
}
