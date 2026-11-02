package code_test

import (
	"testing"

	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/orgs"
	"cobranch.dev/internal/services/repos"
)

// withDiffs gives the fixture's commits 1, 4, 5 and 6 files, and feature/x main as its base.
func withDiffs(f *fixture) *fixture {
	f.objects.Commit(sha(1), map[string]string{"README.md": "# Hello\n"})
	f.objects.Commit(sha(4), map[string]string{"README.md": "# Hello\n", "a.txt": "a\n"})
	f.objects.Commit(sha(5), map[string]string{"README.md": "# Hello\n", "a.txt": "a\nb\n"}, "main")
	f.objects.Commit(sha(6), map[string]string{"README.md": "# Hi\n"})
	f.store.UpdateBranch(f.repo, "feature/x", func(b *code.Branch) { b.Base = "main" })
	return f
}

func TestBranchDiff(t *testing.T) {
	invalid := func(code string) error { return &orgs.ValidationError{Code: code} }
	tests := []struct {
		name    string
		viewer  func(*fixture) code.Viewer
		branch  string
		fail    error
		want    code.DiffRequest
		wantErr error
	}{
		{name: "from its merge base with its base's tip to its head", viewer: func(f *fixture) code.Viewer { return f.max }, branch: "feature/x",
			want: code.DiffRequest{Base: "main", Head: sha(6), MergeBase: true}},
		{name: "main merges into nothing", viewer: func(f *fixture) code.Viewer { return f.max }, branch: "main", wantErr: invalid("branch.default")},
		{name: "a branch that doesn't live", viewer: func(f *fixture) code.Viewer { return f.max }, branch: "gone", wantErr: repos.ErrNotFound},
		{name: "a viewer who doesn't read the repository finds none", viewer: func(f *fixture) code.Viewer { return f.nia }, branch: "feature/x",
			wantErr: repos.ErrNotFound},
		{name: "without a Git server", viewer: func(f *fixture) code.Viewer { return f.max }, branch: "feature/x", fail: code.ErrUnavailable,
			wantErr: code.ErrUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := withDiffs(newFixture())
			f.objects.Fail = tt.fail
			got, err := f.service.BranchDiff(t.Context(), tt.viewer(f), f.repo, tt.branch)
			if !sameError(err, tt.wantErr) {
				t.Fatalf("got error %v, want %v", err, tt.wantErr)
			}
			if err != nil {
				if len(f.objects.Diffs) > 0 && tt.fail == nil {
					t.Errorf("asked Git for %v, want nothing asked", f.objects.Diffs)
				}
				return
			}
			if len(f.objects.Diffs) != 1 || f.objects.Diffs[0] != tt.want {
				t.Errorf("asked Git for %v, want %v", f.objects.Diffs, tt.want)
			}
			if got.Base != sha(5) || got.BaseTip != sha(5) || got.Head != sha(6) || len(got.Files) != 2 {
				t.Errorf("got %+v, want the change from 5 to 6", got)
			}
		})
	}
}

func TestCommitDiff(t *testing.T) {
	invalid := func(code string) error { return &orgs.ValidationError{Code: code} }
	tests := []struct {
		name    string
		viewer  func(*fixture) code.Viewer
		sha     string
		want    code.DiffRequest
		wantErr error
	}{
		{name: "against its first parent", viewer: func(f *fixture) code.Viewer { return f.max }, sha: sha(5),
			want: code.DiffRequest{Base: sha(4), Head: sha(5)}},
		{name: "a root commit, against the empty tree", viewer: func(f *fixture) code.Viewer { return f.max }, sha: sha(1),
			want: code.DiffRequest{Head: sha(1)}},
		{name: "a commit the repository doesn't hold", viewer: func(f *fixture) code.Viewer { return f.max }, sha: sha(99), wantErr: repos.ErrNotFound},
		{name: "an abbreviated name", viewer: func(f *fixture) code.Viewer { return f.max }, sha: "abc", wantErr: invalid("sha.invalid")},
		{name: "a viewer who doesn't read the repository finds none", viewer: func(f *fixture) code.Viewer { return f.nia }, sha: sha(5),
			wantErr: repos.ErrNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := withDiffs(newFixture())
			_, err := f.service.CommitDiff(t.Context(), tt.viewer(f), f.repo, tt.sha)
			if !sameError(err, tt.wantErr) {
				t.Fatalf("got error %v, want %v", err, tt.wantErr)
			}
			var want []code.DiffRequest
			if err == nil {
				want = []code.DiffRequest{tt.want}
			}
			if len(f.objects.Diffs) != len(want) || (len(want) == 1 && f.objects.Diffs[0] != want[0]) {
				t.Errorf("asked Git for %v, want %v", f.objects.Diffs, want)
			}
		})
	}
}

func TestCommitDiffChanges(t *testing.T) {
	f := withDiffs(newFixture())
	got, err := f.service.CommitDiff(t.Context(), f.max, f.repo, sha(5))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Files) != 1 || got.Files[0].Path != "a.txt" || got.Additions != 2 || got.Deletions != 1 {
		t.Errorf("got %+v, want a.txt's change", got)
	}
}
