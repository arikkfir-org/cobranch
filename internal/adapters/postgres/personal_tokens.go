package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/adapters/postgres/queries"
	"cobranch.dev/internal/services/auth"
)

// CreateToken records personal token t, its secret hashed to hash, and its event, as its user.
func (s *AuthStore) CreateToken(ctx context.Context, t auth.Credential, hash []byte) error {
	return s.transact(ctx, "recording the token", t.PrincipalID, func(ctx context.Context, q *queries.Queries) error {
		if err := q.InsertPersonalToken(ctx, queries.InsertPersonalTokenParams{
			ID: t.ID, TokenHash: hash, PrincipalID: t.PrincipalID, Hint: t.Hint, CreatedAt: t.CreatedAt, ExpiresAt: t.ExpiresAt,
			Name: t.Name, Permissions: t.Permissions, Issuer: t.Issuer, Subject: t.Subject,
		}); err != nil {
			return fmt.Errorf("inserting the token: %w", err)
		}
		return event(ctx, q, t.PrincipalID, "personal_token.created", t.ID,
			map[string]any{"name": t.Name, "permissions": t.Permissions, "expiresAt": t.ExpiresAt})
	})
}

// Tokens lists a user's personal tokens that are not revoked, newest first.
func (s *AuthStore) Tokens(ctx context.Context, userID uuid.UUID) ([]auth.Credential, error) {
	rows, err := s.q.PersonalTokens(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("listing the tokens: %w", err)
	}
	tokens := make([]auth.Credential, 0, len(rows))
	for _, c := range rows {
		tokens = append(tokens, credential(c))
	}
	return tokens, nil
}

// RevokeToken revokes personal token id of user userID at now.
func (s *AuthStore) RevokeToken(ctx context.Context, userID, id uuid.UUID, now time.Time) error {
	return s.transact(ctx, "revoking the token", userID, func(ctx context.Context, q *queries.Queries) error {
		revoked, err := q.RevokePersonalToken(ctx, now, id, userID)
		if err != nil {
			return fmt.Errorf("updating the token: %w", err)
		}
		if revoked == 0 {
			return auth.ErrNotFound
		}
		return event(ctx, q, userID, "personal_token.revoked", id, nil)
	})
}
