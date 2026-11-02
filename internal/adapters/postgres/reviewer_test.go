package postgres

import (
	"context"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"

	"cobranch.dev/db/migrations"
	"cobranch.dev/db/seeds"
	"cobranch.dev/internal/services/agents"
	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/orgs"
)

// TestOrgStoreCreateMakesReviewer checks that an organization is created with its AI reviewer, whoever creates it: a
// built-in agent nobody created, holding Automation at the organization and so reading, reviewing and posting in its
// repositories, but neither pushing nor managing.
func TestOrgStoreCreateMakesReviewer(t *testing.T) {
	t.Parallel()
	f := newOrgFixture(t)
	ctx := context.Background()
	globex := orgs.Organization{ID: uuid.New(), Slug: "globex", Name: "Globex", MembershipRoleID: auth.RoleMember, CreatedAt: now}
	if err := f.s.CreateFor(ctx, f.olivia, f.nia, globex); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name string
		org  uuid.UUID
	}{
		{name: "an organization its owner created", org: f.acme.ID},
		{name: "an organization created for its owner", org: globex.ID},
	} {
		t.Run(tt.name, func(t *testing.T) {
			list, err := NewAgentStore(f.pool).Agents(ctx, tt.org)
			if err != nil || len(list) != 1 {
				t.Fatalf("Agents() = %+v, %v; want the AI reviewer alone", list, err)
			}
			r := list[0]
			want := []agents.Grant{{RoleID: auth.RoleOrgAutomation, RoleName: "Automation"}}
			if r.Name != agents.ReviewerName || !r.BuiltIn || r.Purpose != agents.PurposeReview || r.CreatedBy != nil || !slices.Equal(r.Grants, want) {
				t.Errorf("the AI reviewer = %+v, want a built-in reviewer holding %+v", r, want)
			}
			held, err := f.s.Permissions(ctx, r.ID, auth.ScopeOrganization, tt.org)
			if err != nil || !slices.Contains(held, "org.read") || !slices.Contains(held, "reviews.submit") || slices.Contains(held, "repo.push") ||
				slices.Contains(held, "org.manage") {
				t.Errorf("the AI reviewer holds %v (%v), want Automation's permissions", held, err)
			}
			var n int
			if err := f.pool.QueryRow(ctx, "SELECT count(*) FROM events WHERE kind = 'agent.created' AND subject_id = $1 AND actor_id = $2", r.ID, f.olivia).Scan(&n); err != nil || n != 1 {
				t.Errorf("agent.created events of the AI reviewer by its organization's creator = %d, %v; want 1", n, err)
			}
		})
	}
}

// TestReviewerMigrationMakesReviewers checks that migration 00040 gives every organization already there its AI
// reviewer, under a name none of its agents has, and that rolling it back takes the reviewers away and keeps the
// agents people made.
func TestReviewerMigrationMakesReviewers(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testDatabase(t)
	schema := newSchema(t, pool, migrationsBefore(t, "00040"))
	if err := schema.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		agents []string
		want   string
	}{
		{name: "an organization without agents", want: "AI reviewer"},
		{name: "an organization with agents of other names", agents: []string{"Scout"}, want: "AI reviewer"},
		{name: "an organization whose agent has the name", agents: []string{"ai REVIEWER"}, want: "AI reviewer 2"},
		{name: "an organization whose agents have the next names too", agents: []string{"AI reviewer", "AI reviewer 2"}, want: "AI reviewer 3"},
	}
	orgIDs := make([]uuid.UUID, len(tests))
	for i, tt := range tests {
		orgIDs[i] = insertOrganizationWithAgents(ctx, t, pool, tt.agents)
	}
	full := newSchema(t, pool, migrations.FS, migrations.Go...)
	if err := full.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var name string
			if err := pool.QueryRow(ctx, "SELECT a.name FROM agents a JOIN grants g ON g.principal_id = a.principal_id AND g.role_id = $2 AND g.scope = 'organization' AND g.scope_id = a.org_id "+
				"WHERE a.org_id = $1 AND a.built_in AND a.purpose = 'review' AND a.created_by IS NULL", orgIDs[i], auth.RoleOrgAutomation).Scan(&name); err != nil || name != tt.want {
				t.Errorf("the AI reviewer = %q, %v; want %q holding Automation", name, err, tt.want)
			}
		})
	}
	if _, err := full.provider.DownTo(ctx, 39); err != nil {
		t.Fatal(err)
	}
	if n := count(t, pool, "SELECT count(*) FROM principals WHERE kind = 'agent'"); n != 4 {
		t.Errorf("%d agents after rolling back, want the 4 people made", n)
	}
	if n := count(t, pool, "SELECT count(*) FROM roles WHERE id = $1", auth.RoleOrgAutomation); n != 0 {
		t.Errorf("%d roles of Automation at an organization after rolling back, want none", n)
	}
}

// TestReviewerMigrationRollsBack checks that rolling migration 00040 back leaves nobody who held Automation at an
// organization outside it: whoever held it alone there holds Member, the organization's members join as Member, and
// its pending invitations to Automation invite to Member.
func TestReviewerMigrationRollsBack(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testDatabase(t)
	full := newSchema(t, pool, migrations.FS, migrations.Go...)
	if err := full.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	org := insertOrganizationWithAgents(ctx, t, pool, nil)
	exec(ctx, t, pool, "UPDATE organizations SET membership_role_id = $2 WHERE id = $1", org, auth.RoleOrgAutomation)
	tests := []struct {
		name  string
		roles []uuid.UUID
		want  []uuid.UUID
	}{
		{name: "a member who joined as Automation", roles: []uuid.UUID{auth.RoleOrgAutomation}, want: []uuid.UUID{auth.RoleMember}},
		{name: "a Guest who also held Automation", roles: []uuid.UUID{auth.RoleGuest, auth.RoleOrgAutomation}, want: []uuid.UUID{auth.RoleGuest}},
		{name: "a member who never held it", roles: []uuid.UUID{auth.RoleGuest}, want: []uuid.UUID{auth.RoleGuest}},
	}
	users := make([]uuid.UUID, len(tests))
	for i, tt := range tests {
		users[i] = uuid.New()
		exec(ctx, t, pool, "INSERT INTO principals (id, kind, display_name) VALUES ($1, 'user', 'x')", users[i])
		exec(ctx, t, pool, "INSERT INTO memberships (org_id, user_id) VALUES ($1, $2)", org, users[i])
		for _, role := range tt.roles {
			exec(ctx, t, pool, "INSERT INTO grants (principal_id, role_id, scope, scope_id, source) VALUES ($1, $2, 'organization', $3, 'grant')", users[i], role, org)
		}
	}
	for _, state := range []string{"pending", "declined"} {
		exec(ctx, t, pool, "INSERT INTO invitations (id, org_id, email, role_id, invited_by, state, expires_at) VALUES ($1, $2, $3, $4, $5, $6, now() + interval '1 day')",
			uuid.New(), org, state+"@example.com", auth.RoleOrgAutomation, users[0], state)
	}
	if _, err := full.provider.DownTo(ctx, 39); err != nil {
		t.Fatal(err)
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rows, err := pool.Query(ctx, "SELECT role_id FROM grants WHERE principal_id = $1 AND scope = 'organization' AND scope_id = $2 ORDER BY role_id", users[i], org)
			if err != nil {
				t.Fatal(err)
			}
			if got, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID]); err != nil || !slices.Equal(got, tt.want) {
				t.Errorf("roles after rolling back = %v, %v; want %v", got, err, tt.want)
			}
		})
	}
	if n := count(t, pool, "SELECT count(*) FROM organizations WHERE id = $1 AND membership_role_id = $2", org, auth.RoleMember); n != 1 {
		t.Errorf("the organization's membership role isn't Member after rolling back")
	}
	if n := count(t, pool, "SELECT count(*) FROM invitations WHERE org_id = $1 AND (state = 'pending') = (role_id = $2)", org, auth.RoleMember); n != 2 {
		t.Errorf("%d invitations as expected after rolling back, want the pending one to Member and the declined one as it was", n)
	}
}

// exec runs statement on pool's database, or fails the test.
func exec(ctx context.Context, t *testing.T, pool *pgxpool.Pool, statement string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(ctx, statement, args...); err != nil {
		t.Fatal(err)
	}
}

// insertOrganizationWithAgents inserts an organization with an agent of each of names, as migration 00032 keeps them,
// and returns its ID.
func insertOrganizationWithAgents(ctx context.Context, t *testing.T, pool *pgxpool.Pool, names []string) uuid.UUID {
	t.Helper()
	org := uuid.New()
	if _, err := pool.Exec(ctx, "INSERT INTO organizations (id, slug, name, membership_role_id) VALUES ($1, $2, 'x', $3)", org, "org-"+org.String()[:8], auth.RoleMember); err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		id := uuid.New()
		if _, err := pool.Exec(ctx, "INSERT INTO principals (id, kind, display_name, org_id) VALUES ($1, 'agent', $2, $3)", id, name, org); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, "INSERT INTO agents (principal_id, org_id, name) VALUES ($1, $2, $3)", id, org, name); err != nil {
			t.Fatal(err)
		}
	}
	return org
}

// TestAgentStoreAssignReview checks that a task carries the commit it works at, and that a review task names a branch
// and its head, or isn't recorded.
func TestAgentStoreAssignReview(t *testing.T) {
	t.Parallel()
	f := newAgentFixture(t)
	ctx := context.Background()
	list, err := f.a.Agents(ctx, f.acme.ID)
	if err != nil || len(list) == 0 || list[0].Purpose != agents.PurposeReview {
		t.Fatalf("Agents() = %+v, %v; want the AI reviewer first", list, err)
	}
	reviewer := list[0]
	tests := []struct {
		name     string
		kind     string
		onBranch bool
		sha      string
		wantErr  bool
	}{
		{name: "a review of the branch's head", kind: agents.KindReview, onBranch: true, sha: f.feature.Head},
		{name: "a review without a head", kind: agents.KindReview, onBranch: true, wantErr: true},
		{name: "a review of no branch", kind: agents.KindReview, sha: f.feature.Head, wantErr: true},
		{name: "a review of something but a commit", kind: agents.KindReview, onBranch: true, sha: "main", wantErr: true},
		{name: "an assigned task at a commit", kind: agents.KindAssigned, onBranch: true, sha: f.feature.Head},
		{name: "an assigned task at none", kind: agents.KindAssigned},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			task := agents.Task{ID: v7(), OrgID: f.acme.ID, Agent: agents.Principal{ID: reviewer.ID}, Kind: tt.kind,
				AssignedBy: &agents.Principal{ID: reviewer.ID}, Title: "Review", SHA: tt.sha}
			if tt.onBranch {
				task.RepoID, task.BranchID = f.website.ID, f.feature.BranchID
			}
			got, err := f.a.Assign(ctx, reviewer.ID, task)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Assign() = %v, want an error %t", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			if got.SHA != tt.sha || got.Kind != tt.kind || !got.ByCobranch() {
				t.Errorf("Assign() = %+v, want a task of Cobranch's of kind %s at %q", got, tt.kind, tt.sha)
			}
			if read, err := f.a.Task(ctx, f.acme.ID, got.Number); err != nil || read.SHA != tt.sha {
				t.Errorf("Task() = %+v, %v; want it at %q", read, err, tt.sha)
			}
		})
	}
}

// TestReviewerSeed checks that seed 12 gives each demo organization one AI reviewer holding Automation: its own on a
// fresh demo installation, where the seeds make the organizations after the schema's migrations, and the one migration
// 00040 made on a demo installation upgraded in place, without a second.
func TestReviewerSeed(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name     string
		upgraded bool
		wantIDs  int
	}{
		{name: "a fresh demo installation", wantIDs: 2},
		{name: "a demo installation upgraded in place", upgraded: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			pool := testDatabase(t)
			if tt.upgraded {
				if err := newSchema(t, pool, migrationsBefore(t, "00040")).Migrate(context.Background()); err != nil {
					t.Fatal(err)
				}
				seed(t, pool, slices.DeleteFunc(slices.Clone(seeds.Go), func(m *goose.Migration) bool { return m.Version >= 12 }))
			}
			if err := newSchema(t, pool, migrations.FS, migrations.Go...).Migrate(context.Background()); err != nil {
				t.Fatal(err)
			}
			seed(t, pool, seeds.Go)
			var reviewers, seeded int
			if err := pool.QueryRow(context.Background(), "SELECT count(*), count(*) FILTER (WHERE a.principal_id::text LIKE '00000000-0000-7000-8000-00000000d7%') FROM organizations o "+
				"JOIN agents a ON a.org_id = o.id AND a.purpose = 'review' JOIN grants g ON g.principal_id = a.principal_id AND g.role_id = $1 AND g.scope_id = o.id",
				auth.RoleOrgAutomation).Scan(&reviewers, &seeded); err != nil || reviewers != 2 || seeded != tt.wantIDs {
				t.Errorf("reviewers holding Automation = %d, %d of them the seed's (%v); want one for each of the 2 demo organizations, %d the seed's",
					reviewers, seeded, err, tt.wantIDs)
			}
		})
	}
}
