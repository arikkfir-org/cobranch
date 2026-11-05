package postgres

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/deliverer"
	"cobranch.dev/internal/services/repos"
)

// deliveryFixture is jobsFixture with the controller's delivery store and the credentials' store, which reads
// delivery tokens.
type deliveryFixture struct {
	*jobsFixture
	d     *DeliveryStore
	auths *AuthStore
}

func newDeliveryFixture(t *testing.T) *deliveryFixture {
	t.Helper()
	f := &deliveryFixture{jobsFixture: newJobsFixture(t)}
	f.d, f.auths = NewDeliveryStore(f.pool), NewAuthStore(f.pool)
	return f
}

// claim claims the queued delivery that waited longest, which must be there.
func (f *deliveryFixture) claim(t *testing.T) deliverer.Delivery {
	t.Helper()
	d, ok, err := f.d.Claim(context.Background(), time.Now().Add(time.Hour))
	if err != nil || !ok {
		t.Fatalf("Claim() = %t, %v, want a delivery", ok, err)
	}
	return d
}

// token records a token for the delivery that expires at expiresAt, and answers its hash.
func (f *deliveryFixture) token(t *testing.T, deliveryID uuid.UUID, expiresAt time.Time) []byte {
	t.Helper()
	secret := auth.NewSecret(auth.PrefixDeliveryToken)
	if err := f.d.Token(context.Background(), deliveryID, auth.Hash(secret), auth.Hint(secret), expiresAt); err != nil {
		t.Fatal(err)
	}
	return auth.Hash(secret)
}

// inventory is a ConfigMap of each name in namespace shop, as an apply recorded it, with health.
func inventory(health code.Health, names ...string) []deliverer.Object {
	out := make([]deliverer.Object, 0, len(names))
	for _, n := range names {
		out = append(out, deliverer.Object{Version: "v1", Kind: "ConfigMap", Namespace: "shop", Name: n, UID: "uid-" + n, Health: health})
	}
	return out
}

// TestDeliveryStoreDelivers takes main's delivery to production from queued, through its render and its apply, to
// succeeded, its environment Synced and its health followed.
func TestDeliveryStoreDelivers(t *testing.T) {
	t.Parallel()
	f := newDeliveryFixture(t)
	ctx := context.Background()
	production, _ := f.pushTwoDeliveries(t)
	d := f.claim(t)
	if d.Number != 1 || d.Target != "production" || d.Branch != "main" || d.SHA != shaA || d.Namespace != "shop" || d.OrgSlug != "acme" ||
		d.RepoSlug != "website" || d.EnvironmentID != production.ID || d.TargetID != production.Target.ID {
		t.Fatalf("Claim() = %+v, want #1 to production", d)
	}
	if _, ok, err := f.d.Claim(ctx, time.Now().Add(time.Hour)); ok || err != nil {
		t.Errorf("Claim() of a claimed delivery = %t, %v, want none: #2 waits for a member", ok, err)
	}
	hash := f.token(t, d.ID, time.Now().Add(time.Hour))
	f.checkToken(t, hash, d)
	if marked, err := f.d.MarkRendering(ctx, d.ID); !marked || err != nil {
		t.Fatalf("MarkRendering() = %t, %v", marked, err)
	}
	if got := f.note(t, d.RepoID, shaA, "delivery/production"); got != "in_progress: Rendering by production" {
		t.Errorf("annotation of a rendering delivery = %q", got)
	}
	f.checkWorking(t, d.ID, false)
	if _, ok, err := f.d.TakeApply(ctx, d.ID, time.Now().Add(time.Hour)); ok || err != nil {
		t.Errorf("TakeApply() before the render = %t, %v, want false", ok, err)
	}
	if err := f.c.KeepRender(ctx, d.ID, []byte("kind: ConfigMap\n")); err != nil {
		t.Fatal(err)
	}
	if err := f.c.KeepRender(ctx, d.ID, []byte("kind: Secret\n")); !errors.Is(err, code.ErrDeliveryNotRendering) {
		t.Errorf("KeepRender() again = %v, want %v", err, code.ErrDeliveryNotRendering)
	}
	f.checkWorking(t, d.ID, true)
	a, ok, err := f.d.TakeApply(ctx, d.ID, time.Now().Add(time.Hour))
	if err != nil || !ok || string(a.Manifests) != "kind: ConfigMap\n" || a.Namespace != "shop" || a.Revision != "" || len(a.Inventory) != 0 ||
		a.Number != 1 || a.Target != "production" || a.EnvironmentID != production.ID {
		t.Fatalf("TakeApply() = %+v, %t, %v, want #1's first apply", a, ok, err)
	}
	if _, ok, err := f.d.TakeApply(ctx, d.ID, time.Now().Add(time.Hour)); ok || err != nil {
		t.Errorf("TakeApply() of an apply in hand = %t, %v, want false", ok, err)
	}
	if working, err := f.d.Working(ctx); err != nil || len(working) != 0 {
		t.Errorf("Working() = %+v, %v, want none: its apply is in hand", working, err)
	}
	o := deliverer.Outcome{Apply: a, Inventory: inventory(code.Progressing, "site", "settings"), Health: code.Progressing,
		State: code.DeliverySucceeded, Report: code.AnnotationInput{Status: code.AnnotationInProgress, Summary: "Progressing"}}
	if err := f.d.Record(ctx, o); err != nil {
		t.Fatalf("Record() = %v", err)
	}
	f.checkDelivered(t, d, hash)
	f.checkHealth(t, d)
}

// checkToken checks that hash's token acts as d's target, on its repository, which it may read, to check its commit
// out, and nothing else.
func (f *deliveryFixture) checkToken(t *testing.T, hash []byte, d deliverer.Delivery) {
	t.Helper()
	cred, principal, err := f.auths.Credential(context.Background(), hash)
	if err != nil || cred.Kind != auth.CredentialDeliveryToken || cred.ID != d.ID || cred.PrincipalID != d.TargetID || cred.OrgID != d.OrgID ||
		cred.RepoID != d.RepoID || !slices.Equal(cred.Permissions, []string{repos.PermRead}) || principal.Kind != auth.KindTarget ||
		principal.DisplayName != "production" {
		t.Fatalf("Credential() = %+v, %+v, %v, want delivery %s's target", cred, principal, err, d.ID)
	}
	if err := f.auths.TouchCredential(context.Background(), cred.ID, cred.Kind, now); err != nil {
		t.Error(err)
	}
}

// checkWorking checks that the delivery is the one that works, rendered or not.
func (f *deliveryFixture) checkWorking(t *testing.T, id uuid.UUID, rendered bool) {
	t.Helper()
	working, err := f.d.Working(context.Background())
	if err != nil || len(working) != 1 || working[0].ID != id || working[0].State != code.DeliveryRendering || working[0].Rendered != rendered ||
		working[0].StartedAt.IsZero() {
		t.Errorf("Working() = %+v, %v, want the delivery, rendered %t", working, err, rendered)
	}
}

// checkDelivered checks what Record left: the delivery succeeded, its token gone, its environment Synced at its
// commit, Progressing, with its inventory, and its annotation In progress.
func (f *deliveryFixture) checkDelivered(t *testing.T, d deliverer.Delivery, hash []byte) {
	t.Helper()
	ctx := context.Background()
	if got := f.deliveries(t, d.RepoID); got[0] != "#1 production main@a succeeded" {
		t.Errorf("deliveries = %q", got)
	}
	if _, _, err := f.auths.Credential(ctx, hash); !errors.Is(err, auth.ErrNotFound) {
		t.Errorf("Credential() of an ended delivery's token: error = %v, want not found", err)
	}
	envs, err := f.c.Environments(ctx, d.RepoID, "main")
	if err != nil || len(envs) != 1 || envs[0].Sync != code.Synced || envs[0].Revision != shaA || envs[0].Health != code.Progressing {
		t.Errorf("Environments() = %+v, %v, want production Synced at a, Progressing", envs, err)
	}
	if got := f.note(t, d.RepoID, shaA, "delivery/production"); got != "in_progress: Progressing by production" {
		t.Errorf("annotation of a delivery that progresses = %q", got)
	}
	objects, err := f.d.Objects(ctx, d.EnvironmentID)
	if err != nil || !slices.EqualFunc(objects, inventory(code.Progressing, "site", "settings"), func(a, b deliverer.Object) bool { return a == b }) {
		t.Errorf("Objects() = %+v, %v, want site and settings", objects, err)
	}
	if finished, err := f.d.Finish(ctx, d.ID, code.DeliveryFailed, "its render pod vanished"); finished || err != nil {
		t.Errorf("Finish() of an ended delivery = %t, %v, want false", finished, err)
	}
	standing, err := f.d.Deliveries(ctx, []uuid.UUID{d.ID, uuid.New()})
	if err != nil || len(standing) != 1 || standing[d.ID] != (deliverer.Standing{RepoID: d.RepoID, State: code.DeliverySucceeded}) {
		t.Errorf("Deliveries() = %+v, %v, want the delivery, succeeded", standing, err)
	}
}

// checkHealth follows the environment's health: Progressing until it reads Healthy, which its revision's annotation
// follows, once.
func (f *deliveryFixture) checkHealth(t *testing.T, d deliverer.Delivery) {
	t.Helper()
	ctx := context.Background()
	envs, err := f.d.Progressing(ctx)
	want := deliverer.Environment{ID: d.EnvironmentID, OrgID: d.OrgID, RepoID: d.RepoID, TargetID: d.TargetID, Target: "production",
		Namespace: "shop"}
	if err != nil || len(envs) != 1 || envs[0] != want {
		t.Fatalf("Progressing() = %+v, %v, want production", envs, err)
	}
	healthy := code.AnnotationInput{Status: code.AnnotationSuccessful, Summary: "Healthy"}
	read, created := inventory(code.Healthy, "site", "settings"), time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)
	read[1].Ready, read[1].Created = "1/1", &created
	if err := f.d.SetHealth(ctx, d.EnvironmentID, read, code.Healthy, healthy); err != nil {
		t.Fatalf("SetHealth() = %v", err)
	}
	listed, err := f.c.EnvironmentObjects(ctx, d.RepoID, d.EnvironmentID)
	if err != nil || len(listed) != 2 || listed[0].CreatedAt != nil || listed[1].Ready != "1/1" || listed[1].CreatedAt == nil ||
		!listed[1].CreatedAt.Equal(created) || listed[1].Delivery != d.Number || listed[1].Health != code.Healthy {
		t.Errorf("EnvironmentObjects() = %+v, %v, want settings ready 1/1, created at 09:00, applied by #%d", listed, err, d.Number)
	}
	if _, err := f.c.EnvironmentObjects(ctx, f.secret.ID, d.EnvironmentID); !errors.Is(err, repos.ErrNotFound) {
		t.Errorf("another repository's EnvironmentObjects() = %v, want %v", err, repos.ErrNotFound)
	}
	if got := f.note(t, d.RepoID, shaA, "delivery/production"); got != "successful: Healthy by production" {
		t.Errorf("annotation of a healthy delivery = %q", got)
	}
	if objects, err := f.d.Objects(ctx, d.EnvironmentID); err != nil || objects[1].Health != code.Healthy {
		t.Errorf("Objects() = %+v, %v, want them healthy", objects, err)
	}
	if envs, err := f.d.Progressing(ctx); err != nil || len(envs) != 0 {
		t.Errorf("Progressing() = %+v, %v, want none", envs, err)
	}
}

// TestDeliveryStoreFails puts a delivery off while the quota has no room, then fails it, its environment Out of sync
// and its token dropped.
func TestDeliveryStoreFails(t *testing.T) {
	t.Parallel()
	f := newDeliveryFixture(t)
	ctx := context.Background()
	f.pushTwoDeliveries(t)
	d := f.claim(t)
	if err := f.d.Defer(ctx, d.ID, time.Now().Add(-time.Second), "waits for room in the organization's quota"); err != nil {
		t.Fatal(err)
	}
	if got := f.note(t, d.RepoID, shaA, "delivery/production"); got != "pending: Waits for room in the organization's quota by production" {
		t.Errorf("annotation of a delivery put off = %q", got)
	}
	if again := f.claim(t); again.ID != d.ID {
		t.Errorf("Claim() after the delivery's put off until now = %s, want %s", again.ID, d.ID)
	}
	f.token(t, d.ID, time.Now().Add(time.Hour))
	if n, err := f.d.DropTokens(ctx); n != 0 || err != nil {
		t.Errorf("DropTokens() = %d, %v, want none: the delivery is queued", n, err)
	}
	if finished, err := f.d.Finish(ctx, d.ID, code.DeliveryFailed, "this installation delivers to no cluster of its own"); !finished || err != nil {
		t.Fatalf("Finish() = %t, %v", finished, err)
	}
	if got := f.note(t, d.RepoID, shaA, "delivery/production"); got != "failed: This installation delivers to no cluster of its own by production" {
		t.Errorf("annotation of a failed delivery = %q", got)
	}
	if got := f.environments(t, d.RepoID, "main"); !slices.Equal(got, []string{"production main shop out_of_sync"}) {
		t.Errorf("environments = %q", got)
	}
	if n, err := f.d.DropTokens(ctx); n != 0 || err != nil {
		t.Errorf("DropTokens() = %d, %v, want none: Finish deleted it", n, err)
	}
}

// deliverOnce takes the delivery that waited longest through its render, uploaded before it is marked rendering, as
// a fast pod's may be, and its apply, which ends in state.
func (f *deliveryFixture) deliverOnce(t *testing.T, state code.DeliveryState) deliverer.Delivery {
	t.Helper()
	ctx := context.Background()
	d := f.claim(t)
	f.token(t, d.ID, time.Now().Add(time.Hour))
	if err := f.c.KeepRender(ctx, d.ID, []byte("kind: ConfigMap\n")); err != nil {
		t.Fatalf("KeepRender() of a queued delivery = %v", err)
	}
	if marked, err := f.d.MarkRendering(ctx, d.ID); !marked || err != nil {
		t.Fatalf("MarkRendering() = %t, %v", marked, err)
	}
	a, ok, err := f.d.TakeApply(ctx, d.ID, time.Now().Add(time.Hour))
	if err != nil || !ok {
		t.Fatalf("TakeApply() = %t, %v", ok, err)
	}
	if err := f.d.Record(ctx, deliverer.Outcome{Apply: a, Inventory: inventory(code.Healthy, "site"), Health: code.Healthy, State: state,
		Report: code.AnnotationInput{Status: code.AnnotationSuccessful, Summary: "Healthy"}}); err != nil {
		t.Fatalf("Record() = %v", err)
	}
	return d
}

// rendered lists the numbers of the repository's deliveries that keep their renders.
func (f *deliveryFixture) rendered(t *testing.T, repoID uuid.UUID) []int64 {
	t.Helper()
	rows, err := f.pool.Query(context.Background(),
		`SELECT d.number FROM delivery_renders r JOIN deliveries d ON d.id = r.delivery_id WHERE d.repo_id = $1 ORDER BY d.number`, repoID)
	if err != nil {
		t.Fatal(err)
	}
	numbers, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		t.Fatal(err)
	}
	return numbers
}

// TestDeliveryStoreDropsRenders keeps production's newest render and its last to succeed, deleting the rest.
func TestDeliveryStoreDropsRenders(t *testing.T) {
	t.Parallel()
	f := newDeliveryFixture(t)
	ctx := context.Background()
	production, _ := f.pushTwoDeliveries(t)
	f.deliverOnce(t, code.DeliverySucceeded)
	for range 2 {
		if _, err := f.c.Redeliver(ctx, f.max, f.website.ID, production.ID); err != nil {
			t.Fatal(err)
		}
		f.deliverOnce(t, code.DeliveryFailed)
	}
	if got := f.rendered(t, f.website.ID); !slices.Equal(got, []int64{1, 3, 4}) {
		t.Fatalf("renders kept before the sweep = %v", got)
	}
	if n, err := f.d.DropRenders(ctx, 1); n != 1 || err != nil {
		t.Errorf("DropRenders(1) = %d, %v, want #3's", n, err)
	}
	if got := f.rendered(t, f.website.ID); !slices.Equal(got, []int64{1, 4}) {
		t.Errorf("renders kept = %v, want #4, the newest, and #1, the last to succeed", got)
	}
	if n, err := f.d.DropRenders(ctx, 1); n != 0 || err != nil {
		t.Errorf("DropRenders(1) again = %d, %v, want none", n, err)
	}
}

// TestDeliveryStoreLateRecord records nothing of an apply whose lease lapsed once another controller ended the
// delivery: the environment keeps the inventory that controller recorded.
func TestDeliveryStoreLateRecord(t *testing.T) {
	t.Parallel()
	f := newDeliveryFixture(t)
	ctx := context.Background()
	production, _ := f.pushTwoDeliveries(t)
	d := f.claim(t)
	f.token(t, d.ID, time.Now().Add(time.Hour))
	if _, err := f.d.MarkRendering(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.c.KeepRender(ctx, d.ID, []byte("kind: ConfigMap\n")); err != nil {
		t.Fatal(err)
	}
	slow, ok, err := f.d.TakeApply(ctx, d.ID, time.Now().Add(-time.Second))
	if err != nil || !ok {
		t.Fatalf("TakeApply() = %t, %v", ok, err)
	}
	fast, ok, err := f.d.TakeApply(ctx, d.ID, time.Now().Add(time.Hour))
	if err != nil || !ok {
		t.Fatalf("TakeApply() of a lapsed apply = %t, %v, want it taken again", ok, err)
	}
	report := code.AnnotationInput{Status: code.AnnotationSuccessful, Summary: "Healthy"}
	if err := f.d.Record(ctx, deliverer.Outcome{Apply: fast, Inventory: inventory(code.Healthy, "site"), Health: code.Healthy,
		State: code.DeliverySucceeded, Report: report}); err != nil {
		t.Fatal(err)
	}
	if err := f.d.Record(ctx, deliverer.Outcome{Apply: slow, Inventory: inventory(code.Healthy, "stale"), Health: code.Healthy,
		State: code.DeliveryFailed, Report: report}); err != nil {
		t.Errorf("Record() of an ended delivery = %v, want nothing recorded", err)
	}
	objects, err := f.d.Objects(ctx, production.ID)
	if err != nil || len(objects) != 1 || objects[0].Name != "site" {
		t.Errorf("Objects() = %+v, %v, want the inventory the delivery ended with", objects, err)
	}
	if got := f.deliveries(t, f.website.ID); !slices.Contains(got, "#1 production main@a succeeded") {
		t.Errorf("deliveries = %q, want #1 succeeded", got)
	}
}
