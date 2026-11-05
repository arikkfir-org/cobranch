package postgres

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/git"
)

// activityLine says what a is as "kind by repo: what", naming its actor from who.
func activityLine(a code.Activity, who map[uuid.UUID]string) string {
	line := fmt.Sprintf("%s by %s in %s", a.Kind, who[a.Actor.ID], a.Repo)
	switch a.Kind {
	case code.ActivityPushed:
		for _, u := range a.Refs {
			line += " " + strings.TrimPrefix(u.Name, "refs/heads/") + "@" + u.New[:1]
		}
		line += fmt.Sprintf(" (%d commits)", a.Commits)
	case code.ActivityMerged:
		for _, h := range a.Merged {
			line += " " + h.Branch + "@" + h.SHA[:1]
		}
	case code.ActivityReviewed:
		line += fmt.Sprintf(" %s %s on %q", a.Review, a.SHA[:1], a.Branch)
	case code.ActivityDelivered:
		line += fmt.Sprintf(" #%d %s %s to %s: %s", a.Delivery.Number, a.Delivery.Kind, a.SHA[:1], a.Delivery.Environment, a.Delivery.State)
	case code.ActivityCreated:
	}
	return line
}

// seedActivity gives f's website, beside its pushes, a review, a merge into main and two deliveries that ended, the
// second applying a render again, and names who acted in it by their IDs.
func seedActivity(t *testing.T, f *jobsFixture) map[uuid.UUID]string {
	t.Helper()
	ctx := context.Background()
	repo := f.website.ID
	production, preview := f.pushTwoDeliveries(t)
	if _, err := f.c.SubmitReview(ctx, f.max, repo, shaB, code.ReviewInput{State: code.ReviewApproved}); err != nil {
		t.Fatal(err)
	}
	exec(ctx, t, f.pool, "UPDATE deliveries SET state = 'succeeded', ended_at = now() WHERE repo_id = $1 AND number = 1", repo)
	exec(ctx, t, f.pool, "UPDATE deliveries SET kind = 'resync', state = 'succeeded', ended_at = now() WHERE repo_id = $1 AND number = 2", repo)
	merge := pushOf(repo, f.olivia, "", []git.RefUpdate{{Name: "refs/heads/main", Old: shaA, New: shaB}})
	merge.Brought = []git.Brought{{Branch: "main", Heads: []git.Head{{Branch: "feature/login", SHA: shaB}}}}
	f.push(t, merge)
	return map[uuid.UUID]string{f.olivia: "Olivia", f.max: "Max", f.nia: "Nia", production.Target.ID: "production", preview.Target.ID: "previews"}
}

// TestCodeStoreActivity checks that an organization's activity lists, newest first, its repositories' creation, pushes,
// merges, reviews and the deliveries that ended, but those that applied a render again, as their target; only in the
// repositories asked for, a page at a time.
func TestCodeStoreActivity(t *testing.T) {
	t.Parallel()
	f := newJobsFixture(t)
	ctx := context.Background()
	who := seedActivity(t, f)
	all := []string{
		"merged by Olivia in website feature/login@b",
		"delivered by production in website #1 deliver a to main: succeeded",
		`reviewed by Max in website approved b on "feature/login"`,
		"pushed by Nia in website feature/login@b (1 commits)",
		"pushed by Olivia in website main@a (1 commits)",
		"created by Olivia in secret",
		"created by Olivia in website",
	}
	both := []uuid.UUID{f.website.ID, f.secret.ID}
	full, err := f.c.Activity(ctx, f.acme.ID, both, code.ActivityCursor{}, 100)
	if err != nil || len(full) != len(all) {
		t.Fatalf("Activity() = %d, %v, want %d", len(full), err, len(all))
	}
	tests := []struct {
		name   string
		repos  []uuid.UUID
		before code.ActivityCursor
		limit  int
		want   []string
	}{
		{name: "everything, newest first", repos: both, limit: 100, want: all},
		{name: "a page", repos: both, limit: 2, want: all[:2]},
		{name: "the next page", repos: both, before: code.ActivityCursor{At: full[1].At, ID: full[1].ID}, limit: 3, want: all[2:5]},
		{name: "only the repositories asked for", repos: []uuid.UUID{f.secret.ID}, limit: 100, want: all[5:6]},
		{name: "no repositories", limit: 100},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := f.c.Activity(ctx, f.acme.ID, tt.repos, tt.before, tt.limit)
			if err != nil {
				t.Fatal(err)
			}
			var lines []string
			for _, a := range got {
				lines = append(lines, activityLine(a, who))
				if a.Actor.DisplayName == "" || a.Actor.Kind == "" || a.At.IsZero() {
					t.Errorf("Activity() %s acted by %+v at %v, want a named actor of a kind, and a time", a.Kind, a.Actor, a.At)
				}
			}
			if !slices.Equal(lines, tt.want) {
				t.Errorf("Activity() =\n%s\nwant\n%s", strings.Join(lines, "\n"), strings.Join(tt.want, "\n"))
			}
		})
	}
	if other, err := f.c.Activity(ctx, uuid.New(), both, code.ActivityCursor{}, 100); err != nil || len(other) != 0 {
		t.Errorf("another organization's Activity() = %v, %v, want none", other, err)
	}
}
