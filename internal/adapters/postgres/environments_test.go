package postgres

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"cobranch.dev/internal/adapters/postgres/queries"
	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/git"
	"cobranch.dev/internal/services/repos"
)

// deliveryFile delivers main to production in namespace shop, and each feature/ branch to a preview in a namespace of
// its own.
const deliveryFile = `delivery:
  targets:
    production:
      branches: [main]
      namespace: shop
    previews:
      branches: ["feature/**"]
      namespace: shop-${branch.slug}
  roots:
    - path: deploy
`

// deliveries lists the repository's deliveries, oldest first, as "#n target branch@sha state: reason".
func (f *jobsFixture) deliveries(t *testing.T, repoID uuid.UUID) []string {
	t.Helper()
	ctx := context.Background()
	envs, err := f.c.Environments(ctx, repoID, "")
	if err != nil {
		t.Fatal(err)
	}
	var all []code.Delivery
	for _, e := range envs {
		list, err := f.c.Deliveries(ctx, repoID, e.ID, 0, 100)
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, list...)
	}
	slices.SortFunc(all, func(a, b code.Delivery) int { return int(a.Number - b.Number) })
	out := make([]string, 0, len(all))
	for _, d := range all {
		line := fmt.Sprintf("#%d %s %s@%s %s", d.Number, d.Target, d.Branch, d.SHA[:1], d.State)
		if d.Reason != "" {
			line += ": " + d.Reason
		}
		out = append(out, line)
	}
	return out
}

// environments lists the repository's environments as "target branch namespace sync", with " paused" when it is.
func (f *jobsFixture) environments(t *testing.T, repoID uuid.UUID, branch string) []string {
	t.Helper()
	envs, err := f.c.Environments(context.Background(), repoID, branch)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, 0, len(envs))
	for _, e := range envs {
		line := fmt.Sprintf("%s %s %s %s", e.Target.DisplayName, e.Branch, e.Namespace, e.Sync)
		if e.Paused {
			line += " paused"
		}
		out = append(out, line)
	}
	return out
}

func TestGitStoreQueuesDeliveries(t *testing.T) {
	t.Parallel()
	f := newJobsFixture(t)
	repo := f.website.ID
	shaD, shaE, shaF := strings.Repeat("d", 40), strings.Repeat("e", 40), strings.Repeat("f", 40)
	settings := func(branch string) git.TipSettings { return tip(t, branch, "blob-1", deliveryFile) }
	broken := tip(t, "feature/broken", "blob-2", "delivery: [x]\n")
	first := []string{"#1 production main@a queued"}
	second := []string{"#1 production main@a cancelled: superseded by #2", "#2 production main@b queued"}
	third := append(slices.Clone(second), "#3 previews feature/login@c waiting: waits for a member to start it")
	fourth := append(slices.Clone(third), "#4 previews feature/broken@e failed: "+broken.Error)
	fifth := append(slices.Clone(fourth), "#5 previews feature/bare@f queued")
	steps := []struct {
		name     string
		push     git.Push
		settings []git.TipSettings
		want     []string
	}{
		{name: "a member creates main", want: first,
			push:     pushOf(repo, f.olivia, "pack-1", []git.RefUpdate{{Name: "refs/heads/main", Old: git.ZeroSHA, New: shaA}}, commit(shaA, person("Olivia", "olivia@example.com"))),
			settings: []git.TipSettings{settings("main")}},
		{name: "a member moves main on", want: second,
			push:     pushOf(repo, f.olivia, "pack-2", []git.RefUpdate{{Name: "refs/heads/main", Old: shaA, New: shaB}}, commit(shaB, person("Olivia", "olivia@example.com"), shaA)),
			settings: []git.TipSettings{settings("main")}},
		{name: "an outsider pushes a feature", want: third,
			push:     pushOf(repo, f.nia, "pack-3", []git.RefUpdate{{Name: "refs/heads/feature/login", Old: git.ZeroSHA, New: shaC}}, commit(shaC, person("Nia", "nia@example.com"), shaB)),
			settings: []git.TipSettings{settings("feature/login")}},
		{name: "a feature whose namespace another holds", want: third,
			push:     pushOf(repo, f.max, "pack-4", []git.RefUpdate{{Name: "refs/heads/feature/Login", Old: git.ZeroSHA, New: shaD}}, commit(shaD, person("Max", "max@example.com"), shaB)),
			settings: []git.TipSettings{settings("feature/Login")}},
		{name: "a feature whose file doesn't parse", want: fourth,
			push:     pushOf(repo, f.max, "pack-5", []git.RefUpdate{{Name: "refs/heads/feature/broken", Old: git.ZeroSHA, New: shaE}}, commit(shaE, person("Max", "max@example.com"), shaB)),
			settings: []git.TipSettings{broken}},
		{name: "a feature without the file", want: fifth,
			push:     pushOf(repo, f.max, "pack-6", []git.RefUpdate{{Name: "refs/heads/feature/bare", Old: git.ZeroSHA, New: shaF}}, commit(shaF, person("Max", "max@example.com"), shaB)),
			settings: []git.TipSettings{{Branch: "feature/bare"}}},
		{name: "a branch no target delivers", want: fifth,
			push:     pushOf(repo, f.max, "", []git.RefUpdate{{Name: "refs/heads/docs", Old: git.ZeroSHA, New: shaB}}),
			settings: []git.TipSettings{settings("docs")}},
	}
	for _, step := range steps {
		f.push(t, step.push, step.settings...)
		if got := f.deliveries(t, repo); !slices.Equal(got, step.want) {
			t.Fatalf("%s: deliveries = %q, want %q", step.name, got, step.want)
		}
	}
	f.checkDeliveriesQueued(t)
}

// checkDeliveriesQueued checks what TestGitStoreQueuesDeliveries's pushes left beside the deliveries: the
// environments, the targets' annotations and their principals.
func (f *jobsFixture) checkDeliveriesQueued(t *testing.T) {
	t.Helper()
	repo := f.website.ID
	wantEnvs := []string{"previews feature/bare shop-feature-bare out_of_sync", "previews feature/broken shop-feature-broken out_of_sync",
		"previews feature/login shop-feature-login out_of_sync", "production main shop out_of_sync"}
	if got := f.environments(t, repo, ""); !slices.Equal(got, wantEnvs) {
		t.Errorf("environments = %q, want %q", got, wantEnvs)
	}
	if got := f.environments(t, repo, "main"); !slices.Equal(got, wantEnvs[3:]) {
		t.Errorf("main's environments = %q, want %q", got, wantEnvs[3:])
	}
	for _, check := range []struct{ sha, name, want string }{
		{shaA, "delivery/production", "failed: Superseded by #2 by production"},
		{shaB, "delivery/production", "pending: Queued by production"},
		{shaB, "delivery/previews", "none"},
		{shaC, "delivery/previews", "pending: Waits for a member to start it by previews"},
		{strings.Repeat("d", 40), "delivery/previews", "failed: Namespace shop-feature-login is environment previews of feature/login's by previews"},
		{strings.Repeat("e", 40), "delivery/previews", "failed: /cobranch.yaml: line 1: delivery must be a mapping by previews"},
	} {
		if got := f.note(t, repo, check.sha, check.name); got != check.want {
			t.Errorf("annotation %s at %s = %q, want %q", check.name, check.sha[:1], got, check.want)
		}
	}
	if n := count(t, f.pool, "SELECT count(*) FROM principals p JOIN delivery_targets d ON d.principal_id = p.id WHERE p.kind = 'target' AND p.org_id = $1", f.acme.ID); n != 2 {
		t.Errorf("%d target principals, want production and previews", n)
	}
}

func TestGitStoreRefusesAnotherRepositorysNamespace(t *testing.T) {
	t.Parallel()
	f := newJobsFixture(t)
	for _, repo := range []uuid.UUID{f.website.ID, f.secret.ID} {
		f.push(t, pushOf(repo, f.olivia, "pack-1", []git.RefUpdate{{Name: "refs/heads/main", Old: git.ZeroSHA, New: shaA}}, commit(shaA, person("Olivia", "olivia@example.com"))),
			tip(t, "main", "blob-1", deliveryFile))
	}
	if got := f.deliveries(t, f.secret.ID); len(got) != 0 {
		t.Errorf("the second repository's deliveries = %q, want none", got)
	}
	want := "failed: Namespace shop is another repository's environment's by production"
	if got := f.note(t, f.secret.ID, shaA, "delivery/production"); got != want {
		t.Errorf("the second repository's annotation = %q, want %q", got, want)
	}
}

// pushTwoDeliveries has Olivia push main, queueing delivery #1 to production, then Nia, an outsider, push
// feature/login, whose delivery #2 to its preview waits for a member.
func (f *jobsFixture) pushTwoDeliveries(t *testing.T) (production, preview code.Environment) {
	t.Helper()
	repo := f.website.ID
	f.push(t, pushOf(repo, f.olivia, "pack-1", []git.RefUpdate{{Name: "refs/heads/main", Old: git.ZeroSHA, New: shaA}}, commit(shaA, person("Olivia", "olivia@example.com"))),
		tip(t, "main", "blob-1", deliveryFile))
	f.push(t, pushOf(repo, f.nia, "pack-2", []git.RefUpdate{{Name: "refs/heads/feature/login", Old: git.ZeroSHA, New: shaB}}, commit(shaB, person("Nia", "nia@example.com"), shaA)),
		tip(t, "feature/login", "blob-1", deliveryFile))
	envs, err := f.c.Environments(context.Background(), repo, "")
	if err != nil || len(envs) != 2 {
		t.Fatalf("Environments() = %+v, %v", envs, err)
	}
	return envs[1], envs[0]
}

func TestCodeStoreStartAndRedeliver(t *testing.T) {
	t.Parallel()
	f := newJobsFixture(t)
	ctx := context.Background()
	repo := f.website.ID
	production, _ := f.pushTwoDeliveries(t)

	if _, err := f.c.StartDelivery(ctx, f.olivia, repo, 1); !errors.Is(err, code.ErrDeliveryNotWaiting) {
		t.Errorf("StartDelivery(a queued delivery) = %v, want %v", err, code.ErrDeliveryNotWaiting)
	}
	started, err := f.c.StartDelivery(ctx, f.olivia, repo, 2)
	if err != nil || started.State != code.DeliveryQueued || started.StartedBy == nil || started.StartedBy.ID != f.olivia || started.Version != 2 {
		t.Fatalf("StartDelivery() = %+v, %v; want it queued for Olivia", started, err)
	}
	if got := f.note(t, repo, shaB, "delivery/previews"); got != "pending: Queued by previews" {
		t.Errorf("a started delivery's annotation = %q", got)
	}
	again, err := f.c.Redeliver(ctx, f.max, repo, production.ID)
	if err != nil || again.Number != 3 || again.Trigger != code.TriggeredByRedeliver || again.SHA != shaA || again.StartedBy == nil || again.StartedBy.ID != f.max {
		t.Fatalf("Redeliver() = %+v, %v; want #3 of a, for Max", again, err)
	}
	want := []string{"#1 production main@a cancelled: superseded by #3", "#2 previews feature/login@b queued", "#3 production main@a queued"}
	if got := f.deliveries(t, repo); !slices.Equal(got, want) {
		t.Errorf("deliveries = %q, want %q", got, want)
	}
	if got := f.note(t, repo, shaA, "delivery/production"); got != "pending: Queued by production" {
		t.Errorf("a redelivered commit's annotation = %q", got)
	}
	for kind, actor := range map[string]uuid.UUID{"delivery.started": f.olivia, "delivery.redelivered": f.max} {
		if n := count(t, f.pool, "SELECT count(*) FROM events WHERE kind = $1 AND actor_id = $2", kind, actor); n != 1 {
			t.Errorf("%d %s events, want 1", n, kind)
		}
	}
}

func TestCodeStoreRedeliversABrokenTip(t *testing.T) {
	t.Parallel()
	f := newJobsFixture(t)
	ctx := context.Background()
	repo := f.website.ID
	broken := tip(t, "feature/broken", "blob-2", "delivery: [x]\n")
	f.push(t, pushOf(repo, f.olivia, "pack-1", []git.RefUpdate{{Name: "refs/heads/main", Old: git.ZeroSHA, New: shaA}}, commit(shaA, person("Olivia", "olivia@example.com"))),
		tip(t, "main", "blob-1", deliveryFile))
	f.push(t, pushOf(repo, f.max, "pack-2", []git.RefUpdate{{Name: "refs/heads/feature/broken", Old: git.ZeroSHA, New: shaB}}, commit(shaB, person("Max", "max@example.com"), shaA)),
		broken)
	envs, err := f.c.Environments(ctx, repo, "feature/broken")
	if err != nil || len(envs) != 1 {
		t.Fatalf("Environments(feature/broken) = %+v, %v", envs, err)
	}
	preview := envs[0].ID
	if _, err := f.c.Redeliver(ctx, f.max, repo, preview); err != nil {
		t.Fatalf("Redeliver() = %v", err)
	}
	if _, err := f.c.PauseEnvironment(ctx, f.max, repo, preview); err != nil {
		t.Fatalf("PauseEnvironment() = %v", err)
	}
	if _, err := f.c.ResumeEnvironment(ctx, f.max, repo, preview); err != nil {
		t.Fatalf("ResumeEnvironment() = %v", err)
	}
	failed := "failed: " + broken.Error
	want := []string{"#1 production main@a queued", "#2 previews feature/broken@b " + failed, "#3 previews feature/broken@b " + failed,
		"#4 previews feature/broken@b " + failed}
	if got := f.deliveries(t, repo); !slices.Equal(got, want) {
		t.Errorf("deliveries = %q, want %q", got, want)
	}
	if got, want := f.note(t, repo, shaB, "delivery/previews"), "failed: /cobranch.yaml: line 1: delivery must be a mapping by previews"; got != want {
		t.Errorf("the broken tip's annotation = %q, want %q", got, want)
	}
}

// TestCodeStoreWaitsForAQueueingPush checks that a redeliver and a pause wait for a push that holds the repository's
// delivery queue, as a push worker's transaction does while it queues, and act on what it leaves: a pause cancels the
// delivery the push queued, and a redeliver delivers the head the push moved its branch to.
func TestCodeStoreWaitsForAQueueingPush(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		// push is what the push does in tx while it holds the queue.
		push func(t *testing.T, f *jobsFixture, tx pgx.Tx, production code.Environment)
		act  func(f *jobsFixture, production code.Environment) error
		want []string
	}{
		{name: "a pause cancels the push's delivery",
			push: func(t *testing.T, _ *jobsFixture, tx pgx.Tx, production code.Environment) {
				q := queries.New(tx)
				env, err := q.LiveEnvironment(context.Background(), production.Target.ID, "main")
				if err == nil {
					_, err = enqueue(context.Background(), q, queuedDelivery{env: env, target: "production", sha: shaA,
						trigger: code.TriggeredByPush, state: code.DeliveryQueued})
				}
				if err != nil {
					t.Error(err)
				}
			},
			act: func(f *jobsFixture, production code.Environment) error {
				_, err := f.c.PauseEnvironment(context.Background(), f.max, f.website.ID, production.ID)
				return err
			},
			want: []string{"#1 production main@a cancelled: superseded by #3", "#2 previews feature/login@b waiting: waits for a member to start it",
				"#3 production main@a cancelled: paused by Alice"}}, // the fixture names every user Alice
		{name: "a redeliver delivers the push's head",
			push: func(t *testing.T, f *jobsFixture, tx pgx.Tx, _ code.Environment) {
				if _, err := tx.Exec(context.Background(), "UPDATE branches SET head = $1 WHERE repo_id = $2 AND name = 'main'", shaC, f.website.ID); err != nil {
					t.Error(err)
				}
			},
			act: func(f *jobsFixture, production code.Environment) error {
				_, err := f.c.Redeliver(context.Background(), f.max, f.website.ID, production.ID)
				return err
			},
			want: []string{"#1 production main@a cancelled: superseded by #3", "#2 previews feature/login@b waiting: waits for a member to start it",
				"#3 production main@c queued"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newJobsFixture(t)
			production, _ := f.pushTwoDeliveries(t)
			f.push(t, pushOf(f.website.ID, f.olivia, "pack-3", []git.RefUpdate{{Name: "refs/heads/docs", Old: git.ZeroSHA, New: shaC}},
				commit(shaC, person("Olivia", "olivia@example.com"), shaA)))
			f.whileQueueing(t, func(tx pgx.Tx) { tc.push(t, f, tx, production) }, func() error { return tc.act(f, production) })
			if got := f.deliveries(t, f.website.ID); !slices.Equal(got, tc.want) {
				t.Errorf("deliveries = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestCodeStorePushQueuesBeforeItLocksBranches checks that a push waits for an environment's action before it locks a
// branch: the action holds the delivery queue and then locks the branches at its commit, so a push that locked a branch
// first and waited for the queue after would deadlock with it.
func TestCodeStorePushQueuesBeforeItLocksBranches(t *testing.T) {
	t.Parallel()
	f := newJobsFixture(t)
	ctx := context.Background()
	f.pushTwoDeliveries(t)
	// Its job's run restatuses feature/login, at c, before its deliveries queue.
	p := pushOf(f.website.ID, f.nia, "pack-3", []git.RefUpdate{{Name: "refs/heads/feature/login", Old: shaB, New: shaC}},
		commit(shaC, person("Nia", "nia@example.com"), shaB))
	p.Settings = []git.TipSettings{tip(t, "feature/login", "blob-2", deliveryFile+jobsFile)}
	p.Facts = []git.Facts{{Branch: "feature/login", Ahead: 2}}
	if err := f.g.Record(ctx, p); err != nil {
		t.Fatal(err)
	}
	f.whileQueueing(t, func(tx pgx.Tx) {
		if _, err := tx.Exec(ctx, "SELECT id FROM branches WHERE repo_id = $1 AND name = 'feature/login' FOR UPDATE", f.website.ID); err != nil {
			t.Errorf("locking feature/login while the queue is held: %v", err)
		}
	}, func() error {
		_, err := f.g.Process(ctx, git.RetryAfter)
		return err
	})
	want := []string{"#1 production main@a queued", "#2 previews feature/login@b cancelled: superseded by #3",
		"#3 previews feature/login@c waiting: waits for a member to start it"}
	if got := f.deliveries(t, f.website.ID); !slices.Equal(got, want) {
		t.Errorf("deliveries = %q, want %q", got, want)
	}
}

// whileQueueing holds the website's delivery queue in a transaction, as a push worker or an environment's action does,
// starts act, checks that it waits, runs then in the transaction and commits it, and waits for act.
func (f *jobsFixture) whileQueueing(t *testing.T, then func(tx pgx.Tx), act func() error) {
	t.Helper()
	ctx := context.Background()
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // committed below; rolling back a committed transaction does nothing
	if err := queries.New(tx).LockDeliveryQueue(ctx, f.website.ID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- act() }()
	select {
	case err := <-done:
		t.Fatalf("acted while the queue was held: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	then(tx)
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestCodeStorePauseAndResume(t *testing.T) {
	t.Parallel()
	f := newJobsFixture(t)
	ctx := context.Background()
	repo := f.website.ID
	production, _ := f.pushTwoDeliveries(t)

	paused, err := f.c.PauseEnvironment(ctx, f.max, repo, production.ID)
	if err != nil || !paused.Paused || paused.Latest == nil || paused.Latest.State != code.DeliveryCancelled ||
		!strings.HasPrefix(paused.Latest.Reason, "paused by ") {
		t.Fatalf("PauseEnvironment() = %+v, %v; want it paused, #1 cancelled", paused, err)
	}
	if _, err := f.c.PauseEnvironment(ctx, f.max, repo, production.ID); !errors.Is(err, code.ErrEnvironmentPaused) {
		t.Errorf("PauseEnvironment(again) = %v, want %v", err, code.ErrEnvironmentPaused)
	}
	if _, err := f.c.Redeliver(ctx, f.max, repo, production.ID); !errors.Is(err, code.ErrEnvironmentPaused) {
		t.Errorf("Redeliver(paused) = %v, want %v", err, code.ErrEnvironmentPaused)
	}
	f.push(t, pushOf(repo, f.olivia, "pack-3", []git.RefUpdate{{Name: "refs/heads/main", Old: shaA, New: shaC}}, commit(shaC, person("Olivia", "olivia@example.com"), shaA)),
		tip(t, "main", "blob-1", deliveryFile))
	if got := f.note(t, repo, shaC, "delivery/production"); got != "pending: The environment is paused by production" {
		t.Errorf("a push to a paused environment's annotation = %q", got)
	}
	if _, err := f.c.ResumeEnvironment(ctx, f.olivia, repo, production.ID); err != nil {
		t.Fatalf("ResumeEnvironment() = %v", err)
	}
	if _, err := f.c.ResumeEnvironment(ctx, f.olivia, repo, production.ID); !errors.Is(err, code.ErrEnvironmentNotPaused) {
		t.Errorf("ResumeEnvironment(again) = %v, want %v", err, code.ErrEnvironmentNotPaused)
	}
	want := []string{"#1 production main@a cancelled: " + paused.Latest.Reason, "#2 previews feature/login@b waiting: waits for a member to start it",
		"#3 production main@c queued"}
	if got := f.deliveries(t, repo); !slices.Equal(got, want) {
		t.Errorf("deliveries = %q, want %q", got, want)
	}
	resumed, err := f.c.Delivery(ctx, repo, 3)
	if err != nil || resumed.Trigger != code.TriggeredByResume || resumed.StartedBy == nil || resumed.StartedBy.ID != f.olivia {
		t.Errorf("Delivery(#3) = %+v, %v; want Olivia's resume", resumed, err)
	}
	for kind, actor := range map[string]uuid.UUID{"environment.paused": f.max, "environment.resumed": f.olivia} {
		if n := count(t, f.pool, "SELECT count(*) FROM events WHERE kind = $1 AND actor_id = $2", kind, actor); n != 1 {
			t.Errorf("%d %s events, want 1", n, kind)
		}
	}
}

func TestCodeStoreReadsDeliveries(t *testing.T) {
	t.Parallel()
	f := newJobsFixture(t)
	ctx := context.Background()
	repo := f.website.ID
	production, preview := f.pushTwoDeliveries(t)
	for _, number := range []int64{3, 0} {
		if _, err := f.c.StartDelivery(ctx, f.max, repo, number); !errors.Is(err, repos.ErrNotFound) {
			t.Errorf("StartDelivery(#%d) = %v, want %v", number, err, repos.ErrNotFound)
		}
		if _, err := f.c.Delivery(ctx, repo, number); !errors.Is(err, repos.ErrNotFound) {
			t.Errorf("Delivery(#%d) = %v, want %v", number, err, repos.ErrNotFound)
		}
	}
	if _, err := f.c.Delivery(ctx, f.secret.ID, 1); !errors.Is(err, repos.ErrNotFound) {
		t.Errorf("another repository's Delivery(#1) = %v, want %v", err, repos.ErrNotFound)
	}
	for name, call := range map[string]func() error{
		"Environment": func() error { _, err := f.c.Environment(ctx, f.secret.ID, production.ID); return err },
		"Deliveries":  func() error { _, err := f.c.Deliveries(ctx, f.secret.ID, production.ID, 0, 10); return err },
		"Redeliver":   func() error { _, err := f.c.Redeliver(ctx, f.max, f.secret.ID, production.ID); return err },
		"Pause":       func() error { _, err := f.c.PauseEnvironment(ctx, f.max, f.secret.ID, production.ID); return err },
		"Resume":      func() error { _, err := f.c.ResumeEnvironment(ctx, f.max, f.secret.ID, production.ID); return err },
	} {
		if err := call(); !errors.Is(err, repos.ErrNotFound) {
			t.Errorf("another repository's %s() = %v, want %v", name, err, repos.ErrNotFound)
		}
	}
	got, err := f.c.Environment(ctx, repo, preview.ID)
	if err != nil || got.Target.DisplayName != "previews" || got.Branch != "feature/login" || got.Namespace != "shop-feature-login" ||
		got.Cluster != nil || got.Health != code.Unknown || got.Latest == nil || got.Latest.Number != 2 || got.Version < 1 {
		t.Errorf("Environment(preview) = %+v, %v", got, err)
	}
	d, err := f.c.Delivery(ctx, repo, 1)
	if err != nil || d.Kind != code.DeliverCommit || d.Trigger != code.TriggeredByPush || d.PushID == nil || d.StartedBy != nil ||
		d.EnvironmentID != production.ID || d.Target != "production" || d.Branch != "main" {
		t.Errorf("Delivery(#1) = %+v, %v", d, err)
	}
	if page, err := f.c.Deliveries(ctx, repo, preview.ID, 2, 10); err != nil || len(page) != 0 {
		t.Errorf("Deliveries(before #2) = %+v, %v", page, err)
	}
}

func TestDeliveryRunJoinsTheBuiltInRoles(t *testing.T) {
	t.Parallel()
	f := newJobsFixture(t)
	n := count(t, f.pool, "SELECT count(*) FROM roles WHERE built_in AND 'delivery.run' = ANY (permissions) AND 'jobs.run' = ANY (permissions)")
	if m := count(t, f.pool, "SELECT count(*) FROM roles WHERE built_in AND 'jobs.run' = ANY (permissions)"); n != 3 || m != 3 {
		t.Errorf("%d built-in roles hold delivery.run and %d jobs.run, want Owner, Developer and Maintainer both", n, m)
	}
}
