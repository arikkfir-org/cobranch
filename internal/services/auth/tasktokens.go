package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// TaskTokenLifetime is how long a task token acts. Cobranch renews a running task's every half hour, so its pod
// always holds one with half an hour or more to go, and a token that leaks stops within the hour.
const TaskTokenLifetime = time.Hour

// IssueTaskToken makes a token for task taskID that acts as its agent, capped by its assigner, until TaskTokenLifetime
// from now, and returns it with its end; ErrNotFound for a task that isn't there or has ended. The tokens it made
// before keep acting until their own end, so a pod picks up the new one whenever it reads it. Cobranch calls it when it
// starts a task's pod and whenever it renews the token; no request reaches it.
func (s *Service) IssueTaskToken(ctx context.Context, taskID uuid.UUID) (string, time.Time, error) {
	secret := NewSecret(PrefixTaskToken)
	expiresAt := s.now().Add(TaskTokenLifetime)
	if err := s.store.CreateTaskToken(ctx, taskID, Hash(secret), expiresAt); err != nil {
		return "", time.Time{}, fmt.Errorf("recording task %s's token: %w", taskID, err)
	}
	return secret, expiresAt, nil
}

// authenticateTaskToken resolves a task token whose secret hashes to hash to the task's agent, with the task the
// token works.
func (s *Service) authenticateTaskToken(ctx context.Context, hash []byte) (Authenticated, error) {
	credential, agent, err := s.store.TaskToken(ctx, hash)
	if errors.Is(err, ErrNotFound) {
		return Authenticated{}, ErrUnauthenticated
	}
	if err != nil {
		return Authenticated{}, fmt.Errorf("finding the task token: %w", err)
	}
	if !credential.Live(s.now()) || agent.Suspended {
		return Authenticated{}, ErrUnauthenticated
	}
	return Authenticated{User: agent, Credential: credential}, nil
}

// DeleteExpiredTaskTokens deletes the task tokens that expired, and those of tasks that ended, and returns how many:
// the maintenance job's hourly task.
func (s *Service) DeleteExpiredTaskTokens(ctx context.Context) (int64, error) {
	n, err := s.store.DeleteExpiredTaskTokens(ctx, s.now())
	if err != nil {
		return 0, fmt.Errorf("deleting expired task tokens: %w", err)
	}
	return n, nil
}
