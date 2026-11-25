package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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

// DB is the stores' one way to the database: every query runs in a Scope, which says whose rows it may reach. Row-level
// security needs it: PostgreSQL keeps organizations apart only for a
// transaction that says which one it works in, so no store holds the pool or a bare *queries.Queries. In pins one
// organization, Of the organization that owns a row, and Everywhere none, for the work that spans organizations, which
// says why where it runs (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/tenant-isolation.md).
type DB struct {
	pool *pgxpool.Pool
}

// newDB returns the stores' way to pool's database.
func newDB(pool *pgxpool.Pool) *DB {
	return &DB{pool: pool}
}

// Table names a table whose rows have columns id and org_id, for Of.
type Table string

// The tables Of pins by.
const (
	Repositories  Table = "repositories"
	Issues        Table = "issues"
	AgentTasks    Table = "agent_tasks"
	Agents        Table = "agents"
	Artifacts     Table = "artifacts"
	ChatThreads   Table = "chat_threads"
	Conversations Table = "conversations"
	Deliveries    Table = "deliveries"
	Environments  Table = "environments"
	JobRuns       Table = "job_runs"
	Jobs          Table = "jobs"
	Pushes        Table = "pushes"
	Reviews       Table = "reviews"
	Secrets       Table = "secrets"
	Branches      Table = "branches"
	Invitations   Table = "invitations"
	JoinRequests  Table = "join_requests"
	Roles         Table = "roles"
)

// Why the work of Everywhere spans organizations.
const (
	adminAPI         = "the Admin API administers every organization"
	authenticating   = "authenticating a credential, before its organization is known"
	signingIn        = "signing in and a person's sessions belong to no organization"
	personal         = "a person's own things span their organizations"
	installation     = "the installation's settings belong to no organization"
	objectStore      = "the object store keeps objects by key, whoever's they are"
	controllerQueues = "the controller's queues hold every organization's work"
	pushQueue        = "the push worker's queue holds every organization's pushes"
	maintenance      = "maintenance sweeps every organization"
)

// Scope is whose rows a query may reach, with the queries to run there: called on a Scope, a query runs in a
// transaction of its own that the scope binds, or in the transaction its context carries; Transact runs several in
// one.
type Scope struct {
	*queries.Queries
	db *DB
	// everywhere is true for the scope no organization binds, and why says what spans them.
	everywhere bool
	why        string
	// deferred is true for the scope of a transaction that leaves its scope to the first work within it.
	deferred bool
	// org is the organization In pins; table and row are the row whose organization Of pins.
	org   uuid.UUID
	table Table
	row   uuid.UUID
}

// In is the scope of one organization: its queries run as role cobranch_tenant with the organization pinned, so they
// see and change its rows alone, whatever they ask for.
func (d *DB) In(orgID uuid.UUID) Scope {
	return d.scope(Scope{org: orgID})
}

// Of is the scope of the organization that owns the row of table with id, for work that starts from a repository, a
// task or another row rather than its organization. A row that isn't there owns nothing, so the queries find nothing.
func (d *DB) Of(table Table, id uuid.UUID) Scope {
	return d.scope(Scope{table: table, row: id})
}

// Everywhere is the scope of no one organization, for work that spans them or comes before any is known: the
// controller's queues, maintenance, the Admin API, signing in, a person's own things. Its queries run as the role
// Cobranch connects as, which no policy binds. why says what the work is, for whoever reads the call.
func (d *DB) Everywhere(why string) Scope {
	return d.scope(Scope{everywhere: true, why: why})
}

// OfAny is Of the first of ids, for work on several rows of one organization; with none, it finds nothing.
func (d *DB) OfAny(table Table, ids []uuid.UUID) Scope {
	if len(ids) == 0 {
		return d.In(uuid.Nil)
	}
	return d.Of(table, ids[0])
}

// Deferred is the scope of a transaction that joins several stores' work without knowing whose it is: the first work
// within it that names an organization pins the transaction to it, and the rest must agree. Its own queries, before
// any, run as Everywhere's do.
func (d *DB) Deferred() Scope {
	return d.scope(Scope{deferred: true})
}

func (d *DB) scope(s Scope) Scope {
	s.db = d
	s.Queries = queries.New(scopedConn{s})
	return s
}

// String names the scope, for errors.
func (s Scope) String() string {
	switch {
	case s.deferred:
		return "the organization its work names first"
	case s.everywhere:
		return "everywhere (" + s.why + ")"
	case s.table != "":
		return fmt.Sprintf("the organization of %s %s", s.table, s.row)
	}
	return "organization " + s.org.String()
}

// pin is the statement that binds a transaction to the scope, and its arguments.
func (s Scope) pin() (string, []any) {
	if s.table != "" {
		return "SELECT pin_organization_of($1::regclass, $2)", []any{string(s.table), s.row}
	}
	return "SELECT pin_organization($1)", []any{s.org}
}

// begin is the statement that begins a transaction bound to the scope, in one round trip. Its values are a UUID's
// text and a Table's constant, so nothing in it comes from a request as written.
func (s Scope) begin() string {
	switch {
	case s.everywhere, s.deferred:
		return "BEGIN"
	case s.table != "":
		return fmt.Sprintf("BEGIN; SELECT pin_organization_of('%s', '%s')", s.table, s.row)
	}
	return fmt.Sprintf("BEGIN; SELECT pin_organization('%s')", s.org)
}

// running is the transaction a context carries, with the scope it runs in, so a store method called within another's
// transaction joins it instead of opening a second one on another connection.
type running struct {
	tx pgx.Tx
	// scope is the transaction's: the one that began it, or for a Deferred one, the first that pinned it.
	scope *Scope
}

type txKey struct{}

// join admits work in s to the transaction r, or says why not. A transaction that spans organizations admits any; a
// Deferred one is pinned by the first work that names an organization; one pinned to an organization admits work in
// that organization or in a row's, which its policies then hold to it. Anything else would silently find less than it
// asked for, so it is refused.
func (r running) join(ctx context.Context, s Scope) error {
	outer := *r.scope
	switch {
	case outer.deferred && (s.deferred || s.everywhere):
		return nil
	case outer.deferred:
		sql, args := s.pin()
		if _, err := r.tx.Exec(ctx, sql, args...); err != nil {
			return fmt.Errorf("pinning %s: %w", s, err)
		}
		*r.scope = s
		return nil
	case outer.everywhere:
		return nil
	case s.everywhere:
		return fmt.Errorf("a transaction in %s can't run work %s", outer, s)
	case s.deferred, s.table != "", outer.table != "":
		return nil // a row's organization isn't known here; the policies hold the work to the pinned one
	case s.org != outer.org:
		return fmt.Errorf("a transaction in %s can't run work in %s", outer, s)
	}
	return nil
}

// Transact runs fn with the queries in a transaction in the scope that acts for actor, committed when it returns nil
// and rolled back otherwise; a unique constraint it hits is a ConflictError. The actor goes in session variable
// cobranch.principal_id, local to the transaction, so trigger audit() records who changed each row
// (db/migrations/00003_audit.sql); uuid.Nil leaves it unset, for a change of Cobranch's own.
//
// It is re-entrant: fn gets a context that carries the transaction, and a Transact within it runs fn in a savepoint
// of the same transaction, in the outer scope and acting for the outer actor, released when fn returns nil and rolled
// back to otherwise. The outer transaction commits the whole.
func (s Scope) Transact(ctx context.Context, what string, actor uuid.UUID, fn func(ctx context.Context, q *queries.Queries) error) error {
	if outer, nested := ctx.Value(txKey{}).(running); nested {
		if err := outer.join(ctx, s); err != nil {
			return fmt.Errorf("%s: %w", what, err)
		}
		return savepoint(ctx, what, fn)
	}
	tx, err := s.db.pool.BeginTx(ctx, pgx.TxOptions{BeginQuery: s.begin()})
	if err != nil {
		return fmt.Errorf("%s: beginning the transaction: %w", what, err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // a no-op after Commit
	q := queries.New(tx)
	if actor != uuid.Nil {
		if err := q.SetPrincipal(ctx, actor.String()); err != nil {
			return fmt.Errorf("%s: naming the principal: %w", what, err)
		}
	}
	if err := fn(context.WithValue(ctx, txKey{}, running{tx: tx, scope: &s}), q); err != nil {
		return fmt.Errorf("%s: %w", what, conflict(err))
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("%s: committing: %w", what, err)
	}
	return nil
}

// savepoint runs fn in a savepoint of the transaction ctx carries, released when fn returns nil and rolled back to
// otherwise, so a failure it expects, such as a row that is already there, leaves the transaction usable.
func savepoint(ctx context.Context, what string, fn func(ctx context.Context, q *queries.Queries) error) error {
	outer, ok := ctx.Value(txKey{}).(running)
	if !ok {
		return fmt.Errorf("%s: a savepoint needs a transaction", what)
	}
	tx, err := outer.tx.Begin(ctx)
	if err != nil {
		return fmt.Errorf("%s: beginning the savepoint: %w", what, err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // a no-op after Commit
	if err := fn(context.WithValue(ctx, txKey{}, running{tx: tx, scope: outer.scope}), queries.New(tx)); err != nil {
		return fmt.Errorf("%s: %w", what, conflict(err))
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("%s: releasing the savepoint: %w", what, err)
	}
	return nil
}

// conflict is err, as a ConflictError when a unique constraint refused a row.
func conflict(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
		return &ConflictError{Constraint: pgErr.ConstraintName, Err: err}
	}
	return err
}

// scopedConn runs each query of a Scope called outside Transact: in the transaction the context carries, or else in
// one of its own, which a scope that pins an organization begins by pinning it. That transaction is a batch of the
// pin and the query, sent together, so a bound query costs the round trip an unbound one does.
type scopedConn struct {
	scope Scope
}

var _ queries.DBTX = scopedConn{}

// conn is where a query of the scope runs outside a batch: the context's transaction, or the pool for a scope that
// spans organizations. It is nil for a scope that must pin its organization first.
func (c scopedConn) conn(ctx context.Context) (queries.DBTX, error) {
	if outer, ok := ctx.Value(txKey{}).(running); ok {
		if err := outer.join(ctx, c.scope); err != nil {
			return nil, err
		}
		return outer.tx, nil
	}
	if c.scope.everywhere || c.scope.deferred {
		return c.scope.db.pool, nil
	}
	return nil, nil
}

// pinned sends the scope's pin and then queue's queries as one batch, which PostgreSQL runs as one transaction, and
// returns its results past the pin's.
func (c scopedConn) pinned(ctx context.Context, queue func(*pgx.Batch)) (pgx.BatchResults, error) {
	batch := &pgx.Batch{}
	sql, args := c.scope.pin()
	batch.Queue(sql, args...)
	queue(batch)
	results := c.scope.db.pool.SendBatch(ctx, batch)
	if _, err := results.Exec(); err != nil {
		return nil, errors.Join(fmt.Errorf("pinning %s: %w", c.scope, err), results.Close())
	}
	return results, nil
}

func (c scopedConn) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	conn, err := c.conn(ctx)
	if err != nil {
		return pgconn.CommandTag{}, err
	}
	if conn != nil {
		return conn.Exec(ctx, sql, args...) //nolint:wrapcheck // a pass-through: the store that ran the query wraps its error
	}
	results, err := c.pinned(ctx, func(b *pgx.Batch) { b.Queue(sql, args...) })
	if err != nil {
		return pgconn.CommandTag{}, err
	}
	tag, err := results.Exec()
	return tag, errors.Join(err, results.Close())
}

func (c scopedConn) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	conn, err := c.conn(ctx)
	if err != nil {
		return nil, err
	}
	if conn != nil {
		return conn.Query(ctx, sql, args...) //nolint:wrapcheck // a pass-through: the store that ran the query wraps its error
	}
	results, err := c.pinned(ctx, func(b *pgx.Batch) { b.Queue(sql, args...) })
	if err != nil {
		return nil, err
	}
	rows, err := results.Query()
	if err != nil {
		return nil, errors.Join(err, results.Close())
	}
	return &batchRows{Rows: rows, results: results}, nil
}

func (c scopedConn) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	conn, err := c.conn(ctx)
	if err != nil {
		return errRow{err}
	}
	if conn != nil {
		return conn.QueryRow(ctx, sql, args...)
	}
	results, err := c.pinned(ctx, func(b *pgx.Batch) { b.Queue(sql, args...) })
	if err != nil {
		return errRow{err}
	}
	return batchRow{Row: results.QueryRow(), results: results}
}

func (c scopedConn) SendBatch(ctx context.Context, batch *pgx.Batch) pgx.BatchResults {
	conn, err := c.conn(ctx)
	if err != nil {
		return errBatch{err}
	}
	if conn != nil {
		return conn.SendBatch(ctx, batch)
	}
	results, err := c.pinned(ctx, func(b *pgx.Batch) { b.QueuedQueries = append(b.QueuedQueries, batch.QueuedQueries...) })
	if err != nil {
		return errBatch{err}
	}
	return results
}

// batchRows are a pinned query's rows: closing them ends its batch too, which commits its transaction and frees its
// connection.
type batchRows struct {
	pgx.Rows
	results pgx.BatchResults
	closed  bool
	err     error
}

func (r *batchRows) Close() {
	if r.closed {
		return
	}
	r.closed = true
	r.Rows.Close()
	r.err = r.results.Close()
}

func (r *batchRows) Err() error {
	if err := r.Rows.Err(); err != nil {
		return err //nolint:wrapcheck // a pass-through: the store that ran the query wraps its error
	}
	return r.err
}

// batchRow is a pinned query's one row: scanning it ends its batch too.
type batchRow struct {
	pgx.Row
	results pgx.BatchResults
}

func (r batchRow) Scan(dest ...any) error {
	err := r.Row.Scan(dest...)
	if closeErr := r.results.Close(); err == nil {
		return closeErr //nolint:wrapcheck // a pass-through: the store that ran the query wraps its error
	}
	return err //nolint:wrapcheck // a pass-through: the store that ran the query wraps its error
}

// errRow is the row of a query that could not run.
type errRow struct{ err error }

func (r errRow) Scan(...any) error { return r.err }

// errBatch are the results of a batch that could not run.
type errBatch struct{ err error }

func (b errBatch) Exec() (pgconn.CommandTag, error) { return pgconn.CommandTag{}, b.err }
func (b errBatch) Query() (pgx.Rows, error)         { return nil, b.err }
func (b errBatch) QueryRow() pgx.Row                { return errRow(b) }
func (b errBatch) Close() error                     { return b.err }
