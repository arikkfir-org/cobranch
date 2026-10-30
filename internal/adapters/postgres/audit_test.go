package postgres

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"cobranch.dev/internal/services/auth"
)

// auditRow is a row of audit_log as the tests read it.
type auditRow struct {
	Table, Action string
	Principal     *uuid.UUID
	Before, After map[string]any
}

// auditLog lists the audit rows of table, oldest first.
func auditLog(t *testing.T, pool *pgxpool.Pool, table string) []auditRow {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		"SELECT table_name, action, principal_id, before, after FROM audit_log WHERE table_name = $1 ORDER BY at, id", table)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var log []auditRow
	for rows.Next() {
		var r auditRow
		var before, after []byte
		if err := rows.Scan(&r.Table, &r.Action, &r.Principal, &before, &after); err != nil {
			t.Fatal(err)
		}
		for raw, dest := range map[*[]byte]*map[string]any{&before: &r.Before, &after: &r.After} {
			if *raw != nil {
				if err := json.Unmarshal(*raw, dest); err != nil {
					t.Fatal(err)
				}
			}
		}
		log = append(log, r)
	}
	return log
}

// keys lists m's keys, sorted.
func keys(m map[string]any) []string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	slices.Sort(ks)
	return ks
}

// auditCases are what the audited tables record for each change the stores make.
var auditCases = []struct {
	name string
	// table is the audited table the case reads the log of.
	table string
	// do changes the database through s, and returns the principal the rows should name (nil for none).
	do func(t *testing.T, s *AuthStore, pool *pgxpool.Pool) *uuid.UUID
	// want are the rows' actions, and for each the keys of its before and after (nil for a NULL).
	want []auditRow
	// hidden are columns that no row may hold.
	hidden []string
}{
	{
		name: "a sign-up records every row it inserts, as the user", table: "principals",
		do: func(t *testing.T, s *AuthStore, _ *pgxpool.Pool) *uuid.UUID {
			up := alice(t, s)
			return &up.Principal.ID
		},
		want: []auditRow{{Action: "insert", After: map[string]any{"id": nil, "kind": nil, "display_name": nil, "suspended": nil, "created_at": nil}}},
	},
	{
		name: "an update keeps the whole row before and only the changed columns after", table: "identities",
		do: func(t *testing.T, s *AuthStore, _ *pgxpool.Pool) *uuid.UUID {
			up := alice(t, s)
			i := up.Identity
			i.Name, i.LastSignInAt = "Alice A.", now.Add(time.Hour)
			mustSignIn(t, s, i, false, session(up.Principal.ID, "t1"))
			return &up.Principal.ID
		},
		want: []auditRow{
			{Action: "insert", After: map[string]any{"issuer": nil, "subject": nil, "principal_id": nil, "email": nil, "email_verified": nil, "name": nil, "created_at": nil, "last_sign_in_at": nil}},
			{Action: "update", Before: map[string]any{"issuer": nil, "subject": nil, "principal_id": nil, "email": nil, "email_verified": nil, "name": nil, "created_at": nil, "last_sign_in_at": nil}, After: map[string]any{"name": nil, "last_sign_in_at": nil}},
		},
	},
	{
		name: "an update that changes nothing records nothing", table: "identities",
		do: func(t *testing.T, s *AuthStore, _ *pgxpool.Pool) *uuid.UUID {
			up := alice(t, s)
			mustSignIn(t, s, up.Identity, false, session(up.Principal.ID, "t1"))
			return &up.Principal.ID
		},
		want: []auditRow{{Action: "insert", After: map[string]any{"issuer": nil, "subject": nil, "principal_id": nil, "email": nil, "email_verified": nil, "name": nil, "created_at": nil, "last_sign_in_at": nil}}},
	},
	{
		name: "a session's token hash and its use stay out; its revocation is a row", table: "sessions",
		do: func(t *testing.T, s *AuthStore, _ *pgxpool.Pool) *uuid.UUID {
			ctx := t.Context()
			up := alice(t, s)
			sess := session(up.Principal.ID, "t1")
			mustSignIn(t, s, up.Identity, false, sess)
			must(t, s.Touch(ctx, sess.ID, now.Add(time.Minute)))
			must(t, s.Revoke(ctx, up.Principal.ID, sess.ID, now.Add(2*time.Minute)))
			return &up.Principal.ID
		},
		want: []auditRow{
			{Action: "insert", After: map[string]any{"id": nil, "principal_id": nil, "issuer": nil, "subject": nil, "created_at": nil, "last_seen_at": nil, "idle_expires_at": nil, "expires_at": nil, "revoked_at": nil, "user_agent": nil, "ip": nil}},
			{Action: "update", Before: map[string]any{"id": nil, "principal_id": nil, "issuer": nil, "subject": nil, "created_at": nil, "last_seen_at": nil, "idle_expires_at": nil, "expires_at": nil, "revoked_at": nil, "user_agent": nil, "ip": nil}, After: map[string]any{"revoked_at": nil}},
		},
		hidden: []string{"token_hash"},
	},
	{
		name: "a grant taken back is a delete, as the user", table: "grants",
		do: func(t *testing.T, s *AuthStore, _ *pgxpool.Pool) *uuid.UUID {
			up := alice(t, s)
			mustSignIn(t, s, up.Identity, true, session(up.Principal.ID, "t1"))
			mustSignIn(t, s, up.Identity, false, session(up.Principal.ID, "t2"))
			return &up.Principal.ID
		},
		want: []auditRow{
			{Action: "insert", After: map[string]any{"id": nil, "principal_id": nil, "role_id": nil, "scope": nil, "scope_id": nil, "source": nil, "created_at": nil}},
			{Action: "delete", Before: map[string]any{"id": nil, "principal_id": nil, "role_id": nil, "scope": nil, "scope_id": nil, "source": nil, "created_at": nil}},
		},
	},
	{
		name: "a change outside a store's transaction names no principal", table: "principals",
		do: func(t *testing.T, _ *AuthStore, pool *pgxpool.Pool) *uuid.UUID {
			_, err := pool.Exec(t.Context(), "INSERT INTO principals (id, kind, display_name) VALUES ($1, 'user', 'Bob')", uuid.New())
			must(t, err)
			return nil
		},
		want: []auditRow{{Action: "insert", After: map[string]any{"id": nil, "kind": nil, "display_name": nil, "suspended": nil, "created_at": nil}}},
	},
}

func TestAudit(t *testing.T) {
	for _, tc := range auditCases {
		t.Run(tc.name, func(t *testing.T) {
			s, pool := authStore(t)
			principal := tc.do(t, s, pool)
			got := auditLog(t, pool, tc.table)
			if len(got) != len(tc.want) {
				t.Fatalf("got %d rows, want %d: %+v", len(got), len(tc.want), got)
			}
			for i, row := range got {
				checkAuditRow(t, i, row, tc.want[i], tc.table, principal, tc.hidden)
			}
		})
	}
}

// must fails the test on err.
func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// mustSignIn signs i in on s with session, or fails the test.
func mustSignIn(t *testing.T, s *AuthStore, i auth.Identity, admin bool, session auth.Session) {
	t.Helper()
	must(t, s.SignIn(t.Context(), i, admin, session))
}

// checkAuditRow checks row i of the log: its table and action, its principal, the keys of its before and after, and
// that it holds none of the hidden columns.
func checkAuditRow(t *testing.T, i int, row, want auditRow, table string, principal *uuid.UUID, hidden []string) {
	t.Helper()
	if row.Table != table || row.Action != want.Action {
		t.Errorf("row %d: got %s %s, want %s %s", i, row.Table, row.Action, table, want.Action)
	}
	if (principal == nil) != (row.Principal == nil) || (principal != nil && *principal != *row.Principal) {
		t.Errorf("row %d: got principal %v, want %v", i, row.Principal, principal)
	}
	if !slices.Equal(keys(row.Before), keys(want.Before)) {
		t.Errorf("row %d: before has %v, want %v", i, keys(row.Before), keys(want.Before))
	}
	if !slices.Equal(keys(row.After), keys(want.After)) {
		t.Errorf("row %d: after has %v, want %v", i, keys(row.After), keys(want.After))
	}
	for _, h := range hidden {
		if _, ok := row.Before[h]; ok {
			t.Errorf("row %d: before holds %s", i, h)
		}
		if _, ok := row.After[h]; ok {
			t.Errorf("row %d: after holds %s", i, h)
		}
	}
}

// TestAuditPrincipalIsLocal checks that a transaction's principal never outlives it on the pooled connection.
func TestAuditPrincipalIsLocal(t *testing.T) {
	s, pool := authStore(t)
	alice(t, s)
	var principal *string
	if err := pool.QueryRow(t.Context(), "SELECT nullif(current_setting('cobranch.principal_id', true), '')").Scan(&principal); err != nil {
		t.Fatal(err)
	}
	if principal != nil {
		t.Errorf("the connection still names principal %s", *principal)
	}
}
