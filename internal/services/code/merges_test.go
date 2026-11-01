package code_test

import (
	"reflect"
	"testing"

	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/git"
)

func TestViolations(t *testing.T) {
	head := "9f3c2e1aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	feature := git.Head{Branch: "feature/login", SHA: head}
	at := func(status code.BranchStatus, checks ...code.Check) map[string]code.Incoming {
		return map[string]code.Incoming{"feature/login": {Head: head, Base: "main", Status: status, Checks: checks}}
	}
	tests := []struct {
		name    string
		linear  bool
		brought *git.Brought
		live    map[string]code.Incoming
		want    []string
	}{
		{name: "a deletion", want: []string{"main requires repo.bypass to delete it"}},
		{name: "a Ready branch", brought: &git.Brought{Heads: []git.Head{feature}}, live: at(code.StatusReady)},
		{name: "nothing brought", brought: &git.Brought{}},
		{name: "commits no branch holds", brought: &git.Brought{Orphans: 3},
			want: []string{"main requires its commits to come from Ready branches: 3 commits come from none"}},
		{name: "one commit no branch holds", brought: &git.Brought{Orphans: 1},
			want: []string{"main requires its commits to come from Ready branches: 1 commit comes from none"}},
		{name: "a failed annotation", brought: &git.Brought{Heads: []git.Head{feature}},
			live: at(code.StatusBlocked, code.Check{Kind: code.CheckAnnotation, Name: "ci/build", Status: code.AnnotationSuccessful, Met: true},
				code.Check{Kind: code.CheckAnnotation, Name: "ci/test", Status: code.AnnotationFailed}),
			want: []string{"main requires feature/login to be Ready: ci/test failed on 9f3c2e1"}},
		{name: "a missing annotation", brought: &git.Brought{Heads: []git.Head{feature}},
			live: at(code.StatusBlocked, code.Check{Kind: code.CheckAnnotation, Name: "ci/build"}),
			want: []string{"main requires feature/login to be Ready: ci/build isn't on 9f3c2e1"}},
		{name: "merge commits", brought: &git.Brought{Heads: []git.Head{feature}},
			live: at(code.StatusBlocked, code.Check{Kind: code.CheckLinearHistory, Count: 2}),
			want: []string{"main requires feature/login to be Ready: 2 merge commits among its commits"}},
		{name: "behind its base", brought: &git.Brought{Heads: []git.Head{feature}},
			live: at(code.StatusBlocked, code.Check{Kind: code.CheckUpToDate, Count: 1}),
			want: []string{"main requires feature/login to be Ready: it is 1 commit behind main"}},
		{name: "Processing", brought: &git.Brought{Heads: []git.Head{feature}}, live: at(code.StatusProcessing),
			want: []string{"main requires feature/login to be Ready: it is Processing"}},
		{name: "never worked out", brought: &git.Brought{Heads: []git.Head{feature}}, live: at(""),
			want: []string{"main requires feature/login to be Ready: it is Processing"}},
		{name: "moved by the same push", brought: &git.Brought{Heads: []git.Head{{Branch: "feature/login", SHA: "b"}}}, live: at(code.StatusReady),
			want: []string{"main requires feature/login to be Ready: it moves in this push"}},
		{name: "created by the same push", brought: &git.Brought{Heads: []git.Head{feature}},
			want: []string{"main requires feature/login to be Ready: it moves in this push"}},
		{name: "a merge commit with changes of its own", brought: &git.Brought{Merges: 1, Unclean: []string{head}},
			want: []string{"main requires merge commits to merge cleanly: 9f3c2e1 holds changes of its own; make them in a Ready branch"}},
		{name: "several", brought: &git.Brought{Merges: 3, Unclean: []string{head, "b", "c"}},
			want: []string{"main requires merge commits to merge cleanly: 9f3c2e1 and 2 more hold changes of their own; make them in a Ready branch"}},
		{name: "a merge commit where linear history is required", linear: true, brought: &git.Brought{Merges: 1},
			want: []string{"main requires linear history: the push adds 1 merge commit"}},
		{name: "merge commits where it isn't", brought: &git.Brought{Merges: 2}},
		{name: "each break, commits first", brought: &git.Brought{Orphans: 2, Heads: []git.Head{feature}}, live: at(code.StatusProcessing),
			want: []string{"main requires its commits to come from Ready branches: 2 commits come from none",
				"main requires feature/login to be Ready: it is Processing"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := code.Violations("main", tt.linear, tt.brought, tt.live); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Violations() = %q, want %q", got, tt.want)
			}
		})
	}
}
