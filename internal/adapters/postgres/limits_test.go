package postgres

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/google/uuid"

	"cobranch.dev/db/migrations"
	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/git"
	"cobranch.dev/internal/services/limits"
	"cobranch.dev/internal/services/orgs"
	"cobranch.dev/internal/services/repos"
)

// newOrganization is a new organization of slug.
func newOrganization(slug string) orgs.Organization {
	return orgs.Organization{ID: uuid.New(), Slug: slug, Name: slug, MembershipRoleID: auth.RoleMember, CreatedAt: now}
}

// exceeded is err's *limits.ExceededError, or nil.
func exceeded(err error) *limits.ExceededError {
	var e *limits.ExceededError
	if errors.As(err, &e) {
		return e
	}
	return nil
}

// checkExceeded fails the test unless err is want, nil for none.
func checkExceeded(t *testing.T, err error, want *limits.ExceededError) {
	t.Helper()
	got := exceeded(err)
	if (err == nil) != (want == nil) || (want != nil && (got == nil || *got != *want)) {
		t.Fatalf("Create() = %v, want %v", err, want)
	}
}

// createForNia has Olivia, an administrator here, create byAnAdmin organizations for Nia, and Nia create created more
// herself.
func (f *orgFixture) createForNia(t *testing.T, byAnAdmin, created int) {
	t.Helper()
	ctx := context.Background()
	for i := range byAnAdmin {
		if err := f.s.CreateFor(ctx, f.olivia, f.nia, newOrganization(fmt.Sprintf("for-nia-%d", i))); err != nil {
			t.Fatal(err)
		}
	}
	for i := range created {
		if err := f.s.Create(ctx, f.nia, newOrganization(fmt.Sprintf("by-nia-%d", i)), 0); err != nil {
			t.Fatal(err)
		}
	}
}

func TestOrgStoreCreateHeldToLimit(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		limit int64
		// byAnAdmin creates the organizations Nia owns before, for her, as Olivia.
		byAnAdmin int
		// created is how many Nia created herself before.
		created int
		want    *limits.ExceededError
	}{
		{name: "under the limit", limit: 2, created: 1},
		{name: "at the limit", limit: 2, created: 2, want: &limits.ExceededError{Kind: limits.KindOrganizations, Limit: 2, Used: 2, Adding: 1}},
		{name: "those an administrator created count against nobody", limit: 1, byAnAdmin: 2},
		{name: "no limit", limit: 0, created: 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newOrgFixture(t)
			f.createForNia(t, tt.byAnAdmin, tt.created)
			checkExceeded(t, f.s.Create(context.Background(), f.nia, newOrganization("initech"), tt.limit), tt.want)
			wantCreated := tt.created
			if tt.want == nil {
				wantCreated++
			}
			if n := count(t, f.pool, "SELECT count(*) FROM organizations WHERE created_by = $1", f.nia); n != wantCreated {
				t.Errorf("Nia created %d organizations, want %d", n, wantCreated)
			}
		})
	}
}

func TestOrgStoreCreateHeldToLimitAtOnce(t *testing.T) {
	t.Parallel()
	f := newOrgFixture(t) // Olivia created Acme
	ctx := context.Background()
	var wg sync.WaitGroup
	errs := make([]error, 4)
	for i := range errs {
		wg.Go(func() { errs[i] = f.s.Create(ctx, f.olivia, newOrganization(fmt.Sprintf("at-once-%d", i)), 2) })
	}
	wg.Wait()
	created, refused := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			created++
		case exceeded(err) != nil:
			refused++
		default:
			t.Errorf("Create() = %v", err)
		}
	}
	if created != 1 || refused != 3 {
		t.Errorf("%d created and %d refused at once, want 1 and 3", created, refused)
	}
}

func TestRepoStoreCreateHeldToLimit(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		limit int64
		want  *limits.ExceededError
	}{
		{name: "under the limit", limit: 3},
		{name: "at the limit", limit: 2, want: &limits.ExceededError{Kind: limits.KindRepositories, Limit: 2, Used: 2, Adding: 1}},
		{name: "no limit", limit: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newRepoFixture(t) // Acme holds website and secret
			tools := repos.Repository{ID: uuid.New(), OrgID: f.acme.ID, Slug: "tools", Name: "tools", ObjectFormat: "sha1", CreatedBy: f.olivia, CreatedAt: now}
			checkExceeded(t, f.r.Create(context.Background(), f.olivia, tools, tt.limit), tt.want)
			want := 3
			if tt.want != nil {
				want = 2
			}
			if n := count(t, f.pool, "SELECT count(*) FROM repositories WHERE org_id = $1", f.acme.ID); n != want {
				t.Errorf("Acme holds %d repositories, want %d", n, want)
			}
		})
	}
}

func TestLimitStore(t *testing.T) {
	t.Parallel()
	f := newGitFixture(t)
	ctx := context.Background()
	s := NewLimitStore(f.pool)
	if got, err := s.InstallationLimits(ctx); err != nil || got != (limits.Settings{}) {
		t.Errorf("InstallationLimits() = %+v, %v, want none set", got, err)
	}
	if _, err := f.pool.Exec(ctx, "UPDATE installation_settings SET organizations_per_user = 3, git_data_per_organization = 0"); err != nil {
		t.Fatal(err)
	}
	got, err := s.InstallationLimits(ctx)
	if err != nil || got.OrganizationsPerUser == nil || *got.OrganizationsPerUser != 3 || got.RepositoriesPerOrganization != nil ||
		got.GitDataPerOrganization == nil || *got.GitDataPerOrganization != 0 {
		t.Errorf("InstallationLimits() = %+v, %v, want 3 organizations per user and no Git data limit", got, err)
	}
	if got, err := s.OrganizationLimits(ctx, f.acme.ID); err != nil || got != (limits.Overrides{}) {
		t.Errorf("OrganizationLimits() = %+v, %v, want none of its own", got, err)
	}
	if _, err := f.pool.Exec(ctx, "INSERT INTO organization_limits (org_id, git_data) VALUES ($1, 5368709120)", f.acme.ID); err != nil {
		t.Fatal(err)
	}
	own, err := s.OrganizationLimits(ctx, f.acme.ID)
	if err != nil || own.Repositories != nil || own.GitData == nil || *own.GitData != 5<<30 {
		t.Errorf("OrganizationLimits() = %+v, %v, want 5 GiB of Git data", own, err)
	}
	for _, p := range []git.Push{
		pushOf(f.website.ID, f.olivia, "pack-1", []git.RefUpdate{{Name: "refs/heads/main", Old: git.ZeroSHA, New: shaA}}),
		pushOf(f.secret.ID, f.olivia, "pack-2", []git.RefUpdate{{Name: "refs/heads/main", Old: git.ZeroSHA, New: shaB}}),
	} {
		if err := f.g.Record(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.pool.Exec(ctx, "UPDATE packs SET state = 'superseded', superseded_at = now() WHERE repo_id = $1", f.secret.ID); err != nil {
		t.Fatal(err)
	}
	if used, err := s.GitData(ctx, f.acme.ID); err != nil || used != 100 {
		t.Errorf("GitData() = %d, %v, want 100, website's active pack alone", used, err)
	}
}

func TestGitStoreRecordHeldToGitDataLimit(t *testing.T) {
	t.Parallel()
	f := newGitFixture(t)
	ctx := context.Background()
	// Each pack holds 100 bytes; Acme may store 250, until an administrator lowers its limit to 150.
	pushUnder := func(limit int64, repo uuid.UUID, pack, branch string) git.Push {
		p := pushOf(repo, f.olivia, pack, []git.RefUpdate{{Name: "refs/heads/" + branch, Old: git.ZeroSHA, New: shaA}})
		p.OrgID, p.GitDataLimit = f.acme.ID, limit
		return p
	}
	push := func(repo uuid.UUID, pack, branch string) git.Push { return pushUnder(250, repo, pack, branch) }
	steps := []struct {
		name string
		push git.Push
		want string
	}{
		{name: "a first pack", push: push(f.website.ID, "pack-1", "main")},
		{name: "another repository's", push: push(f.secret.ID, "pack-2", "main")},
		{name: "one too many", push: push(f.secret.ID, "pack-3", "feature"),
			want: "This organization stores 200 B of Git data, and this push adds 100 B: over its limit of 250 B. Nothing changed; an administrator can raise the limit."},
		{name: "a pack the repository holds already adds nothing", push: push(f.website.ID, "pack-1", "again")},
		{name: "nor over a limit lowered below what Acme stores", push: pushUnder(150, f.website.ID, "pack-1", "lowered")},
		{name: "where a new pack is refused", push: pushUnder(150, f.website.ID, "pack-4", "new"),
			want: "This organization stores 200 B of Git data, and this push adds 100 B: over its limit of 150 B. Nothing changed; an administrator can raise the limit."},
	}
	for _, step := range steps {
		err := f.g.Record(ctx, step.push)
		var refused *git.RefusedError
		switch {
		case step.want == "" && err != nil:
			t.Errorf("%s: Record() = %v", step.name, err)
		case step.want != "" && (!errors.As(err, &refused) || refused.Message != step.want):
			t.Errorf("%s: Record() = %v, want %q", step.name, err, step.want)
		}
	}
	if n := count(t, f.pool, "SELECT count(*) FROM packs WHERE org_id = $1", f.acme.ID); n != 2 {
		t.Errorf("Acme lists %d packs, want pack-1 and pack-2", n)
	}
	if n := count(t, f.pool, "SELECT count(*) FROM refs WHERE repo_id = $1", f.secret.ID); n != 1 {
		t.Errorf("secret holds %d refs, want main alone: the refused push moved none", n)
	}
}

func TestGitStoreRecordHeldToGitDataLimitAtOnce(t *testing.T) {
	t.Parallel()
	f := newGitFixture(t)
	ctx := context.Background()
	// Two pushes into Acme's two repositories, 100 bytes each, under a limit of 150: one fits, whichever is first.
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, repo := range []uuid.UUID{f.website.ID, f.secret.ID} {
		p := pushOf(repo, f.olivia, fmt.Sprintf("pack-%d", i), []git.RefUpdate{{Name: "refs/heads/main", Old: git.ZeroSHA, New: shaA}})
		p.OrgID, p.GitDataLimit = f.acme.ID, 150
		wg.Go(func() { errs[i] = f.g.Record(ctx, p) })
	}
	wg.Wait()
	var refused *git.RefusedError
	if (errs[0] == nil) == (errs[1] == nil) || (!errors.As(errs[0], &refused) && !errors.As(errs[1], &refused)) {
		t.Errorf("Record() at once = %v and %v, want one recorded and the other refused", errs[0], errs[1])
	}
}

func TestLimitsMigrationNamesCreators(t *testing.T) {
	t.Parallel()
	f := newOrgFixture(t) // Olivia created Acme herself
	ctx := context.Background()
	globex := newOrganization("globex")
	if err := f.s.CreateFor(ctx, f.olivia, f.nia, globex); err != nil {
		t.Fatal(err)
	}
	schema := newSchema(t, f.pool, migrations.FS, migrations.Go...)
	// Down past the limits' migration and up again, as on an installation that had organizations before it.
	if _, err := schema.provider.DownTo(ctx, 25); err != nil {
		t.Fatal(err)
	}
	if _, err := schema.provider.Up(ctx); err != nil {
		t.Fatal(err)
	}
	creators := map[uuid.UUID]*uuid.UUID{}
	rows, err := f.pool.Query(ctx, "SELECT id, created_by FROM organizations")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id uuid.UUID
		var by *uuid.UUID
		if err := rows.Scan(&id, &by); err != nil {
			t.Fatal(err)
		}
		creators[id] = by
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if by := creators[f.acme.ID]; by == nil || *by != f.olivia {
		t.Errorf("Acme was created by %v, want Olivia, who created it for herself", by)
	}
	if by, ok := creators[globex.ID]; !ok || by != nil {
		t.Errorf("Globex was created by %v, want nobody: an administrator created it for Nia", by)
	}
}
