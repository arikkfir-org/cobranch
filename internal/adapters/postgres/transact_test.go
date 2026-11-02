package postgres

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"cobranch.dev/internal/adapters/postgres/queries"
)

// insertPrincipal inserts a principal with id, as a change of a transaction.
func insertPrincipal(ctx context.Context, q *queries.Queries, id uuid.UUID) error {
	if err := q.InsertPrincipal(ctx, queries.InsertPrincipalParams{ID: id, Kind: "user", DisplayName: id.String()[:8], CreatedAt: now}); err != nil {
		return fmt.Errorf("inserting %s: %w", id, err)
	}
	return nil
}

// TestTransactNested checks that a transact within a transact joins it: the inner one runs in a savepoint, acts
// for the outer actor, and its failure rolls back its own changes alone. The pool has one connection, so a second
// transaction would deadlock rather than join.
func TestTransactNested(t *testing.T) {
	t.Parallel()
	_, shared := authStore(t)
	cfg := shared.Config().Copy()
	cfg.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	q := queries.New(pool)
	actor, outer, kept, dropped := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	errInner := errors.New("inner failure")
	err = transact(t.Context(), pool, q, "outer", actor, func(ctx context.Context, q *queries.Queries) error {
		if err := insertPrincipal(ctx, q, outer); err != nil {
			return err
		}
		if err := transact(ctx, pool, q, "kept", uuid.Nil, func(ctx context.Context, q *queries.Queries) error {
			return insertPrincipal(ctx, q, kept)
		}); err != nil {
			return err
		}
		err := transact(ctx, pool, q, "dropped", actor, func(ctx context.Context, q *queries.Queries) error {
			if err := insertPrincipal(ctx, q, dropped); err != nil {
				return err
			}
			return errInner
		})
		if !errors.Is(err, errInner) {
			t.Errorf("the inner transact error = %v, want its failure", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for id, want := range map[uuid.UUID]int{outer: 1, kept: 1, dropped: 0} {
		if n := count(t, pool, "SELECT count(*) FROM principals WHERE id = $1", id); n != want {
			t.Errorf("%d rows for %s, want %d", n, id, want)
		}
	}
	// Audit rows of the inner, kept change name the outer actor, which the savepoint inherited.
	if n := count(t, pool, "SELECT count(*) FROM audit_log WHERE table_name = 'principals' AND principal_id = $1", actor); n != 2 {
		t.Errorf("%d audit rows name the outer actor, want 2 (the outer and the kept inner change)", n)
	}
}
