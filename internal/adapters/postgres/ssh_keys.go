package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"cobranch.dev/internal/adapters/postgres/queries"
	"cobranch.dev/internal/services/auth"
)

// AddSSHKey records SSH key k, its fingerprint hash, and its event, as its user: auth.ErrSSHKeyTaken when a user
// holds the key already.
func (s *AuthStore) AddSSHKey(ctx context.Context, k auth.Credential, hash []byte) error {
	err := s.transact(ctx, "recording the SSH key", k.PrincipalID, func(ctx context.Context, q *queries.Queries) error {
		if err := q.InsertSSHKey(ctx, queries.InsertSSHKeyParams{ID: k.ID, TokenHash: hash, PrincipalID: k.PrincipalID, Hint: k.Hint,
			CreatedAt: k.CreatedAt, Name: k.Name, PublicKey: k.PublicKey}); err != nil {
			return fmt.Errorf("inserting the key: %w", err)
		}
		return event(ctx, q, k.PrincipalID, "ssh_key.added", k.ID, map[string]any{"name": k.Name, "fingerprint": k.Hint})
	})
	var conflict *ConflictError
	if errors.As(err, &conflict) && conflict.Constraint == "credentials_token_hash_key" {
		return auth.ErrSSHKeyTaken
	}
	return err
}

// SSHKeys lists a user's SSH keys, newest first.
func (s *AuthStore) SSHKeys(ctx context.Context, userID uuid.UUID) ([]auth.Credential, error) {
	rows, err := s.q.SSHKeys(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("listing the SSH keys: %w", err)
	}
	keys := make([]auth.Credential, 0, len(rows))
	for _, c := range rows {
		keys = append(keys, credential(c))
	}
	return keys, nil
}

// DeleteSSHKey deletes SSH key id of user userID, and records its event, or auth.ErrNotFound.
func (s *AuthStore) DeleteSSHKey(ctx context.Context, userID, id uuid.UUID) error {
	return s.transact(ctx, "deleting the SSH key", userID, func(ctx context.Context, q *queries.Queries) error {
		deleted, err := q.DeleteSSHKey(ctx, id, userID)
		if err != nil {
			return fmt.Errorf("deleting the key: %w", err)
		}
		if deleted == 0 {
			return auth.ErrNotFound
		}
		return event(ctx, q, userID, "ssh_key.deleted", id, nil)
	})
}
