package postgres

import (
	"context"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"cobranch.dev/db/migrations"
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
