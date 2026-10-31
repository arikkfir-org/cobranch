package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"cobranch.dev/internal/adapters/postgres/queries"
)

// uniqueViolation is PostgreSQL's error code for a row that a unique constraint refuses.
const uniqueViolation = "23505"

// ConflictError is a row that a unique constraint refused: Constraint names which, so a store maps it to the row
// that is already there.
type ConflictError struct {
	Constraint string
	Err        error
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("a row is already there (%s): %v", e.Constraint, e.Err)
}

func (e *ConflictError) Unwrap() error { return e.Err }

// transact runs fn with the queries in a transaction on pool that acts for actor, committed when it returns nil and
// rolled back otherwise; a unique constraint it hits is a ConflictError. The actor goes in session variable
// cobranch.principal_id, local to the transaction, so trigger audit() records who changed each row
// (db/migrations/00003_audit.sql); uuid.Nil leaves it unset, for a change of Cobranch's own.
func transact(ctx context.Context, pool *pgxpool.Pool, q *queries.Queries, what string, actor uuid.UUID, fn func(q *queries.Queries) error) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("%s: beginning the transaction: %w", what, err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // a no-op after Commit
	q = q.WithTx(tx)
	if actor != uuid.Nil {
		if err := q.SetPrincipal(ctx, actor.String()); err != nil {
			return fmt.Errorf("%s: naming the principal: %w", what, err)
		}
	}
	if err := fn(q); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
			return fmt.Errorf("%s: %w", what, &ConflictError{Constraint: pgErr.ConstraintName, Err: err})
		}
		return fmt.Errorf("%s: %w", what, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("%s: committing: %w", what, err)
	}
	return nil
}
