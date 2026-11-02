package postgres

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/repos"
)

// repoFixture is Acme's orgFixture with two repositories Olivia created: website and secret.
type repoFixture struct {
	*orgFixture
	r               *RepoStore
	website, secret repos.Repository
}

func newRepoFixture(t *testing.T) *repoFixture {
	t.Helper()
	f := &repoFixture{orgFixture: newOrgFixture(t)}
	f.r = NewRepoStore(f.pool)
	for _, r := range []*repos.Repository{&f.website, &f.secret} {
		*r = repos.Repository{ID: uuid.New(), OrgID: f.acme.ID, Name: "x", ObjectFormat: "sha1", CreatedBy: f.olivia, CreatedAt: now}
	}
	f.website.Slug, f.secret.Slug = "website", "secret"
	for _, r := range []repos.Repository{f.website, f.secret} {
		if err := f.r.Create(context.Background(), f.olivia, r, 0); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func TestRepoStoreCreate(t *testing.T) {
	f := newRepoFixture(t)
	ctx := context.Background()
	got, err := f.r.BySlug(ctx, f.acme.ID, "website")
	if err != nil || got.ID != f.website.ID || got.CreatedBy != f.olivia || got.Visibility != repos.VisibilityPrivate || got.ObjectFormat != "sha1" {
		t.Fatalf("BySlug() = %+v, %v", got, err)
	}
	if _, err := f.r.BySlug(ctx, uuid.New(), "website"); !errors.Is(err, repos.ErrNotFound) {
		t.Errorf("another organization's BySlug() = %v, want %v", err, repos.ErrNotFound)
	}
	dup := f.website
	dup.ID = uuid.New()
	if err := f.r.Create(ctx, f.olivia, dup, 0); !errors.Is(err, repos.ErrSlugTaken) {
		t.Errorf("Create() of a taken slug = %v, want %v", err, repos.ErrSlugTaken)
	}
	access, err := f.r.Access(ctx, f.website.ID)
	if err != nil || len(access) != 1 || access[0].Principal.ID != f.olivia || access[0].Principal.Kind != auth.KindUser ||
		len(access[0].Roles) != 1 || access[0].Roles[0].ID != auth.RoleMaintainer {
		t.Errorf("Access() = %+v, %v, want Olivia as Maintainer", access, err)
	}
	if n := count(t, f.pool, "SELECT count(*) FROM events WHERE kind = 'repo.created' AND org_id = $1", f.acme.ID); n != 2 {
		t.Errorf("%d repo.created events, want 2", n)
	}
	// Each repository, its slug and its creator's grant, all as Olivia.
	if n := count(t, f.pool, "SELECT count(*) FROM audit_log WHERE table_name IN ('repositories', 'repository_slugs') AND principal_id = $1", f.olivia); n != 4 {
		t.Errorf("%d audit rows name Olivia for the repositories and their slugs, want 4", n)
	}
}

func TestRepoStoreUpdate(t *testing.T) {
	f := newRepoFixture(t)
	ctx := context.Background()
	renamed := f.website
	renamed.Slug, renamed.Name, renamed.Description = "www", "WWW", "The site"
	if err := f.r.Update(ctx, f.olivia, renamed); err != nil {
		t.Fatal(err)
	}
	for _, slug := range []string{"www", "website"} {
		if got, err := f.r.BySlug(ctx, f.acme.ID, slug); err != nil || got.ID != f.website.ID || got.Slug != "www" || got.Description != "The site" {
			t.Errorf("BySlug(%s) = %+v, %v", slug, got, err)
		}
	}
	taken := f.secret
	taken.Slug = "www"
	if err := f.r.Update(ctx, f.olivia, taken); !errors.Is(err, repos.ErrSlugTaken) {
		t.Errorf("Update() to a current slug = %v, want %v", err, repos.ErrSlugTaken)
	}
	// An old slug is free to take: it then names the repository that took it.
	taken.Slug = "website"
	if err := f.r.Update(ctx, f.olivia, taken); err != nil {
		t.Fatal(err)
	}
	if got, err := f.r.BySlug(ctx, f.acme.ID, "website"); err != nil || got.ID != f.secret.ID {
		t.Errorf("BySlug(website) after secret took it = %+v, %v", got, err)
	}
	gone := f.website
	gone.ID = uuid.New()
	if err := f.r.Update(ctx, f.olivia, gone); !errors.Is(err, repos.ErrNotFound) {
		t.Errorf("Update() of no repository = %v, want %v", err, repos.ErrNotFound)
	}
}

func TestRepoStorePermissions(t *testing.T) {
	f := newRepoFixture(t)
	ctx := context.Background()
	held := func(principal, repo uuid.UUID) []string {
		t.Helper()
		p, err := f.s.Permissions(ctx, principal, auth.ScopeRepository, repo)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	// Olivia is Owner and Maintainer; Max, a Member, holds no repository permission at Acme.
	if got := held(f.olivia, f.secret.ID); !slices.Equal(got, slices.Sorted(slices.Values(auth.RepositoryPermissions))) {
		t.Errorf("Olivia holds %v on secret, want every repository permission and no organization one", got)
	}
	if got := held(f.max, f.website.ID); len(got) != 0 {
		t.Errorf("Max holds %v on website, want nothing", got)
	}
	if err := f.r.Grant(ctx, f.olivia, f.website, f.max, auth.RoleDeveloper); err != nil {
		t.Fatal(err)
	}
	if err := f.r.Grant(ctx, f.olivia, f.website, f.max, auth.RoleDeveloper); !errors.Is(err, repos.ErrGranted) {
		t.Errorf("Grant() again = %v, want %v", err, repos.ErrGranted)
	}
	if got := held(f.max, f.website.ID); !slices.Contains(got, "repo.push") || slices.Contains(got, "repo.manage") {
		t.Errorf("Max holds %v on website, want Developer's", got)
	}
	if got := held(f.max, f.secret.ID); len(got) != 0 {
		t.Errorf("Max holds %v on secret, want nothing", got)
	}
	ids, err := f.s.Repositories(ctx, f.max, f.acme.ID, "repo.read")
	if err != nil || !slices.Equal(ids, []uuid.UUID{f.website.ID}) {
		t.Errorf("Max reads %v (%v), want website alone", ids, err)
	}
	ids, err = f.s.Repositories(ctx, f.olivia, f.acme.ID, "repo.read")
	if err != nil || !slices.Equal(ids, []uuid.UUID{f.secret.ID, f.website.ID}) {
		t.Errorf("Olivia reads %v (%v), want both, by slug", ids, err)
	}
	list, err := f.r.Among(ctx, f.acme.ID, ids)
	if err != nil || len(list) != 2 || list[0].Visibility != repos.VisibilityPrivate || list[1].Visibility != repos.VisibilityInternal {
		t.Errorf("Among() = %+v, %v, want secret Private and website Internal", list, err)
	}
	if err := f.r.Revoke(ctx, f.olivia, f.website, f.max, auth.RoleDeveloper); err != nil {
		t.Fatal(err)
	}
	if err := f.r.Revoke(ctx, f.olivia, f.website, f.max, auth.RoleDeveloper); !errors.Is(err, repos.ErrNotFound) {
		t.Errorf("Revoke() again = %v, want %v", err, repos.ErrNotFound)
	}
	if got := held(f.max, f.website.ID); len(got) != 0 {
		t.Errorf("Max holds %v on website after the revocation", got)
	}
}

func TestRepoStoreLeave(t *testing.T) {
	f := newRepoFixture(t)
	ctx := context.Background()
	if err := f.r.Grant(ctx, f.olivia, f.website, f.max, auth.RoleDeveloper); err != nil {
		t.Fatal(err)
	}
	if err := f.s.Leave(ctx, f.olivia, f.acme.ID, f.max, now); err != nil {
		t.Fatal(err)
	}
	if p, err := f.s.Permissions(ctx, f.max, auth.ScopeRepository, f.website.ID); err != nil || len(p) != 0 {
		t.Errorf("Max holds %v (%v) on website after his removal, want nothing", p, err)
	}
	if n := count(t, f.pool, "SELECT count(*) FROM grants WHERE principal_id = $1", f.max); n != 0 {
		t.Errorf("Max keeps %d grants after his removal, want none", n)
	}
	// Olivia's own grants on the repositories stay.
	if access, err := f.r.Access(ctx, f.website.ID); err != nil || len(access) != 1 || access[0].Principal.ID != f.olivia {
		t.Errorf("Access() = %+v, %v, want Olivia alone", access, err)
	}
}

func TestRepoStorePrincipalsAndRoles(t *testing.T) {
	f := newRepoFixture(t)
	ctx := context.Background()
	key, _ := deployKey(t, f.orgFixture)
	for _, tt := range []struct {
		id   uuid.UUID
		kind auth.Kind
		err  error
	}{{f.max, auth.KindUser, nil}, {key.ID, auth.KindAPIKey, nil}, {f.nia, "", repos.ErrNotFound}} {
		p, err := f.r.Principal(ctx, f.acme.ID, tt.id)
		if !errors.Is(err, tt.err) || p.Kind != tt.kind {
			t.Errorf("Principal(%s) = %+v, %v, want kind %q and %v", tt.id, p, err, tt.kind, tt.err)
		}
	}
	roles, err := f.r.Roles(ctx, f.acme.ID)
	if err != nil {
		t.Fatal(err)
	}
	var ids []uuid.UUID
	for _, r := range roles {
		ids = append(ids, r.ID)
	}
	if want := []uuid.UUID{auth.RoleReader, auth.RoleDeveloper, auth.RoleMaintainer, auth.RoleAutomation}; !slices.Equal(ids, want) {
		t.Errorf("Roles() = %v, want the built-in repository roles in order", ids)
	}
}
