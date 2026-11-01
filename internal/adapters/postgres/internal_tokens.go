package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"cobranch.dev/internal/adapters/postgres/queries"
)

// InternalTokens reads the tokens of internal calls between Cobranch's workloads, which a migration generates
// (db/migrations/00014_internal_tokens.sql): both ends of a call read its token, so neither needs a secret from the
// operator.
type InternalTokens struct {
	q *queries.Queries
}

// NewInternalTokens returns the internal tokens of pool's database.
func NewInternalTokens(pool *pgxpool.Pool) *InternalTokens {
	return &InternalTokens{q: queries.New(pool)}
}

// Read reads the token of the internal call name, such as git-server.
func (t *InternalTokens) Read(ctx context.Context, name string) (string, error) {
	token, err := t.q.InternalToken(ctx, name)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("reading internal token %s: none, as before the migrations ran", name)
	}
	if err != nil {
		return "", fmt.Errorf("reading internal token %s: %w", name, err)
	}
	return token, nil
}
