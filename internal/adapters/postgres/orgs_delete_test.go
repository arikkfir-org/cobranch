package postgres

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"cobranch.dev/internal/services/agents"
	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/git"
	"cobranch.dev/internal/services/orgs"
	"cobranch.dev/internal/services/repos"
)

// deletionFixture is agentFixture with what deleting Acme must take besides: its former slug acme, now that it is
// acme-corp, Scout's task, the key Deploy, Nia's join request and her invitation; and Globex, which Max created and
// keeps.
type deletionFixture struct {
	*agentFixture
	o       *OrgStore
	version int64
	task    agents.Task
	key     orgs.APIKey
	hash    []byte
	globex  orgs.Organization
}

func newDeletionFixture(t *testing.T) *deletionFixture {
	t.Helper()
	f := &deletionFixture{agentFixture: newAgentFixture(t)}
	f.o = f.orgFixture.s
	ctx := context.Background()
	renamed := f.acme
	renamed.Slug = "acme-corp"
	var err error
	if f.version, err = f.o.Update(ctx, f.olivia, renamed, orgs.AnyVersion); err != nil {
		t.Fatal(err)
	}
	f.task = f.assigned(t, "Look around", false, uuid.Nil)
	f.key, f.hash = deployKey(t, f.orgFixture)
	req := orgs.JoinRequest{ID: uuid.New(), OrgID: f.acme.ID, UserID: f.nia, Note: "hi", CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
	if err := f.o.Request(ctx, f.nia, req); err != nil {
		t.Fatal(err)
	}
	inv := orgs.Invitation{ID: uuid.New(), OrgID: f.acme.ID, Email: "nia@example.com", RoleID: auth.RoleGuest, InvitedBy: f.olivia,
		State: orgs.StatePending, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
	if err := f.o.Invite(ctx, f.olivia, inv); err != nil {
		t.Fatal(err)
	}
	f.globex = orgs.Organization{ID: uuid.New(), Slug: "globex", Name: "Globex", MembershipRoleID: auth.RoleMember, CreatedAt: now}
	if err := f.o.Create(ctx, f.max, f.globex, 0); err != nil {
		t.Fatal(err)
	}
	return f
}

// TestOrgStoreDeletes deletes Acme, as Olivia, its Owner, at its version, then purges it.
func TestOrgStoreDeletes(t *testing.T) {
	t.Parallel()
	f := newDeletionFixture(t)
	ctx := context.Background()
	if f.version != 2 {
		t.Fatalf("Update() = version %d, want 2", f.version)
	}
	if err := f.o.Delete(ctx, f.olivia, f.acme.ID, 1, time.Now()); !errors.Is(err, orgs.ErrChanged) {
		t.Fatalf("Delete() of version 1 = %v, want %v", err, orgs.ErrChanged)
	}
	if err := f.o.Delete(ctx, f.olivia, f.acme.ID, f.version, time.Now()); err != nil {
		t.Fatalf("Delete() = %v", err)
	}
	if err := f.o.Delete(ctx, f.olivia, f.acme.ID, orgs.AnyVersion, time.Now()); !errors.Is(err, orgs.ErrNotFound) {
		t.Errorf("Delete() again = %v, want %v", err, orgs.ErrNotFound)
	}
	f.checkDeleting(t)
	f.checkAccessGone(t)
	f.checkSlugsFree(t)
	due, err := f.o.Purgeable(ctx, time.Now().Add(-orgs.PurgeWait))
	if err != nil || len(due) != 1 || due[0].ID != f.acme.ID || due[0].LiveEnvironments != 0 {
		t.Fatalf("Purgeable() = %+v, %v, want Acme at once: it has no environments", due, err)
	}
	if purged, err := f.o.Purge(ctx, f.acme.ID); !purged || err != nil {
		t.Fatalf("Purge() = %t, %v", purged, err)
	}
	if purged, err := f.o.Purge(ctx, f.acme.ID); purged || err != nil {
		t.Errorf("Purge() again = %t, %v, want false", purged, err)
	}
	checkPurged(t, f.pool, f.acme.ID, f.website.ID, f.secret.ID)
	f.checkKept(t)
}

// checkDeleting checks that Acme is Deleting for Olivia, which the audit log says, and gone for everyone.
func (f *deletionFixture) checkDeleting(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	if n := count(t, f.pool, "SELECT count(*) FROM organizations WHERE id = $1 AND deleting_at IS NOT NULL AND deleted_by = $2", f.acme.ID, f.olivia); n != 1 {
		t.Errorf("Acme's rows Deleting by Olivia = %d, want 1", n)
	}
	if n := f.deletionsLogged(t); n != 1 {
		t.Errorf("audit log rows of Olivia deleting Acme = %d, want 1", n)
	}
	if _, err := f.o.ByID(ctx, f.acme.ID); !errors.Is(err, orgs.ErrNotFound) {
		t.Errorf("ByID() = %v, want %v", err, orgs.ErrNotFound)
	}
	for _, slug := range []string{"acme", "acme-corp"} {
		if _, err := f.o.BySlug(ctx, slug); !errors.Is(err, orgs.ErrNotFound) {
			t.Errorf("BySlug(%s) = %v, want %v", slug, err, orgs.ErrNotFound)
		}
	}
	if _, err := f.o.Update(ctx, f.olivia, f.acme, orgs.AnyVersion); !errors.Is(err, orgs.ErrNotFound) {
		t.Errorf("Update() = %v, want %v", err, orgs.ErrNotFound)
	}
	if n, err := f.o.Created(ctx, f.olivia); n != 0 || err != nil {
		t.Errorf("Created(Olivia) = %d, %v, want 0", n, err)
	}
	if n := count(t, f.pool, "SELECT count(*) FROM agent_tasks WHERE id = $1 AND state = 'cancelled' AND reason = $2 AND cancelled_by = $3",
		f.task.ID, deletedReason, f.olivia); n != 1 {
		t.Errorf("Scout's task cancelled by Olivia's deletion = %d, want 1", n)
	}
}

// checkAccessGone checks that nobody reaches Acme any more: its memberships, invitations, join requests and the
// grants there of its people are gone, and so are its key's secrets, while Scout and Deploy keep their grants, which
// nothing acts with, until the purge.
func (f *deletionFixture) checkAccessGone(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	for _, table := range []string{"memberships", "invitations", "join_requests"} {
		if n := count(t, f.pool, "SELECT count(*) FROM "+table+" WHERE org_id = $1", f.acme.ID); n != 0 {
			t.Errorf("Acme's %s = %d, want none", table, n)
		}
	}
	if n := count(t, f.pool, `SELECT count(*) FROM grants WHERE principal_id = ANY($1)
		AND (scope_id = $2 OR scope_id IN (SELECT id FROM repositories WHERE org_id = $2))`, []uuid.UUID{f.olivia, f.max}, f.acme.ID); n != 0 {
		t.Errorf("Olivia's and Max's grants at Acme = %d, want none", n)
	}
	if n := count(t, f.pool, "SELECT count(*) FROM grants WHERE principal_id = ANY($1)", []uuid.UUID{f.scout.ID, f.key.ID}); n != 3 {
		t.Errorf("Scout's and Deploy's grants = %d, want their 3", n)
	}
	if n := count(t, f.pool, "SELECT count(*) FROM credentials WHERE principal_id = $1", f.key.ID); n != 0 {
		t.Errorf("Deploy's secrets = %d, want none", n)
	}
	if _, _, err := NewAuthStore(f.pool).Credential(ctx, f.hash); err == nil {
		t.Error("Credential() of Deploy's secret found it, want none")
	}
	held, err := f.o.Permissions(ctx, f.max, auth.ScopeOrganization, f.globex.ID)
	if err != nil || !slices.Contains(held, "org.manage") {
		t.Errorf("Max holds %v (%v) at Globex, want its Owner's permissions", held, err)
	}
	if mine, err := f.o.OrganizationsOf(ctx, f.max); err != nil || len(mine) != 1 || mine[0].ID != f.globex.ID {
		t.Errorf("OrganizationsOf(Max) = %+v, %v, want Globex", mine, err)
	}
}

// checkSlugsFree has Nia take Acme's former slug and Max its last.
func (f *deletionFixture) checkSlugsFree(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	for _, o := range []struct {
		by   uuid.UUID
		slug string
	}{{f.nia, "acme"}, {f.max, "acme-corp"}} {
		org := orgs.Organization{ID: uuid.New(), Slug: o.slug, Name: "New", MembershipRoleID: auth.RoleMember, CreatedAt: now}
		if err := f.o.Create(ctx, o.by, org, 0); err != nil {
			t.Errorf("Create(%s) = %v, want Acme's slug free", o.slug, err)
		}
	}
}

// checkKept checks that the purge kept the people, Globex and the organizations that took Acme's slugs, and the audit
// log's row of who deleted it.
func (f *deletionFixture) checkKept(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	if n := count(t, f.pool, "SELECT count(*) FROM principals WHERE id = ANY($1)", []uuid.UUID{f.olivia, f.max, f.nia}); n != 3 {
		t.Errorf("people after the purge = %d, want Olivia, Max and Nia", n)
	}
	for _, slug := range []string{"globex", "acme", "acme-corp"} {
		if org, err := f.o.BySlug(ctx, slug); err != nil || org.ID == f.acme.ID {
			t.Errorf("BySlug(%s) after the purge = %+v, %v, want a live organization", slug, org, err)
		}
	}
	if n := f.deletionsLogged(t); n != 1 {
		t.Errorf("audit log rows of Olivia deleting Acme after the purge = %d, want 1", n)
	}
}

// deletionsLogged counts the audit log's rows of Olivia making Acme Deleting.
func (f *deletionFixture) deletionsLogged(t *testing.T) int {
	t.Helper()
	return count(t, f.pool, `SELECT count(*) FROM audit_log WHERE table_name = 'organizations' AND action = 'update' AND principal_id = $1
		AND before->>'id' = $2 AND after->>'deleted_by' = $1::text AND after->>'deleting_at' IS NOT NULL`, f.olivia, f.acme.ID.String())
}

// checkPurged checks that no row of any table names the organization, or any of the repositories, by its org_id or
// repo_id.
func checkPurged(t *testing.T, pool *pgxpool.Pool, orgID uuid.UUID, repoIDs ...uuid.UUID) {
	t.Helper()
	rows, err := pool.Query(context.Background(), `SELECT c.table_name::text, c.column_name::text FROM information_schema.columns c
		JOIN information_schema.tables t ON t.table_schema = c.table_schema AND t.table_name = c.table_name
		WHERE c.table_schema = current_schema() AND t.table_type = 'BASE TABLE' AND c.column_name IN ('org_id', 'repo_id') AND c.data_type = 'uuid'
		ORDER BY 1, 2`)
	if err != nil {
		t.Fatal(err)
	}
	type column struct{ table, name string }
	columns, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (column, error) {
		var c column
		return c, r.Scan(&c.table, &c.name)
	})
	if err != nil || len(columns) < 10 {
		t.Fatalf("org_id and repo_id columns = %d, %v", len(columns), err)
	}
	ids := append([]uuid.UUID{orgID}, repoIDs...)
	for _, c := range columns {
		query := fmt.Sprintf("SELECT count(*) FROM %s WHERE %s = ANY($1)", pgx.Identifier{c.table}.Sanitize(), pgx.Identifier{c.name}.Sanitize())
		if n := count(t, pool, query, ids); n != 0 {
			t.Errorf("%s rows by %s after the purge = %d, want none", c.table, c.name, n)
		}
	}
}

// TestOrgStoreDeletesTearingDown deletes Acme while website's production holds what main delivered and its preview
// waits for a member, both of which its schedule then tears down, the one failing: Acme may be purged once both
// teardowns ended, or once it waited long enough.
func TestOrgStoreDeletesTearingDown(t *testing.T) {
	t.Parallel()
	f := newDeliveryFixture(t)
	ctx := context.Background()
	f.deletingWithEnvironments(t)
	if due, err := f.s.Purgeable(ctx, time.Now().Add(-orgs.PurgeWait)); err != nil || len(due) != 0 {
		t.Errorf("Purgeable() = %+v, %v, want none: its environments live", due, err)
	}
	if due, err := f.s.Purgeable(ctx, time.Now().Add(time.Hour)); err != nil || len(due) != 1 || due[0].LiveEnvironments != 2 {
		t.Errorf("Purgeable() past the wait = %+v, %v, want Acme with its 2 environments", due, err)
	}
	if n, err := f.d.Schedule(ctx, time.Hour); n != 2 || err != nil {
		t.Fatalf("Schedule() = %d, %v, want both teardowns, the paused production's too", n, err)
	}
	if n, err := f.d.Schedule(ctx, time.Nanosecond); n != 0 || err != nil {
		t.Errorf("Schedule() again = %d, %v, want none: the teardowns are queued", n, err)
	}
	for i := range 2 {
		f.tearDown(t, i == 0)
	}
	if n, err := f.d.Schedule(ctx, time.Nanosecond); n != 0 || err != nil {
		t.Errorf("Schedule() after the teardowns = %d, %v, want none: each environment had its teardown", n, err)
	}
	due, err := f.s.Purgeable(ctx, time.Now().Add(-orgs.PurgeWait))
	if err != nil || len(due) != 1 || due[0].ID != f.acme.ID || due[0].LiveEnvironments != 1 {
		t.Fatalf("Purgeable() = %+v, %v, want Acme with the environment its failed teardown left", due, err)
	}
	if purged, err := f.s.Purge(ctx, f.acme.ID); !purged || err != nil {
		t.Fatalf("Purge() = %t, %v", purged, err)
	}
	checkPurged(t, f.pool, f.acme.ID, f.website.ID, f.secret.ID)
}

// deletingWithEnvironments pushes main, whose job queues a run and whose delivery to production succeeds, and Nia's
// feature/login, whose delivery to its preview waits for a member; pauses production, has the Git server record Nia's
// feature/signup, and deletes Acme, which cancels the run and the waiting delivery before the push worker gets to
// feature/signup, which then starts nothing.
func (f *deliveryFixture) deletingWithEnvironments(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	repo := f.website.ID
	f.push(t, pushOf(repo, f.olivia, "pack-1", []git.RefUpdate{{Name: "refs/heads/main", Old: git.ZeroSHA, New: shaA}}, commit(shaA, person("Olivia", "olivia@example.com"))),
		tip(t, "main", "blob-0", jobsFile+deliveryFile))
	f.push(t, pushOf(repo, f.nia, "pack-2", []git.RefUpdate{{Name: "refs/heads/feature/login", Old: git.ZeroSHA, New: shaB}}, commit(shaB, person("Nia", "nia@example.com"), shaA)),
		tip(t, "feature/login", "blob-1", deliveryFile))
	envs, err := f.c.Environments(ctx, repo, "")
	if err != nil || len(envs) != 2 {
		t.Fatalf("Environments() = %+v, %v", envs, err)
	}
	production := envs[1]
	f.deliverOnce(t, code.DeliverySucceeded)
	if _, err := f.c.PauseEnvironment(ctx, f.max, repo, production.ID); err != nil {
		t.Fatal(err)
	}
	if n := count(t, f.pool, "SELECT count(*) FROM job_runs WHERE repo_id = $1 AND state = 'queued'", repo); n != 1 {
		t.Fatalf("queued runs = %d, want main's", n)
	}
	signup := pushOf(repo, f.nia, "pack-3", []git.RefUpdate{{Name: "refs/heads/feature/signup", Old: git.ZeroSHA, New: shaC}}, commit(shaC, person("Nia", "nia@example.com"), shaA))
	signup.Settings = []git.TipSettings{tip(t, "feature/signup", "blob-2", jobsFile+deliveryFile)}
	if err := f.g.Record(ctx, signup); err != nil {
		t.Fatal(err)
	}
	if err := f.s.Delete(ctx, f.olivia, f.acme.ID, orgs.AnyVersion, time.Now()); err != nil {
		t.Fatalf("Delete() = %v", err)
	}
	f.checkNothingStarts(t, envs[0])
	if n := count(t, f.pool, "SELECT count(*) FROM job_runs WHERE repo_id = $1 AND state = 'cancelled' AND reason = $2 AND ended_at IS NOT NULL", repo, deletedReason); n != 1 {
		t.Errorf("runs cancelled by the deletion = %d, want main's", n)
	}
	want := []string{"#1 production main@a succeeded", "#2 previews feature/login@b cancelled: " + deletedReason}
	if got := f.history(t, repo); !slices.Equal(got, want) {
		t.Errorf("deliveries = %q, want %q", got, want)
	}
}

// checkNothingStarts checks that once Acme is being deleted, nothing starts work there but its teardowns: the push
// worker processes feature/signup's push without queueing its run or its preview, and feature/login's preview can't be
// redelivered.
func (f *deliveryFixture) checkNothingStarts(t *testing.T, preview code.Environment) {
	t.Helper()
	ctx := context.Background()
	repo := f.website.ID
	if n := f.process(t); n != 1 {
		t.Errorf("pushes processed after the deletion = %d, want feature/signup's", n)
	}
	if n := count(t, f.pool, "SELECT count(*) FROM pushes WHERE repo_id = $1 AND processed_at IS NULL", repo); n != 0 {
		t.Errorf("pushes waiting = %d, want none", n)
	}
	if n := count(t, f.pool, "SELECT count(*) FROM job_runs WHERE repo_id = $1 AND ended_at IS NULL", repo); n != 0 {
		t.Errorf("runs that haven't ended = %d, want none: feature/signup's job queues no run", n)
	}
	if envs, err := f.c.Environments(ctx, repo, ""); err != nil || len(envs) != 2 {
		t.Errorf("Environments() = %+v, %v, want production and feature/login's preview only", envs, err)
	}
	if _, err := f.c.Redeliver(ctx, f.max, repo, preview.ID); !errors.Is(err, repos.ErrNotFound) {
		t.Errorf("Redeliver() = %v, want repos.ErrNotFound", err)
	}
}

// tearDown claims a teardown and takes it to its end, failing with what it held when fail says so.
func (f *deliveryFixture) tearDown(t *testing.T, fail bool) {
	t.Helper()
	ctx := context.Background()
	d := f.claim(t)
	if d.Kind != code.Teardown {
		t.Fatalf("Claim() = %+v, want a teardown", d)
	}
	if marked, err := f.d.MarkDeleting(ctx, d.ID); !marked || err != nil {
		t.Fatalf("MarkDeleting() = %t, %v", marked, err)
	}
	td, ok, err := f.d.TakeTeardown(ctx, d.ID, time.Now().Add(time.Hour))
	if err != nil || !ok {
		t.Fatalf("TakeTeardown() = %t, %v", ok, err)
	}
	state, reason := code.DeliverySucceeded, ""
	if fail {
		state, reason = code.DeliveryFailed, "the cluster refused"
	}
	if err := f.d.EndTeardown(ctx, td, td.Inventory, state, reason); err != nil {
		t.Fatalf("EndTeardown() = %v", err)
	}
}
