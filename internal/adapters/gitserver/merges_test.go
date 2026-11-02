package gitserver_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"cobranch.dev/internal/adapters/postgres"
	"cobranch.dev/internal/services/code"
)

// requireAnnotation is a /cobranch.yaml whose main requires ci/build alone.
const requireAnnotation = "branches:\n  main:\n    requires:\n      annotations: [ci/build]\n"

// requireLinear is a /cobranch.yaml whose main requires ci/build and linear history.
const requireLinear = requireAnnotation + "      linearHistory: true\n"

// mergeRow is what a branch's row says of the last merge recorded of it.
type mergeRow struct {
	Status string
	Head   string
	By     string
}

// branchMerge reads branch name's status and merge in Acme's website, the merging principal by ID.
func branchMerge(t *testing.T, s *stack, name string) mergeRow {
	t.Helper()
	var row mergeRow
	var head, by *string
	if err := s.pool.QueryRow(context.Background(), "SELECT status, merged_head, merged_by::text FROM branches WHERE repo_id = $1 AND name = $2 AND deleted_at IS NULL",
		website, name).Scan(&row.Status, &head, &by); err != nil {
		t.Fatalf("reading branch %s: %v", name, err)
	}
	if head != nil {
		row.Head = *head
	}
	if by != nil {
		row.By = *by
	}
	return row
}

// mergeCase pushes main, perhaps with a /cobranch.yaml, and branch feature with a commit, perhaps annotated ci/build
// Successful once the push worker processed it; then pushes as push says, and expects the push refused with refusal,
// or accepted and feature's row to be want once processed.
type mergeCase struct {
	name     string
	settings string
	annotate bool
	push     func(t *testing.T, s *stack, dir string) (string, error)
	refusal  string
	want     func(feature string) mergeRow
}

// pushMain pushes local branch from to main with the clone's own token, which holds no repo.bypass.
func pushMain(from string) func(t *testing.T, s *stack, dir string) (string, error) {
	return func(t *testing.T, _ *stack, dir string) (string, error) {
		return gitCmd(t, dir, "push", "origin", from+":main")
	}
}

// merged is a row of feature merged at its head by Olivia.
func merged(feature string) mergeRow {
	return mergeRow{Status: "merged", Head: feature, By: olivia.String()}
}

// mergeCases are TestMergeByPush's cases.
func mergeCases() []mergeCase {
	return append([]mergeCase{
		{name: "nothing required: a push merges by fast-forward", push: pushMain("feature"), want: merged},
		{name: "a Ready branch merges by fast-forward", settings: requireAnnotation, annotate: true, push: pushMain("feature"), want: merged},
		{name: "a Ready branch merges by a merge commit", settings: requireAnnotation, annotate: true,
			push: func(t *testing.T, _ *stack, dir string) (string, error) {
				mustGit(t, dir, "checkout", "-q", "main")
				mustGit(t, dir, "merge", "-q", "--no-ff", "-m", "Merge feature", "feature")
				return gitCmd(t, dir, "push", "origin", "main")
			}, want: merged},
		{name: "a Blocked branch is refused", settings: requireAnnotation, push: pushMain("feature"),
			refusal: "main requires feature to be Ready: ci/build isn't on "},
		{name: "a merge commit with changes of its own is refused", settings: requireAnnotation, annotate: true,
			push: func(t *testing.T, _ *stack, dir string) (string, error) {
				mustGit(t, dir, "checkout", "-q", "main")
				mustGit(t, dir, "merge", "-q", "--no-ff", "--no-commit", "feature")
				if err := os.WriteFile(filepath.Join(dir, "evil.txt"), []byte("in no branch\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				mustGit(t, dir, "add", "evil.txt")
				mustGit(t, dir, "commit", "-q", "-m", "Merge feature")
				return gitCmd(t, dir, "push", "origin", "main")
			}, refusal: "main requires merge commits to merge cleanly: "},
		{name: "linear history refuses a merge commit", settings: requireLinear, annotate: true,
			push: func(t *testing.T, _ *stack, dir string) (string, error) {
				mustGit(t, dir, "checkout", "-q", "main")
				mustGit(t, dir, "merge", "-q", "--no-ff", "-m", "Merge feature", "feature")
				return gitCmd(t, dir, "push", "origin", "main")
			}, refusal: "main requires linear history: the push adds 1 merge commit"},
		{name: "linear history takes a fast-forward", settings: requireLinear, annotate: true, push: pushMain("feature"), want: merged},
		{name: "a commit no branch holds is refused", settings: requireAnnotation, annotate: true,
			push: func(t *testing.T, _ *stack, dir string) (string, error) {
				mustGit(t, dir, "checkout", "-q", "main")
				commit(t, dir, "direct.txt", "straight into main\n")
				return gitCmd(t, dir, "push", "origin", "main")
			}, refusal: "main requires its commits to come from Ready branches: 1 commit comes from none"},
		{name: "deleting main is refused", settings: requireAnnotation,
			push: func(t *testing.T, _ *stack, dir string) (string, error) {
				return gitCmd(t, dir, "push", "origin", ":main")
			},
			refusal: "main requires repo.bypass to delete it"},
		{name: "repo.bypass merges a Blocked branch", settings: requireAnnotation,
			push: func(t *testing.T, s *stack, dir string) (string, error) {
				return gitCmd(t, dir, "push", s.url(s.base, s.token(olivia, "repo.read", "repo.push", "repo.bypass"), "acme/website.git"), "feature:main")
			}, want: merged},
		{name: "a branch moved back to main's tip is Empty, not Merged",
			push: func(t *testing.T, _ *stack, dir string) (string, error) {
				return gitCmd(t, dir, "push", "origin", "main:feature", "--force")
			},
			want: func(string) mergeRow { return mergeRow{Status: "empty"} }},
	}, otherBranchCases()...)
}

// otherBranchCases are TestMergeByPush's cases of pushes into a branch with merge requirements besides main.
func otherBranchCases() []mergeCase {
	return []mergeCase{
		{name: "main's history moves another branch with merge requirements on", settings: requireAnnotation + "  release:\n    requires:\n      annotations: [ci/build]\n",
			annotate: true, push: func(t *testing.T, s *stack, dir string) (string, error) {
				mustGit(t, dir, "push", "-q", "origin", "main:release")
				mustGit(t, dir, "push", "-q", "origin", "feature:main")
				s.process()
				return gitCmd(t, dir, "push", "origin", "feature:release")
			}, want: merged},
		{name: "linear history refuses a Ready branch's merge commits into another branch", settings: "branches:\n  release:\n    requires:\n      linearHistory: true\n",
			push: func(t *testing.T, s *stack, dir string) (string, error) {
				mustGit(t, dir, "push", "-q", "origin", "main:release")
				mustGit(t, dir, "checkout", "-q", "-b", "side", "main")
				commit(t, dir, "side.txt", "beside the feature\n")
				mustGit(t, dir, "checkout", "-q", "feature")
				mustGit(t, dir, "merge", "-q", "--no-ff", "-m", "Merge side", "side")
				mustGit(t, dir, "push", "-q", "origin", "feature")
				s.process()
				return gitCmd(t, dir, "push", "origin", "feature:release")
			}, refusal: "release requires linear history: the push adds 1 merge commit"},
	}
}

func TestMergeByPush(t *testing.T) {
	t.Parallel()
	for _, tt := range mergeCases() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s, dir, feature := branchToMerge(t, tt)
			out, err := tt.push(t, s, dir)
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
			if got, want := branchMerge(t, s, "feature"), tt.want(feature); !reflect.DeepEqual(got, want) {
				t.Errorf("feature = %+v, want %+v", got, want)
			}
		})
	}
}

// TestMergeOfADeletedBranch checks that a merge into main keeps its record on its branch once the branch is deleted
// before the push worker records the merge, as a deletion pushed right after the merge leaves it.
func TestMergeOfADeletedBranch(t *testing.T) {
	tests := []struct {
		name   string
		pushes [][]string
	}{
		{name: "deleted right after a fast-forward", pushes: [][]string{{"feature:main"}, {":feature"}}},
		{name: "deleted right after a merge commit", pushes: [][]string{{"merged:main"}, {":feature"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, dir, feature := branchToMerge(t, mergeCase{})
			mustGit(t, dir, "checkout", "-q", "-b", "merged", "main")
			mustGit(t, dir, "merge", "-q", "--no-ff", "-m", "Merge feature", "feature")
			for _, refs := range tt.pushes {
				mustGit(t, dir, append([]string{"push", "-q", "origin"}, refs...)...)
			}
			s.process()
			var head, by *string
			if err := s.pool.QueryRow(t.Context(), "SELECT merged_head, merged_by::text FROM branches WHERE repo_id = $1 AND name = 'feature' AND deleted_at IS NOT NULL",
				website).Scan(&head, &by); err != nil {
				t.Fatalf("reading the deleted branch: %v", err)
			}
			if head == nil || *head != feature || by == nil || *by != olivia.String() {
				t.Errorf("feature's merge = %v by %v, want %s by Olivia", head, by, feature)
			}
		})
	}
}

// branchToMerge sets up tt's repository: main, with tt's /cobranch.yaml if any, and branch feature a commit ahead of
// it, processed and, if tt says so, annotated; it returns the stack, the clone and feature's head.
func branchToMerge(t *testing.T, tt mergeCase) (*stack, string, string) {
	t.Helper()
	s := newStack(t)
	dir := clone(t, s.websiteURL(s.base, olivia))
	mustGit(t, dir, "checkout", "-q", "-b", "main")
	commit(t, dir, "README.md", "# Website\n")
	if tt.settings != "" {
		commit(t, dir, "cobranch.yaml", tt.settings)
	}
	mustGit(t, dir, "push", "-q", "origin", "main")
	mustGit(t, dir, "checkout", "-q", "-b", "feature")
	feature := commit(t, dir, "feature.txt", "a feature\n")
	mustGit(t, dir, "push", "-q", "origin", "feature")
	s.process()
	if tt.annotate {
		annotate(t, s, feature, code.AnnotationSuccessful)
	}
	return s, dir, feature
}

// annotate has Olivia set ci/build on commit sha.
func annotate(t *testing.T, s *stack, sha string, status code.AnnotationStatus) {
	t.Helper()
	if _, err := postgres.NewCodeStore(s.pool).SetAnnotation(context.Background(), olivia, website, sha,
		code.AnnotationInput{Name: "ci/build", Status: status}); err != nil {
		t.Fatal(err)
	}
}

func TestBypassEvent(t *testing.T) {
	t.Parallel()
	s := newStack(t)
	dir := clone(t, s.websiteURL(s.base, olivia))
	commit(t, dir, "README.md", "# Website\n")
	commit(t, dir, "cobranch.yaml", requireAnnotation)
	mustGit(t, dir, "push", "-q", "origin", "HEAD:refs/heads/main")
	commit(t, dir, "direct.txt", "straight into main\n")
	mustGit(t, dir, "push", "-q", s.url(s.base, s.token(olivia, "repo.read", "repo.push", "repo.bypass"), "acme/website.git"), "HEAD:refs/heads/main")
	var details []byte
	if err := s.pool.QueryRow(context.Background(), "SELECT details FROM events WHERE kind = 'branch.bypassed' AND subject_id = $1", website).Scan(&details); err != nil {
		t.Fatalf("reading event branch.bypassed: %v", err)
	}
	var got struct {
		Branch string   `json:"branch"`
		Broken []string `json:"broken"`
	}
	if err := json.Unmarshal(details, &got); err != nil {
		t.Fatal(err)
	}
	want := []string{"main requires its commits to come from Ready branches: 1 commit comes from none"}
	if got.Branch != "main" || !reflect.DeepEqual(got.Broken, want) {
		t.Errorf("event branch.bypassed = %+v, want branch main, broken %q", got, want)
	}
}

func TestMergedBranchMovesOn(t *testing.T) {
	t.Parallel()
	s := newStack(t)
	dir := clone(t, s.websiteURL(s.base, olivia))
	mustGit(t, dir, "checkout", "-q", "-b", "main")
	commit(t, dir, "README.md", "# Website\n")
	mustGit(t, dir, "push", "-q", "origin", "main")
	mustGit(t, dir, "checkout", "-q", "-b", "feature")
	first := commit(t, dir, "feature.txt", "a feature\n")
	mustGit(t, dir, "push", "-q", "origin", "feature", "feature:main")
	s.process()
	if got := branchMerge(t, s, "feature"); got.Status != "merged" {
		t.Fatalf("feature = %+v, want merged", got)
	}
	// A new commit makes it a request again; its merge stays recorded at the head merged.
	commit(t, dir, "more.txt", "more of it\n")
	mustGit(t, dir, "push", "-q", "origin", "feature")
	s.process()
	if got, want := branchMerge(t, s, "feature"), (mergeRow{Status: "ready", Head: first, By: olivia.String()}); got != want {
		t.Errorf("feature = %+v, want %+v", got, want)
	}
}
