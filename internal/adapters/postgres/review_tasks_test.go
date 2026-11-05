package postgres

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/git"
	"cobranch.dev/internal/services/repos"
)

// giveReviewerModel has the organization's AI reviewer work on a model of a provider of its own.
func giveReviewerModel(t *testing.T, f *jobsFixture, orgID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	exec(ctx, t, f.pool, "INSERT INTO model_providers (org_id, provider) VALUES ($1, 'acme-models')", orgID)
	exec(ctx, t, f.pool, "INSERT INTO models (org_id, provider, model) VALUES ($1, 'acme-models', 'review-1')", orgID)
	exec(ctx, t, f.pool, "UPDATE agents SET provider = 'acme-models', model = 'review-1' WHERE org_id = $1 AND purpose = 'review'", orgID)
}

// reviewTasks lists the repository's review tasks, oldest first, as "#n branch@sha state: reason".
func reviewTasks(t *testing.T, f *jobsFixture, repoID uuid.UUID) []string {
	t.Helper()
	rows, err := f.pool.Query(context.Background(), `SELECT t.number, b.name, t.sha, t.state, t.reason FROM agent_tasks t
		JOIN branches b ON b.id = t.branch_id WHERE t.repo_id = $1 AND t.kind = 'review' ORDER BY t.number`, repoID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var number int64
		var branch, sha, state, reason string
		if err := rows.Scan(&number, &branch, &sha, &state, &reason); err != nil {
			t.Fatal(err)
		}
		line := fmt.Sprintf("#%d %s@%s %s", number, branch, sha[:1], state)
		if reason != "" {
			line += ": " + reason
		}
		out = append(out, line)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// refs is a push by pusher into repo of updates, bringing commits.
func refs(repo, pusher uuid.UUID, updates []git.RefUpdate, commits ...git.Commit) git.Push {
	pack := ""
	if len(commits) > 0 {
		pack = "pack-" + commits[0].SHA[:1]
	}
	return pushOf(repo, pusher, pack, updates, commits...)
}

// reviewStep is a push into the fixture's repository and the review tasks it leaves, after its setup.
type reviewStep struct {
	name   string
	before func()
	push   git.Push
	want   []string
}

// reviewSteps are pushes into the website that queue, supersede, hold, cancel and skip reviews, one after another.
func reviewSteps(t *testing.T, f *jobsFixture) []reviewStep {
	t.Helper()
	repo := f.website.ID
	shaD, shaE, shaF := strings.Repeat("d", 40), strings.Repeat("e", 40), strings.Repeat("f", 40)
	olivia := person("Olivia", "olivia@example.com")
	queued := []string{"#1 feature@c queued"}
	moved := []string{"#1 feature@c cancelled: A newer push to the branch superseded it.", "#2 feature@d queued"}
	outside := append(slices.Clone(moved), "#3 outside@e waiting: "+outsideReason)
	deleted := []string{moved[0], "#2 feature@d cancelled: The branch was deleted.", outside[2]}
	again := append(slices.Clone(deleted), "#4 again@d queued")
	docs := append(slices.Clone(again), "#5 docs/new@f queued")
	onlyDocs := git.DefaultReviewSettings()
	onlyDocs.Branches = []string{"docs/*"}
	settingsPush := refs(repo, f.olivia, []git.RefUpdate{{Name: "refs/heads/main", Old: shaC, New: shaB}}, commit(shaB, olivia, shaA))
	settingsPush.ReviewSettings = &onlyDocs
	return []reviewStep{
		{name: "a reviewer without a model reviews nothing",
			push: refs(repo, f.olivia, []git.RefUpdate{{Name: "refs/heads/main", Old: git.ZeroSHA, New: shaA}, {Name: "refs/heads/feature", Old: git.ZeroSHA, New: shaA}},
				commit(shaA, olivia))},
		{name: "a push to a branch queues its review", before: func() { giveReviewerModel(t, f, f.acme.ID) }, want: queued,
			push: refs(repo, f.olivia, []git.RefUpdate{{Name: "refs/heads/feature", Old: shaA, New: shaC}}, commit(shaC, olivia, shaA))},
		{name: "main's pushes are never reviewed", want: queued,
			push: refs(repo, f.olivia, []git.RefUpdate{{Name: "refs/heads/main", Old: shaA, New: shaC}})},
		{name: "a newer push supersedes the open review", want: moved,
			push: refs(repo, f.max, []git.RefUpdate{{Name: "refs/heads/feature", Old: shaC, New: shaD}}, commit(shaD, olivia, shaC))},
		{name: "an outsider's push waits for a member", want: outside,
			push: refs(repo, f.nia, []git.RefUpdate{{Name: "refs/heads/outside", Old: git.ZeroSHA, New: shaE}}, commit(shaE, olivia, shaC))},
		{name: "deleting the branch cancels its review", want: deleted,
			push: refs(repo, f.olivia, []git.RefUpdate{{Name: "refs/heads/feature", Old: shaD, New: git.ZeroSHA}})},
		{name: "another branch at a reviewed commit gets its own review", want: again,
			push: refs(repo, f.olivia, []git.RefUpdate{{Name: "refs/heads/again", Old: git.ZeroSHA, New: shaD}})},
		{name: "settings that take docs branches alone", want: again, push: settingsPush},
		{name: "a branch the settings leave out", want: again,
			push: refs(repo, f.olivia, []git.RefUpdate{{Name: "refs/heads/other", Old: git.ZeroSHA, New: shaD}})},
		{name: "a branch the settings take in", want: docs,
			push: refs(repo, f.olivia, []git.RefUpdate{{Name: "refs/heads/docs/new", Old: git.ZeroSHA, New: shaF}}, commit(shaF, olivia, shaB))},
		{name: "a suspended reviewer reviews nothing", want: docs,
			before: func() {
				exec(context.Background(), t, f.pool, "UPDATE principals SET suspended = true WHERE id IN (SELECT principal_id FROM agents WHERE purpose = 'review')")
			},
			push: refs(repo, f.olivia, []git.RefUpdate{{Name: "refs/heads/docs/more", Old: git.ZeroSHA, New: shaF}})},
	}
}

func TestGitStoreQueuesReviews(t *testing.T) {
	t.Parallel()
	f := newJobsFixture(t)
	repo := f.website.ID
	for _, step := range reviewSteps(t, f) {
		if step.before != nil {
			step.before()
		}
		f.push(t, step.push)
		if got := reviewTasks(t, f, repo); !slices.Equal(got, step.want) {
			t.Fatalf("%s: reviews = %q, want %q", step.name, got, step.want)
		}
	}
	if n := count(t, f.pool, "SELECT count(*) FROM events WHERE kind = 'task.assigned' AND details->>'kind' = 'review'"); n != 5 {
		t.Errorf("task.assigned events = %d, want 5", n)
	}
	if n := count(t, f.pool, "SELECT count(*) FROM events WHERE kind = 'task.cancelled'"); n != 2 {
		t.Errorf("task.cancelled events = %d, want 2", n)
	}
}

func TestCodeStoreRequestReview(t *testing.T) {
	t.Parallel()
	f := newJobsFixture(t)
	ctx := context.Background()
	repo := f.website.ID
	olivia := person("Olivia", "olivia@example.com")
	f.push(t, refs(repo, f.olivia, []git.RefUpdate{{Name: "refs/heads/main", Old: git.ZeroSHA, New: shaA}}, commit(shaA, olivia)))
	if _, err := f.c.RequestReview(ctx, f.olivia, repo, "main"); !errors.Is(err, code.ErrReviewerNoModel) {
		t.Fatalf("RequestReview() without a model = %v, want %v", err, code.ErrReviewerNoModel)
	}
	giveReviewerModel(t, f, f.acme.ID)
	f.push(t, refs(repo, f.nia, []git.RefUpdate{{Name: "refs/heads/outside", Old: git.ZeroSHA, New: shaB}}, commit(shaB, olivia, shaA)))
	got, err := f.c.RequestReview(ctx, f.max, repo, "outside")
	if err != nil || got != (code.AIReview{Task: 2, State: code.ReviewQueued}) {
		t.Fatalf("RequestReview() = %+v, %v, want task 2, queued", got, err)
	}
	want := []string{"#1 outside@b cancelled: Another review was asked for.", "#2 outside@b queued"}
	if tasks := reviewTasks(t, f, repo); !slices.Equal(tasks, want) {
		t.Errorf("reviews = %q, want %q", tasks, want)
	}
	if n := count(t, f.pool, "SELECT count(*) FROM agent_tasks WHERE number = 1 AND cancelled_by = $1", f.max); n != 1 {
		t.Errorf("tasks cancelled by Max = %d, want 1", n)
	}
	if n := count(t, f.pool, "SELECT count(*) FROM events WHERE kind = 'task.assigned' AND actor_id = $1", f.max); n != 1 {
		t.Errorf("task.assigned events by Max = %d, want 1", n)
	}
	branch, err := f.c.Branch(ctx, repo, "outside")
	if err != nil || branch.AIReview == nil || *branch.AIReview != got {
		t.Errorf("Branch().AIReview = %+v, %v, want %+v", branch.AIReview, err, got)
	}
	if _, err := f.c.RequestReview(ctx, f.max, repo, "gone"); !errors.Is(err, repos.ErrNotFound) {
		t.Errorf("RequestReview() of a branch that isn't = %v, want %v", err, repos.ErrNotFound)
	}
	exec(ctx, t, f.pool, `INSERT INTO review_settings (repo_id, org_id, enabled, branches, ignore, instructions, include, max_diff_lines)
		VALUES ($1, $2, false, '{"**"}', '{}', '', '{}', 5000)`, repo, f.acme.ID)
	if _, err := f.c.RequestReview(ctx, f.max, repo, "outside"); !errors.Is(err, code.ErrReviewOff) {
		t.Errorf("RequestReview() while the settings turn the reviewer off = %v, want %v", err, code.ErrReviewOff)
	}
}
