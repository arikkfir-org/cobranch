package postgres

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/deliverer"
	"cobranch.dev/internal/services/git"
)

// retargetedFile drops previews and delivers each feature/ branch to staging instead.
const retargetedFile = `delivery:
  targets:
    production:
      branches: [main]
      namespace: shop
    staging:
      branches: ["feature/**"]
      namespace: stage-${branch.slug}
  roots:
    - path: deploy
`

// history lists every delivery of the repository, its ended environments' too, oldest first, as deliveryLine says
// them.
func (f *jobsFixture) history(t *testing.T, repoID uuid.UUID) []string {
	t.Helper()
	rows, err := f.pool.Query(context.Background(), `SELECT d.number, t.name, e.branch, coalesce(d.sha, ''), d.kind, d.state, d.reason
		FROM deliveries d JOIN environments e ON e.id = d.environment_id JOIN delivery_targets t ON t.principal_id = e.target_id
		WHERE d.repo_id = $1 ORDER BY d.number`, repoID)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	var number int64
	var target, branch, sha, kind, state, reason string
	if _, err := pgx.ForEachRow(rows, []any{&number, &target, &branch, &sha, &kind, &state, &reason}, func() error {
		out = append(out, deliveryLine(number, target, branch, sha, code.DeliveryKind(kind), code.DeliveryState(state), reason))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

// deleteBranch has Max, a member, delete branch, whose head is sha.
func (f *jobsFixture) deleteBranch(t *testing.T, branch, sha string) {
	t.Helper()
	f.push(t, pushOf(f.website.ID, f.max, "", []git.RefUpdate{{Name: "refs/heads/" + branch, Old: sha, New: git.ZeroSHA}}))
}

func TestGitStoreTearsDownDeletedBranches(t *testing.T) {
	t.Parallel()
	f := newJobsFixture(t)
	repo := f.website.ID
	f.pushTwoDeliveries(t)
	f.deleteBranch(t, "feature/login", shaB)
	want := []string{"#1 production main@a queued", "#2 previews feature/login@b cancelled: superseded by #3",
		"#3 previews feature/login teardown queued"}
	if got := f.history(t, repo); !slices.Equal(got, want) {
		t.Errorf("deliveries = %q, want %q", got, want)
	}
	if got := f.note(t, repo, shaB, "delivery/previews"); got != "failed: Superseded by #3 by previews" {
		t.Errorf("the deleted branch's annotation = %q", got)
	}
	d, err := f.c.Delivery(context.Background(), repo, 3)
	if err != nil || d.Kind != code.Teardown || d.Trigger != code.TriggeredByPush || d.PushID == nil || d.SHA != "" {
		t.Errorf("Delivery(#3) = %+v, %v, want the push's teardown", d, err)
	}
	if _, err := f.c.PauseEnvironment(context.Background(), f.max, repo, d.EnvironmentID); err != nil {
		t.Fatal(err)
	}
	if got := f.history(t, repo); got[2] != want[2] {
		t.Errorf("after a pause, deliveries = %q, want the teardown still queued", got)
	}
}

// TestGitStoreKeepsARecreatedBranch processes a branch's delete after a later push made it again: the branch is live,
// so its environment stays and delivers the new head.
func TestGitStoreKeepsARecreatedBranch(t *testing.T) {
	t.Parallel()
	f := newJobsFixture(t)
	ctx := context.Background()
	repo := f.website.ID
	f.pushTwoDeliveries(t)
	gone := pushOf(repo, f.max, "", []git.RefUpdate{{Name: "refs/heads/feature/login", Old: shaB, New: git.ZeroSHA}})
	back := pushOf(repo, f.olivia, "pack-3", []git.RefUpdate{{Name: "refs/heads/feature/login", Old: git.ZeroSHA, New: shaC}},
		commit(shaC, person("Olivia", "olivia@example.com"), shaA))
	back.Settings = []git.TipSettings{tip(t, "feature/login", "blob-1", deliveryFile)}
	for _, p := range []git.Push{gone, back} {
		if err := f.g.Record(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	f.process(t)
	want := []string{"#1 production main@a queued", "#2 previews feature/login@b cancelled: superseded by #3",
		"#3 previews feature/login@c queued"}
	if got := f.history(t, repo); !slices.Equal(got, want) {
		t.Errorf("deliveries = %q, want %q", got, want)
	}
}

// TestGitStoreRetargets follows a push into main whose targets drop previews for staging: previews' environment is torn
// down, and feature/login gets a staging environment, whose delivery waits for a member as Nia's push did.
func TestGitStoreRetargets(t *testing.T) {
	t.Parallel()
	f := newJobsFixture(t)
	repo := f.website.ID
	f.pushTwoDeliveries(t)
	f.push(t, pushOf(repo, f.olivia, "pack-3", []git.RefUpdate{{Name: "refs/heads/main", Old: shaA, New: shaC}},
		commit(shaC, person("Olivia", "olivia@example.com"), shaA)), tip(t, "main", "blob-2", retargetedFile))
	want := []string{"#1 production main@a cancelled: superseded by #5", "#2 previews feature/login@b cancelled: superseded by #3",
		"#3 previews feature/login teardown queued", "#4 staging feature/login@b waiting: waits for a member to start it",
		"#5 production main@c queued"}
	if got := f.history(t, repo); !slices.Equal(got, want) {
		t.Errorf("deliveries = %q, want %q", got, want)
	}
	wantEnvs := []string{"previews feature/login shop-feature-login out_of_sync", "production main shop out_of_sync",
		"staging feature/login stage-feature-login out_of_sync"}
	if got := f.environments(t, repo, ""); !slices.Equal(got, wantEnvs) {
		t.Errorf("environments = %q, want %q", got, wantEnvs)
	}
	if got := f.note(t, repo, shaB, "delivery/staging"); got != "pending: Waits for a member to start it by staging" {
		t.Errorf("feature/login's staging annotation = %q", got)
	}
	// Another push into main with the same targets changes nothing more: previews' teardown is queued already.
	shaD := strings.Repeat("d", 40)
	f.push(t, pushOf(repo, f.olivia, "pack-4", []git.RefUpdate{{Name: "refs/heads/main", Old: shaC, New: shaD}},
		commit(shaD, person("Olivia", "olivia@example.com"), shaC)), tip(t, "main", "blob-2", retargetedFile))
	want = []string{"#1 production main@a cancelled: superseded by #5", "#2 previews feature/login@b cancelled: superseded by #3",
		"#3 previews feature/login teardown queued", "#4 staging feature/login@b waiting: waits for a member to start it",
		"#5 production main@c cancelled: superseded by #6", "#6 production main@d queued"}
	if got := f.history(t, repo); !slices.Equal(got, want) {
		t.Errorf("deliveries after another push = %q, want %q", got, want)
	}
}

// teardownFixture delivers main to production and feature/login to its preview, then deletes feature/login: the
// preview's teardown, #3, is queued.
func teardownFixture(t *testing.T) (*deliveryFixture, code.Environment) {
	t.Helper()
	f := newDeliveryFixture(t)
	_, preview := f.pushTwoDeliveries(t)
	f.deliverOnce(t, code.DeliverySucceeded)
	if _, err := f.c.StartDelivery(context.Background(), f.olivia, f.website.ID, 2); err != nil {
		t.Fatal(err)
	}
	f.deliverOnce(t, code.DeliverySucceeded)
	f.deleteBranch(t, "feature/login", shaB)
	return f, preview
}

// takeTeardown claims the teardown, marks it deleting and takes it until leaseUntil.
func (f *deliveryFixture) takeTeardown(t *testing.T, leaseUntil time.Time) deliverer.Teardown {
	t.Helper()
	ctx := context.Background()
	d := f.claim(t)
	if d.Kind != code.Teardown || d.SHA != "" || d.Namespace != "shop-feature-login" || d.Target != "previews" {
		t.Fatalf("Claim() = %+v, want the preview's teardown", d)
	}
	if marked, err := f.d.MarkDeleting(ctx, d.ID); !marked || err != nil {
		t.Fatalf("MarkDeleting() = %t, %v", marked, err)
	}
	if marked, err := f.d.MarkDeleting(ctx, d.ID); marked || err != nil {
		t.Errorf("MarkDeleting() again = %t, %v, want false", marked, err)
	}
	td, ok, err := f.d.TakeTeardown(ctx, d.ID, leaseUntil)
	if err != nil || !ok {
		t.Fatalf("TakeTeardown() = %t, %v", ok, err)
	}
	return td
}

func TestDeliveryStoreTearsDown(t *testing.T) {
	t.Parallel()
	f, preview := teardownFixture(t)
	ctx := context.Background()
	td := f.takeTeardown(t, time.Now().Add(time.Hour))
	if td.Number != 3 || td.Environment.ID != preview.ID || td.Environment.Target != "previews" || td.Environment.TargetID != preview.Target.ID ||
		td.Environment.Namespace != "shop-feature-login" || td.Environment.Cluster != nil || len(td.Inventory) != 1 || td.Inventory[0].Name != "site" ||
		time.Since(td.StartedAt) > time.Minute {
		t.Fatalf("TakeTeardown() = %+v, want #3 of the preview, holding site", td)
	}
	if _, ok, err := f.d.TakeTeardown(ctx, td.ID, time.Now().Add(time.Hour)); ok || err != nil {
		t.Errorf("TakeTeardown() of a teardown in hand = %t, %v, want false", ok, err)
	}
	if working, err := f.d.Working(ctx); err != nil || len(working) != 0 {
		t.Errorf("Working() = %+v, %v, want none: the teardown is in hand", working, err)
	}
	if err := f.d.LookAgain(ctx, td.ID, time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	working, err := f.d.Working(ctx)
	if err != nil || len(working) != 1 || working[0].ID != td.ID || working[0].Kind != code.Teardown || working[0].State != code.DeliveryDeleting {
		t.Fatalf("Working() = %+v, %v, want the teardown to look at again", working, err)
	}
	again, ok, err := f.d.TakeTeardown(ctx, td.ID, time.Now().Add(time.Hour))
	if err != nil || !ok || !again.StartedAt.Equal(td.StartedAt) {
		t.Fatalf("TakeTeardown() once due = %+v, %t, %v, want it again, started when it was", again, ok, err)
	}
	if err := f.d.EndTeardown(ctx, again, nil, code.DeliverySucceeded, ""); err != nil {
		t.Fatalf("EndTeardown() = %v", err)
	}
	if got := f.environments(t, f.website.ID, ""); !slices.Equal(got, []string{"production main shop synced"}) {
		t.Errorf("environments = %q, want the preview ended", got)
	}
	if got := f.history(t, f.website.ID); got[len(got)-1] != "#3 previews feature/login teardown succeeded" {
		t.Errorf("deliveries = %q, want #3 succeeded", got)
	}
	// The namespace is free again: the branch made anew gets a new environment in it.
	f.push(t, pushOf(f.website.ID, f.olivia, "pack-3", []git.RefUpdate{{Name: "refs/heads/feature/login", Old: git.ZeroSHA, New: shaC}},
		commit(shaC, person("Olivia", "olivia@example.com"), shaA)), tip(t, "feature/login", "blob-1", deliveryFile))
	envs, err := f.c.Environments(ctx, f.website.ID, "feature/login")
	if err != nil || len(envs) != 1 || envs[0].ID == preview.ID || envs[0].Namespace != "shop-feature-login" {
		t.Errorf("Environments(feature/login) = %+v, %v, want a new environment in shop-feature-login", envs, err)
	}
}

func TestDeliveryStoreFailsTeardown(t *testing.T) {
	t.Parallel()
	f, preview := teardownFixture(t)
	ctx := context.Background()
	td := f.takeTeardown(t, time.Now().Add(time.Hour))
	remaining := inventory(code.Unknown, "settings")
	if err := f.d.EndTeardown(ctx, td, remaining, code.DeliveryFailed, "1 object remains after 10m0s: v1/ConfigMap shop/settings"); err != nil {
		t.Fatalf("EndTeardown() = %v", err)
	}
	if err := f.d.EndTeardown(ctx, td, nil, code.DeliverySucceeded, ""); err != nil {
		t.Errorf("EndTeardown() of an ended teardown = %v, want nothing done", err)
	}
	if got := f.environments(t, f.website.ID, "feature/login"); !slices.Equal(got, []string{"previews feature/login shop-feature-login out_of_sync"}) {
		t.Errorf("environments = %q, want the preview live, Out of sync", got)
	}
	objects, err := f.d.Objects(ctx, preview.ID)
	if err != nil || len(objects) != 1 || objects[0].Name != "settings" {
		t.Errorf("Objects() = %+v, %v, want what remains", objects, err)
	}
	if got := f.history(t, f.website.ID); got[len(got)-1] != "#3 previews feature/login teardown failed: 1 object remains after 10m0s: v1/ConfigMap shop/settings" {
		t.Errorf("deliveries = %q, want #3 failed", got)
	}
}

// TestDeliveryStoreTearsDownAReturningBranch ends a teardown while a delivery of its branch, pushed again meanwhile,
// waits: the environment lives on, holding nothing, and the delivery is claimed only once the teardown ended.
func TestDeliveryStoreTearsDownAReturningBranch(t *testing.T) {
	t.Parallel()
	f, preview := teardownFixture(t)
	ctx := context.Background()
	td := f.takeTeardown(t, time.Now().Add(time.Hour))
	f.push(t, pushOf(f.website.ID, f.olivia, "pack-3", []git.RefUpdate{{Name: "refs/heads/feature/login", Old: git.ZeroSHA, New: shaC}},
		commit(shaC, person("Olivia", "olivia@example.com"), shaA)), tip(t, "feature/login", "blob-1", deliveryFile))
	if _, ok, err := f.d.Claim(ctx, time.Now().Add(time.Hour)); ok || err != nil {
		t.Errorf("Claim() while the environment deletes = %t, %v, want none", ok, err)
	}
	if err := f.d.EndTeardown(ctx, td, nil, code.DeliverySucceeded, ""); err != nil {
		t.Fatalf("EndTeardown() = %v", err)
	}
	got, err := f.c.Environment(ctx, f.website.ID, preview.ID)
	if err != nil || got.Revision != "" || got.Sync != code.OutOfSync || got.Health != code.Unknown {
		t.Errorf("Environment(preview) = %+v, %v, want it live, holding nothing", got, err)
	}
	if objects, err := f.d.Objects(ctx, preview.ID); err != nil || len(objects) != 0 {
		t.Errorf("Objects() = %+v, %v, want none", objects, err)
	}
	if d := f.claim(t); d.Number != 4 || d.SHA != shaC || d.EnvironmentID != preview.ID {
		t.Errorf("Claim() = %+v, want #4 of c to the preview", d)
	}
}

func TestDeliveryStoreSchedules(t *testing.T) {
	t.Parallel()
	f := newDeliveryFixture(t)
	ctx := context.Background()
	repo := f.website.ID
	production, _ := f.pushTwoDeliveries(t)
	f.deliverOnce(t, code.DeliverySucceeded)
	if n, err := f.d.Schedule(ctx, time.Hour); n != 0 || err != nil {
		t.Errorf("Schedule(1h) = %d, %v, want none: production delivered just now", n, err)
	}
	if n, err := f.d.Schedule(ctx, time.Nanosecond); n != 1 || err != nil {
		t.Fatalf("Schedule() = %d, %v, want production's resync: the preview's delivery waits", n, err)
	}
	if n, err := f.d.Schedule(ctx, time.Nanosecond); n != 0 || err != nil {
		t.Errorf("Schedule() again = %d, %v, want none: the resync is queued", n, err)
	}
	d := f.claim(t)
	if d.Kind != code.Resync || d.SHA != shaA || d.EnvironmentID != production.ID {
		t.Fatalf("Claim() = %+v, want production's resync of a", d)
	}
	a, ok, err := f.d.TakeApply(ctx, d.ID, time.Now().Add(time.Hour))
	if err != nil || !ok || a.Kind != code.Resync || !a.Rendered || string(a.Manifests) != "kind: ConfigMap\n" || a.Revision != shaA ||
		len(a.Inventory) != 1 {
		t.Fatalf("TakeApply() of a queued resync = %+v, %t, %v, want #1's render", a, ok, err)
	}
	if err := f.d.Record(ctx, deliverer.Outcome{Apply: a, Inventory: inventory(code.Healthy, "site"), Health: code.Healthy,
		State: code.DeliverySucceeded}); err != nil {
		t.Fatalf("Record() = %v", err)
	}
	if got := f.note(t, repo, shaA, "delivery/production"); got != "successful: Healthy by production" {
		t.Errorf("a resynced commit's annotation = %q, want #1's", got)
	}
	want := []string{"#1 production main@a succeeded", "#2 previews feature/login@b waiting: waits for a member to start it",
		"#3 production main@a resync succeeded"}
	if got := f.history(t, repo); !slices.Equal(got, want) {
		t.Errorf("deliveries = %q, want %q", got, want)
	}
	resync, err := f.c.Delivery(ctx, repo, 3)
	if err != nil || resync.Trigger != code.TriggeredBySchedule || resync.StartedBy != nil {
		t.Errorf("Delivery(#3) = %+v, %v, want the schedule's", resync, err)
	}
	f.checkScheduledTeardown(t, production)
}

// checkScheduledTeardown pauses production, which the schedule then leaves alone, and leaves feature/login's preview
// behind its deleted branch, as an installation before teardowns did: the schedule tears it down.
func (f *deliveryFixture) checkScheduledTeardown(t *testing.T, production code.Environment) {
	t.Helper()
	ctx := context.Background()
	if _, err := f.c.PauseEnvironment(ctx, f.max, f.website.ID, production.ID); err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{"UPDATE deliveries SET state = 'cancelled', ended_at = now() WHERE repo_id = $1 AND number = 2",
		"UPDATE branches SET deleted_at = now() WHERE repo_id = $1 AND name = 'feature/login'"} {
		if _, err := f.pool.Exec(ctx, stmt, f.website.ID); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := f.d.Schedule(ctx, time.Nanosecond); n != 1 || err != nil {
		t.Fatalf("Schedule() = %d, %v, want the preview's teardown", n, err)
	}
	got := f.history(t, f.website.ID)
	if want := "#4 previews feature/login teardown queued"; got[len(got)-1] != want || strings.Contains(strings.Join(got, "\n"), "#5") {
		t.Errorf("deliveries = %q, want %q last", got, want)
	}
}

func TestDeliveryStoreFailsAResyncWithoutARender(t *testing.T) {
	t.Parallel()
	f := newDeliveryFixture(t)
	ctx := context.Background()
	production, _ := f.pushTwoDeliveries(t)
	f.deliverOnce(t, code.DeliverySucceeded)
	if _, err := f.pool.Exec(ctx, "DELETE FROM delivery_renders"); err != nil {
		t.Fatal(err)
	}
	if n, err := f.d.Schedule(ctx, time.Nanosecond); n != 1 || err != nil {
		t.Fatalf("Schedule() = %d, %v", n, err)
	}
	d := f.claim(t)
	a, ok, err := f.d.TakeApply(ctx, d.ID, time.Now().Add(time.Hour))
	if err != nil || !ok || a.Rendered || a.EnvironmentID != production.ID {
		t.Errorf("TakeApply() = %+v, %t, %v, want the resync without a render", a, ok, err)
	}
}
