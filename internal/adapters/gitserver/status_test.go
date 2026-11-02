package gitserver_test

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"cobranch.dev/internal/adapters/postgres"
	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/git"
)

// requireBuild is a /cobranch.yaml whose main requires ci/build and linear history.
const requireBuild = "branches:\n  main:\n    requires:\n      annotations: [ci/build]\n      linearHistory: true\n"

// statusRow is what a branch's row says of it as a request to merge.
type statusRow struct {
	Status string
	Ahead  int
	Behind int
	Merges *int
	Checks []map[string]any
}

// branchStatus reads branch name's row in Acme's website.
func branchStatus(t *testing.T, s *stack, name string) statusRow {
	t.Helper()
	var row statusRow
	var checks []byte
	if err := s.pool.QueryRow(context.Background(), "SELECT status, ahead, behind, merges, coalesce(checks, '[]') FROM branches WHERE repo_id = $1 AND name = $2 AND deleted_at IS NULL",
		website, name).Scan(&row.Status, &row.Ahead, &row.Behind, &row.Merges, &checks); err != nil {
		t.Fatalf("reading branch %s: %v", name, err)
	}
	if err := json.Unmarshal(checks, &row.Checks); err != nil {
		t.Fatal(err)
	}
	return row
}

// branchStatusCase is a repository built with main, perhaps with a /cobranch.yaml, and branch feature, perhaps with an
// annotation on its head, and the row feature should have once the push worker processed it.
type branchStatusCase struct {
	name     string
	settings string
	build    func(t *testing.T, dir string)
	annotate code.AnnotationStatus
	want     statusRow
}

// branchStatusCases are TestBranchStatus's cases, a branch for each status and each check.
func branchStatusCases() []branchStatusCase {
	one := 1
	zero := 0
	feature := func(t *testing.T, dir string) {
		mustGit(t, dir, "checkout", "-q", "-b", "feature")
		commit(t, dir, "feature.txt", "a feature\n")
		mustGit(t, dir, "push", "-q", "origin", "feature")
	}
	return []branchStatusCase{
		{name: "nothing required, a commit to merge", build: feature,
			want: statusRow{Status: "ready", Ahead: 1, Checks: []map[string]any{{"kind": "up_to_date", "met": true}}}},
		{name: "a required annotation missing", settings: requireBuild, build: feature,
			want: statusRow{Status: "blocked", Ahead: 1, Merges: &zero, Checks: []map[string]any{
				{"kind": "annotation", "name": "ci/build", "met": false}, {"kind": "linear_history", "met": true}, {"kind": "up_to_date", "met": true}}}},
		{name: "a required annotation running", settings: requireBuild, build: feature, annotate: code.AnnotationInProgress,
			want: statusRow{Status: "processing", Ahead: 1, Merges: &zero, Checks: []map[string]any{
				{"kind": "annotation", "name": "ci/build", "status": "in_progress", "met": false}, {"kind": "linear_history", "met": true}, {"kind": "up_to_date", "met": true}}}},
		{name: "a required annotation successful", settings: requireBuild, build: feature, annotate: code.AnnotationSuccessful,
			want: statusRow{Status: "ready", Ahead: 1, Merges: &zero, Checks: []map[string]any{
				{"kind": "annotation", "name": "ci/build", "status": "successful", "met": true}, {"kind": "linear_history", "met": true}, {"kind": "up_to_date", "met": true}}}},
		{name: "behind main", build: func(t *testing.T, dir string) {
			feature(t, dir)
			mustGit(t, dir, "checkout", "-q", "main")
			commit(t, dir, "main.txt", "main moves on\n")
			mustGit(t, dir, "push", "-q", "origin", "main")
		}, want: statusRow{Status: "blocked", Ahead: 1, Behind: 1, Checks: []map[string]any{{"kind": "up_to_date", "count": 1.0, "met": false}}}},
		{name: "a merge where linear history is required", settings: requireBuild, annotate: code.AnnotationSuccessful, build: func(t *testing.T, dir string) {
			mustGit(t, dir, "checkout", "-q", "-b", "side")
			commit(t, dir, "side.txt", "a side line\n")
			mustGit(t, dir, "checkout", "-q", "-b", "feature", "main")
			commit(t, dir, "feature.txt", "a feature\n")
			mustGit(t, dir, "merge", "-q", "--no-ff", "-m", "Merge side", "side")
			mustGit(t, dir, "push", "-q", "origin", "feature")
		}, want: statusRow{Status: "blocked", Ahead: 3, Merges: &one, Checks: []map[string]any{
			{"kind": "annotation", "name": "ci/build", "status": "successful", "met": true}, {"kind": "linear_history", "count": 1.0, "met": false},
			{"kind": "up_to_date", "met": true}}}},
		{name: "at main's tip", build: func(t *testing.T, dir string) { mustGit(t, dir, "push", "-q", "origin", "main:feature") },
			want: statusRow{Status: "empty", Checks: []map[string]any{{"kind": "up_to_date", "met": true}}}},
	}
}

func TestBranchStatus(t *testing.T) {
	t.Parallel()
	for _, tt := range branchStatusCases() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := newStack(t)
			dir := clone(t, s.websiteURL(s.base, olivia))
			mustGit(t, dir, "checkout", "-q", "-b", "main")
			commit(t, dir, "README.md", "# Website\n")
			if tt.settings != "" {
				commit(t, dir, "cobranch.yaml", tt.settings)
			}
			mustGit(t, dir, "push", "-q", "origin", "main")
			tt.build(t, dir)
			if got := branchStatus(t, s, "feature").Status; got != "processing" {
				t.Errorf("before the push worker, feature is %s, want processing", got)
			}
			s.process()
			if tt.annotate != "" {
				head := strings.TrimSpace(mustGit(t, dir, "rev-parse", "feature"))
				if _, err := postgres.NewCodeStore(s.pool).SetAnnotation(context.Background(), olivia, website, head,
					code.AnnotationInput{Name: "ci/build", Status: tt.annotate}); err != nil {
					t.Fatal(err)
				}
			}
			if got := branchStatus(t, s, "feature"); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("feature = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestSettingsFile(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		settings string
		want     string
	}{
		{name: "annotations that aren't a list", settings: "branches:\n  main:\n    requires:\n      annotations: ci/build\n",
			want: "/cobranch.yaml: line 4: annotations must be a list of names"},
		{name: "a misspelt requirement", settings: "branches:\n  main:\n    requires:\n      annotation: [ci/build]\n",
			want: `/cobranch.yaml: line 4: requires takes no "annotation"`},
		{name: "not YAML", settings: "branches: [\n", want: "/cobranch.yaml: line 1: did not find expected node content"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := newStack(t)
			dir := clone(t, s.websiteURL(s.base, olivia))
			commit(t, dir, "README.md", "# Website\n")
			mustGit(t, dir, "push", "-q", "origin", "HEAD:refs/heads/main")
			commit(t, dir, "cobranch.yaml", tt.settings)
			out, err := gitCmd(t, dir, "push", "origin", "HEAD:refs/heads/main")
			if err == nil || !strings.Contains(out, "remote: "+tt.want) {
				t.Fatalf("git push = %v:\n%s\nwant it refused with %q", err, out, tt.want)
			}
			// Elsewhere than main, the file is only a file.
			mustGit(t, dir, "push", "-q", "origin", "HEAD:refs/heads/feature")
		})
	}
}

func TestRequirementsFollowMain(t *testing.T) {
	t.Parallel()
	s := newStack(t)
	dir := clone(t, s.websiteURL(s.base, olivia))
	commit(t, dir, "README.md", "# Website\n")
	commit(t, dir, "cobranch.yaml", requireBuild+"  release:\n")
	mustGit(t, dir, "push", "-q", "origin", "HEAD:refs/heads/main")
	requirements := func() map[string]string {
		rows, err := s.pool.Query(context.Background(), "SELECT branch, array_to_string(annotations, ','), linear_history FROM merge_requirements WHERE repo_id = $1", website)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		out := map[string]string{}
		for rows.Next() {
			var branch, annotations string
			var linear bool
			if err := rows.Scan(&branch, &annotations, &linear); err != nil {
				t.Fatal(err)
			}
			out[branch] = annotations + map[bool]string{true: " linear", false: ""}[linear]
		}
		return out
	}
	if got, want := requirements(), map[string]string{"main": "ci/build linear", "release": ""}; !reflect.DeepEqual(got, want) {
		t.Errorf("requirements = %v, want %v", got, want)
	}
	// Pushed to another branch, a change to the file changes nothing; pushed to main, it replaces the requirements.
	commit(t, dir, "cobranch.yaml", "jobs: {}\n")
	mustGit(t, dir, "push", "-q", "origin", "HEAD:refs/heads/feature")
	if got := requirements(); len(got) != 2 {
		t.Errorf("after a push to feature, requirements = %v, want main's as they were", got)
	}
	// main requires ci/build, which feature's head lacks: the push takes repo.bypass.
	mustGit(t, dir, "push", "-q", s.url(s.base, s.token(olivia, "repo.read", "repo.push", "repo.bypass"), "acme/website.git"), "HEAD:refs/heads/main")
	if got := requirements(); len(got) != 0 {
		t.Errorf("after main dropped its branches, requirements = %v, want none", got)
	}
}

// raceCase pushes branch outer, a commit further, from a clone of main and of branches feature-a and feature-b, each a
// commit ahead of main, while another clone's push of branch other, a commit further too, records first: between the
// outer push's snapshot and its record. It expects the outer push refused with refusal, or accepted and each branch to
// be as far ahead as want says once the push worker processed both.
type raceCase struct {
	name    string
	outer   string
	other   string
	refusal string
	want    map[string]int
}

func TestRacingPushes(t *testing.T) {
	t.Parallel()
	const during = " moved while Cobranch took your push; nothing changed, so push again"
	tests := []raceCase{
		{name: "a branch another push moved keeps the facts that push counted", outer: "feature-a", other: "feature-b",
			want: map[string]int{"feature-a": 2, "feature-b": 2}},
		{name: "a main another push moved refuses the push", outer: "feature-a", other: "main", refusal: "refs/heads/main" + during},
		{name: "a branch another push moved refuses a push into main", outer: "main", other: "feature-b", refusal: "refs/heads/feature-b" + during},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s, dir := raceRepo(t, tt)
			out, err := gitCmd(t, dir, "push", "origin", tt.outer)
			if tt.refusal != "" {
				if err == nil || !strings.Contains(out, "remote: "+tt.refusal) {
					t.Fatalf("push = %v:\n%s\nwant it refused with %q", err, out, tt.refusal)
				}
				return
			}
			if err != nil {
				t.Fatalf("push: %v\n%s", err, out)
			}
			s.process()
			for name, ahead := range tt.want {
				if got := branchStatus(t, s, name).Ahead; got != ahead {
					t.Errorf("%s is %d ahead, want %d", name, got, ahead)
				}
			}
		})
	}
}

// raceRepo sets up tt's repository and the clone of it to push from, whose branch outer is a commit further, and has
// the stack record another clone's push of branch other before it records the first push it takes.
func raceRepo(t *testing.T, tt raceCase) (*stack, string) {
	t.Helper()
	s := newStack(t)
	dir := clone(t, s.websiteURL(s.base, olivia))
	mustGit(t, dir, "checkout", "-q", "-b", "main")
	commit(t, dir, "README.md", "# Website\n")
	for _, b := range []string{"feature-a", "feature-b"} {
		mustGit(t, dir, "checkout", "-q", "-b", b, "main")
		commit(t, dir, b+".txt", b+"\n")
	}
	mustGit(t, dir, "push", "-q", "origin", "main", "feature-a", "feature-b")
	s.process()
	other := clone(t, s.websiteURL(s.base, olivia))
	mustGit(t, other, "checkout", "-q", tt.other)
	commit(t, other, "other.txt", "from another clone\n")
	var raced atomic.Bool
	s.record = func(ctx context.Context, push git.Push, next git.Store) error {
		if !raced.Swap(true) {
			if out, err := gitCmd(t, other, "push", "origin", tt.other); err != nil {
				return fmt.Errorf("pushing %s first: %w\n%s", tt.other, err, out)
			}
		}
		return next.Record(ctx, push)
	}
	mustGit(t, dir, "checkout", "-q", tt.outer)
	commit(t, dir, "outer.txt", "from this clone\n")
	return s, dir
}
