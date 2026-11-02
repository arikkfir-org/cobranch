package postgres

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"
	"testing"

	"github.com/google/uuid"

	"cobranch.dev/db/migrations"
	"cobranch.dev/internal/services/admin"
	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/git"
	"cobranch.dev/internal/services/limits"
	"cobranch.dev/internal/services/orgs"
	"cobranch.dev/internal/services/repos"
)

// of points at n.
func of(n int64) *int64 { return &n }

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
		{name: "over a limit lowered below what she created", limit: 1, created: 3, want: &limits.ExceededError{Kind: limits.KindOrganizations, Limit: 1, Used: 3, Adding: 1}},
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
		{name: "over a limit lowered below what Acme holds", limit: 1, want: &limits.ExceededError{Kind: limits.KindRepositories, Limit: 1, Used: 2, Adding: 1}},
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

func TestOrgStoreInstallationLimits(t *testing.T) {
	t.Parallel()
	f := newOrgFixture(t)
	ctx := context.Background()
	stored, err := f.s.InstallationSettings(ctx)
	if err != nil || stored.Limits != (limits.Settings{}) {
		t.Fatalf("InstallationSettings() = %+v, %v; want no limits set", stored, err)
	}
	none, five := int64(0), int64(5<<30)
	stored.Limits = limits.Settings{RepositoriesPerOrganization: &none, GitDataPerOrganization: &five}
	written, err := f.s.ChangeInstallationSettings(ctx, f.olivia, stored, now)
	if err != nil || written.Version != 2 || written.Limits.OrganizationsPerUser != nil || *written.Limits.RepositoriesPerOrganization != 0 ||
		*written.Limits.GitDataPerOrganization != 5<<30 {
		t.Fatalf("ChangeInstallationSettings() = %+v, %v; want no repositories limit and 5 GiB at version 2", written, err)
	}
	if got, err := NewLimitStore(f.pool).InstallationLimits(ctx); err != nil || got.GitDataPerOrganization == nil || *got.GitDataPerOrganization != 5<<30 {
		t.Errorf("InstallationLimits() = %+v, %v; want the Back-office's 5 GiB", got, err)
	}
	log := auditLog(t, f.pool, "installation_settings")
	if len(log) != 1 || !slices.Equal(keys(log[0].After), []string{"git_data_per_organization", "repositories_per_organization", "updated_at", "updated_by", "version"}) {
		t.Errorf("audit log = %+v, want the limits' change", log)
	}
}

func TestOrgStoreOwnLimits(t *testing.T) {
	t.Parallel()
	f := newOrgFixture(t)
	ctx := context.Background()
	if got, err := f.s.OwnLimits(ctx, f.acme.ID); err != nil || got != (admin.OwnLimits{}) {
		t.Fatalf("OwnLimits() = %+v, %v; want none, at version 0", got, err)
	}
	steps := []struct {
		name        string
		own         admin.OwnLimits
		wantErr     error
		wantVersion int64
	}{
		{name: "the first", own: admin.OwnLimits{Overrides: limits.Overrides{Repositories: of(500)}}, wantVersion: 1},
		{name: "another first, meanwhile", own: admin.OwnLimits{Overrides: limits.Overrides{Repositories: of(7)}}, wantErr: admin.ErrStale, wantVersion: 1},
		{name: "a change", own: admin.OwnLimits{Overrides: limits.Overrides{GitData: of(0)}, Version: 1}, wantVersion: 2},
		{name: "a change from an old version", own: admin.OwnLimits{Version: 1}, wantErr: admin.ErrStale, wantVersion: 2},
	}
	for _, step := range steps {
		got, err := f.s.ChangeOwnLimits(ctx, f.olivia, f.acme.ID, step.own, now)
		if !errors.Is(err, step.wantErr) {
			t.Fatalf("%s: ChangeOwnLimits() error = %v, want %v", step.name, err, step.wantErr)
		}
		stored, err := f.s.OwnLimits(ctx, f.acme.ID)
		if err != nil || stored.Version != step.wantVersion {
			t.Fatalf("%s: OwnLimits() = %+v, %v; want version %d", step.name, stored, err, step.wantVersion)
		}
		if step.wantErr == nil && (!sameOwn(got, stored) || !sameOwn(got, step.own) || got.UpdatedBy != f.olivia || !got.UpdatedAt.Equal(now)) {
			t.Errorf("%s: ChangeOwnLimits() = %+v, want %+v, by Olivia", step.name, got, stored)
		}
	}
	if got, err := f.s.OwnLimits(ctx, f.acme.ID); err != nil || got.Repositories != nil || got.GitData == nil || *got.GitData != 0 {
		t.Errorf("OwnLimits() = %+v, %v; want no Git data limit, and the installation's repositories", got, err)
	}
	if n := count(t, f.pool, "SELECT count(*) FROM events WHERE kind = 'org.limits_changed' AND org_id = $1 AND actor_id = $2", f.acme.ID, f.olivia); n != 2 {
		t.Errorf("Acme's trail records %d changes of its limits, want 2", n)
	}
	if log := auditLog(t, f.pool, "organization_limits"); len(log) != 2 || log[0].Action != "insert" || log[1].Action != "update" {
		t.Errorf("audit log = %+v, want the insert and the update", log)
	}
	if _, err := f.s.ChangeOwnLimits(ctx, f.olivia, uuid.New(), admin.OwnLimits{}, now); !errors.Is(err, orgs.ErrNotFound) {
		t.Errorf("ChangeOwnLimits() of an organization that isn't there = %v, want ErrNotFound", err)
	}
}

// sameOwn reports whether a and b set the same limits.
func sameOwn(a, b admin.OwnLimits) bool {
	same := func(x, y *int64) bool { return (x == nil) == (y == nil) && (x == nil || *x == *y) }
	return same(a.Repositories, b.Repositories) && same(a.GitData, b.GitData)
}

func TestOrgStoreAllOrganizationsUsage(t *testing.T) {
	t.Parallel()
	f := newGitFixture(t)
	ctx := context.Background()
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
	if _, err := f.s.ChangeOwnLimits(ctx, f.olivia, f.acme.ID, admin.OwnLimits{Overrides: limits.Overrides{GitData: of(1 << 20)}}, now); err != nil {
		t.Fatal(err)
	}
	got, err := f.s.AllOrganizations(ctx, admin.Page{Limit: 10})
	if err != nil || len(got) != 1 {
		t.Fatalf("AllOrganizations() = %+v, %v; want Acme", got, err)
	}
	if acme := got[0]; acme.Usage != (limits.Usage{Repositories: 2, GitData: 100}) || acme.Own.Repositories != nil || acme.Own.GitData == nil || *acme.Own.GitData != 1<<20 {
		t.Errorf("Acme = %+v, want 2 repositories, website's 100 bytes, and its own 1 MiB of Git data", acme)
	}
}

func TestOrgStoreCreated(t *testing.T) {
	t.Parallel()
	f := newOrgFixture(t)
	ctx := context.Background()
	f.createForNia(t, 2, 3)
	if _, err := f.pool.Exec(ctx, "DELETE FROM organizations WHERE slug = 'by-nia-0'"); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name string
		user uuid.UUID
		want int64
	}{
		{name: "those Nia created herself that still exist", user: f.nia, want: 2},
		{name: "none for someone who created none", user: f.max, want: 0},
	} {
		if got, err := f.s.Created(ctx, tt.user); err != nil || got != tt.want {
			t.Errorf("%s: Created() = %d, %v, want %d", tt.name, got, err, tt.want)
		}
	}
}

func TestRepoStoreUsage(t *testing.T) {
	t.Parallel()
	f := newGitFixture(t)
	ctx := context.Background()
	// Each pack holds 100 bytes: website lists two active ones, and secret one that compaction superseded.
	for _, p := range []git.Push{
		pushOf(f.website.ID, f.olivia, "pack-1", []git.RefUpdate{{Name: "refs/heads/main", Old: git.ZeroSHA, New: shaA}}),
		pushOf(f.website.ID, f.olivia, "pack-2", []git.RefUpdate{{Name: "refs/heads/feature", Old: git.ZeroSHA, New: shaB}}),
		pushOf(f.secret.ID, f.olivia, "pack-3", []git.RefUpdate{{Name: "refs/heads/main", Old: git.ZeroSHA, New: shaB}}),
	} {
		if err := f.g.Record(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.pool.Exec(ctx, "UPDATE packs SET state = 'superseded', superseded_at = now() WHERE repo_id = $1", f.secret.ID); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		ids  []uuid.UUID
		want map[string]int64 // each listed repository's Git data, by slug
	}{
		{name: "the repositories asked, by slug", ids: []uuid.UUID{f.website.ID, f.secret.ID}, want: map[string]int64{"secret": 0, "website": 200}},
		{name: "only the organization's", ids: []uuid.UUID{f.website.ID, uuid.New()}, want: map[string]int64{"website": 200}},
		{name: "the totals without any", want: map[string]int64{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			used, byRepository, err := f.r.Usage(ctx, f.acme.ID, tt.ids)
			if err != nil || used != (limits.Usage{Repositories: 2, GitData: 200}) {
				t.Fatalf("Usage() = %+v, %v, want 2 repositories and website's 200 bytes", used, err)
			}
			got := map[string]int64{}
			for i, r := range byRepository {
				if i > 0 && byRepository[i-1].Repository.Slug >= r.Repository.Slug {
					t.Errorf("Usage() lists %s after %s, want them by slug", r.Repository.Slug, byRepository[i-1].Repository.Slug)
				}
				got[r.Repository.Slug] = r.GitData
			}
			if !maps.Equal(got, tt.want) {
				t.Errorf("Usage() lists %v, want %v", got, tt.want)
			}
		})
	}
}
