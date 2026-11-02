package postgres

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"

	"cobranch.dev/internal/adapters/postgres/queries"
	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/secrets"
)

// secretFixture is Acme's repoFixture with a SecretStore, and build, a job of website's.
type secretFixture struct {
	*repoFixture
	store *SecretStore
	build uuid.UUID
}

func newSecretFixture(t *testing.T) *secretFixture {
	t.Helper()
	f := &secretFixture{repoFixture: newRepoFixture(t), build: uuid.New()}
	f.store = NewSecretStore(f.pool)
	if err := queries.New(f.pool).InsertJob(context.Background(), queries.InsertJobParams{ID: f.build, Name: "build", OrgID: f.acme.ID, RepoID: f.website.ID}); err != nil {
		t.Fatal(err)
	}
	return f
}

// create adds a secret named name where it lives, sealed as value.
func (f *secretFixture) create(t *testing.T, repoID uuid.UUID, name, value string) secrets.Secret {
	t.Helper()
	s, err := f.store.Create(context.Background(), f.olivia, secrets.Secret{ID: uuid.New(), OrgID: f.acme.ID, RepoID: repoID, Name: name}, []byte(value))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// holding is what a principal should hold on a secret.
type holding struct {
	name              string
	principal, secret uuid.UUID
	want              []string
}

// assertHolds fails t, naming stage, unless each principal holds what it should on its secret.
func (f *secretFixture) assertHolds(t *testing.T, stage string, cases []holding) {
	t.Helper()
	for _, c := range cases {
		got, err := f.s.Permissions(context.Background(), c.principal, auth.ScopeSecret, c.secret)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(got, c.want) {
			t.Errorf("%s: %s holds %v, want %v", stage, c.name, got, c.want)
		}
	}
}

func TestSecretStoreWrites(t *testing.T) {
	t.Parallel()
	f := newSecretFixture(t)
	ctx := context.Background()
	npm, deploy := f.create(t, uuid.Nil, "npm", "sealed-npm"), f.create(t, f.website.ID, "npm", "sealed-deploy")
	if npm.Version != 1 || npm.CreatedBy != f.olivia || npm.UpdatedBy != f.olivia || npm.RepoID != uuid.Nil || deploy.RepoID != f.website.ID {
		t.Errorf("created %+v and %+v", npm, deploy)
	}
	if _, err := f.store.Create(ctx, f.olivia, secrets.Secret{ID: uuid.New(), OrgID: f.acme.ID, Name: "npm"}, []byte("x")); !errors.Is(err, secrets.ErrNameTaken) {
		t.Errorf("Create() of a taken name = %v, want %v", err, secrets.ErrNameTaken)
	}
	if _, err := f.store.Create(ctx, f.olivia, secrets.Secret{ID: uuid.New(), OrgID: uuid.New(), RepoID: f.website.ID, Name: "x"}, []byte("x")); !errors.Is(err, secrets.ErrNotFound) {
		t.Errorf("Create() in another organization's repository = %v, want %v", err, secrets.ErrNotFound)
	}
	list, err := f.store.List(ctx, f.acme.ID, f.website.ID)
	if err != nil || len(list) != 1 || list[0].ID != deploy.ID {
		t.Errorf("List(website) = %+v, %v", list, err)
	}

	renamed, err := f.store.Replace(ctx, f.max, npm.ID, 1, "npm-token", nil)
	if err != nil || renamed.Name != "npm-token" || renamed.Version != 2 || renamed.UpdatedBy != f.max || renamed.CreatedBy != f.olivia {
		t.Fatalf("Replace() = %+v, %v", renamed, err)
	}
	if _, err := f.store.Replace(ctx, f.max, npm.ID, 1, "npm", []byte("x")); !errors.Is(err, secrets.ErrChanged) {
		t.Errorf("Replace() of an old version = %v, want %v", err, secrets.ErrChanged)
	}
	if _, err := f.store.Replace(ctx, f.max, npm.ID, secrets.AnyVersion, "npm", []byte("sealed-new")); err != nil {
		t.Fatal(err)
	}
	sealed, err := f.store.Sealed(ctx, []uuid.UUID{npm.ID, deploy.ID, uuid.New()})
	if err != nil || len(sealed) != 2 || string(sealed[npm.ID]) != "sealed-new" || string(sealed[deploy.ID]) != "sealed-deploy" {
		t.Errorf("Sealed() = %q, %v", sealed, err)
	}
	if n := count(t, f.pool, "SELECT count(*) FROM audit_log WHERE table_name = 'secrets' AND (before ? 'value' OR after ? 'value')"); n != 0 {
		t.Errorf("%d audit rows hold a value", n)
	}
	if n := count(t, f.pool, "SELECT count(*) FROM audit_log WHERE table_name = 'secrets' AND principal_id = $1", f.max); n != 2 {
		t.Errorf("%d audit rows name Max, want 2", n)
	}

	if err := f.store.Delete(ctx, f.olivia, deploy.ID, 7); !errors.Is(err, secrets.ErrChanged) {
		t.Errorf("Delete() of another version = %v, want %v", err, secrets.ErrChanged)
	}
	if err := f.store.Delete(ctx, f.olivia, deploy.ID, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Get(ctx, deploy.ID); !errors.Is(err, secrets.ErrNotFound) {
		t.Errorf("Get() of a deleted one = %v", err)
	}
	if err := f.store.Delete(ctx, f.olivia, deploy.ID, secrets.AnyVersion); !errors.Is(err, secrets.ErrNotFound) {
		t.Errorf("Delete() of a deleted one = %v", err)
	}
	if n := count(t, f.pool, "SELECT count(*) FROM events WHERE kind LIKE 'secret.%' AND org_id = $1", f.acme.ID); n != 5 {
		t.Errorf("%d secret events, want 5", n)
	}
}

func TestSecretStoreGrants(t *testing.T) {
	t.Parallel()
	f := newSecretFixture(t)
	ctx := context.Background()
	npm, deploy := f.create(t, uuid.Nil, "npm", "x"), f.create(t, f.website.ID, "deploy", "x")
	both := []string{"secrets.manage", "secrets.read"}
	f.assertHolds(t, "at first", []holding{
		{"the Owner on the organization's", f.olivia, npm.ID, both},
		{"the Owner on the repository's", f.olivia, deploy.ID, both},
		{"a Member on the repository's", f.max, deploy.ID, nil},
		{"the job on the repository's", f.build, deploy.ID, nil},
	})

	if err := f.store.Grant(ctx, f.olivia, npm, f.build); err != nil {
		t.Fatal(err)
	}
	if err := f.store.Grant(ctx, f.olivia, npm, f.build); !errors.Is(err, secrets.ErrGranted) {
		t.Errorf("a second Grant() = %v, want %v", err, secrets.ErrGranted)
	}
	if err := f.r.Grant(ctx, f.olivia, f.website, f.max, auth.RoleSecretReader); err != nil {
		t.Fatal(err)
	}
	f.assertHolds(t, "granted", []holding{
		{"the job on the secret it was granted", f.build, npm.ID, []string{"secrets.read"}},
		{"the job on another", f.build, deploy.ID, nil},
		{"a Secret reader of the repository on its secret", f.max, deploy.ID, []string{"secrets.read"}},
		{"a Secret reader of the repository on the organization's", f.max, npm.ID, nil},
	})
	access, err := f.store.Access(ctx, npm.ID)
	if err != nil || len(access) != 1 || access[0].ID != f.build || access[0].Kind != auth.KindJob || access[0].DisplayName != "build" {
		t.Errorf("Access() = %+v, %v", access, err)
	}
	if p, err := f.store.Principal(ctx, f.acme.ID, f.build); err != nil || p.Kind != auth.KindJob {
		t.Errorf("Principal(build) = %+v, %v", p, err)
	}
	if _, err := f.store.Principal(ctx, f.acme.ID, f.nia); !errors.Is(err, secrets.ErrNotFound) {
		t.Errorf("Principal(a stranger) = %v, want %v", err, secrets.ErrNotFound)
	}

	if err := f.store.Revoke(ctx, f.olivia, npm, f.max); !errors.Is(err, secrets.ErrNotFound) {
		t.Errorf("Revoke() of a grant not there = %v, want %v", err, secrets.ErrNotFound)
	}
	if err := f.store.Revoke(ctx, f.olivia, npm, f.build); err != nil {
		t.Fatal(err)
	}
	if err := f.store.Grant(ctx, f.olivia, npm, f.build); err != nil {
		t.Fatal(err)
	}
	if err := f.store.Delete(ctx, f.olivia, npm.ID, secrets.AnyVersion); err != nil {
		t.Fatal(err)
	}
	if n := count(t, f.pool, "SELECT count(*) FROM grants WHERE scope = 'secret' AND scope_id = $1", npm.ID); n != 0 {
		t.Errorf("%d grants on the deleted secret remain", n)
	}
	if n := count(t, f.pool, "SELECT count(*) FROM audit_log WHERE table_name = 'grants' AND principal_id = $1 AND (after->>'scope' = 'secret' OR before->>'scope' = 'secret')", f.olivia); n != 4 {
		t.Errorf("%d audit rows of secret grants name Olivia, want 4", n)
	}
}

func TestSecretGrantsGoWithTheMembership(t *testing.T) {
	t.Parallel()
	f := newSecretFixture(t)
	ctx := context.Background()
	npm, deploy := f.create(t, uuid.Nil, "npm", "x"), f.create(t, f.website.ID, "deploy", "x")
	for _, s := range []secrets.Secret{npm, deploy} {
		for _, p := range []uuid.UUID{f.max, f.build} {
			if err := f.store.Grant(ctx, f.olivia, s, p); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := f.s.Leave(ctx, f.olivia, f.acme.ID, f.max, now); err != nil {
		t.Fatal(err)
	}
	if n := count(t, f.pool, "SELECT count(*) FROM grants WHERE scope = 'secret' AND principal_id = $1", f.max); n != 0 {
		t.Errorf("%d grants on secrets remain to the member removed", n)
	}
	read := []string{"secrets.read"}
	f.assertHolds(t, "after Max left", []holding{
		{"the member removed on the organization's", f.max, npm.ID, nil},
		{"the member removed on the repository's", f.max, deploy.ID, nil},
		{"the job on the organization's", f.build, npm.ID, read},
		{"the job on the repository's", f.build, deploy.ID, read},
	})
}

func TestSecretStoreJobs(t *testing.T) {
	t.Parallel()
	f := newSecretFixture(t)
	ctx := context.Background()
	if jobs, err := f.store.Jobs(ctx, f.acme.ID, f.website.ID); err != nil || len(jobs) != 1 || jobs[0].ID != f.build || jobs[0].Kind != auth.KindJob || jobs[0].DisplayName != "build" {
		t.Errorf("Jobs() = %+v, %v", jobs, err)
	}
	if jobs, err := f.store.Jobs(ctx, uuid.New(), f.website.ID); err != nil || len(jobs) != 0 {
		t.Errorf("Jobs() of another organization = %+v, %v", jobs, err)
	}
}
