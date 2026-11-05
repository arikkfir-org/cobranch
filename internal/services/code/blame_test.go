package code_test

import (
	"slices"
	"strings"
	"testing"

	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/code/codetest"
	"cobranch.dev/internal/services/repos"
)

func TestBlame(t *testing.T) {
	runs := []code.BlameRange{{SHA: sha(2), Start: 1, Lines: 2}, {SHA: sha(7), Start: 3, Lines: 1}, {SHA: sha(2), Start: 4, Lines: 1}}
	tests := []struct {
		name        string
		nia         bool
		ref, path   string
		fail        error
		runs        []code.BlameRange
		wantRuns    []code.BlameRange
		wantCommits []string
		wantErr     error
	}{
		{name: "the runs, and each commit they name once, the newest committed first", ref: "main", path: "README.md", runs: runs,
			wantRuns: runs, wantCommits: []string{"7", "2"}},
		{name: "a run whose commit no push recorded keeps its name", ref: "main", path: "README.md",
			runs:     []code.BlameRange{{SHA: sha(77), Start: 1, Lines: 1}, {SHA: sha(2), Start: 2, Lines: 3}},
			wantRuns: []code.BlameRange{{SHA: sha(77), Start: 1, Lines: 1}, {SHA: sha(2), Start: 2, Lines: 3}}, wantCommits: []string{"2"}},
		{name: "by a commit's full name", ref: sha(5), path: "README.md", wantRuns: []code.BlameRange{{SHA: sha(5), Start: 1, Lines: 4}},
			wantCommits: []string{"5"}},
		{name: "an empty file", ref: "main", path: "empty.txt", wantRuns: []code.BlameRange{}},
		{name: "a binary file", ref: "main", path: "logo.png", wantErr: invalid("path.text")},
		{name: "a file over the text the API shows", ref: "main", path: "big.txt", wantErr: invalid("path.text")},
		{name: "a symlink", ref: "main", path: "docs" + codetest.Symlink, wantErr: invalid("path.text")},
		{name: "the root", ref: "main", wantErr: code.ErrNotBlob},
		{name: "a directory", ref: "main", path: "docs", wantErr: code.ErrNotBlob},
		{name: "a ref git would read as a revision", ref: "main~2", path: "README.md", wantErr: invalid("ref.invalid")},
		{name: "a path that climbs out", ref: "main", path: "../README.md", wantErr: invalid("path.invalid")},
		{name: "the Git server's failure", ref: "main", path: "README.md", fail: code.ErrPathNotFound, wantErr: code.ErrPathNotFound},
		{name: "a viewer who doesn't read the repository", nia: true, ref: "main", path: "README.md", wantErr: repos.ErrNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := withLogs(newFixture())
			f.objects.Commit(sha(5), map[string]string{"README.md": "# Hi\n\nA\nB\n", "empty.txt": "", "logo.png": "\x89PNG\x00",
				"big.txt": strings.Repeat("x", code.MaxText+1), "docs/guide.md": "Guide\n", "docs" + codetest.Symlink: "docs/guide.md"})
			f.store.Add(f.repo, f.push, code.Commit{SHA: sha(7), Parents: []string{sha(5)}, Committer: code.Person{At: now}})
			if tt.runs != nil {
				f.objects.Blamed(sha(5), tt.path, tt.runs)
			}
			f.objects.Fail = tt.fail
			v := f.max
			if tt.nia {
				v = f.nia
			}
			got, err := f.service.Blame(t.Context(), v, f.repo, tt.ref, tt.path)
			if !sameError(err, tt.wantErr) {
				t.Fatalf("got error %v, want %v", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			if got.Commit != sha(5) || got.Path != tt.path || !slices.Equal(got.Ranges, tt.wantRuns) || !slices.Equal(shas(got.Commits), tt.wantCommits) {
				t.Errorf("got %+v with commits %v, want runs %+v and commits %v", got, shas(got.Commits), tt.wantRuns, tt.wantCommits)
			}
		})
	}
}

func TestPaths(t *testing.T) {
	tests := []struct {
		name       string
		nia        bool
		ref        string
		fail       error
		wantCommit string
		want       []string
		wantErr    error
	}{
		{name: "every file and symlink, without submodules", ref: "main", wantCommit: sha(5),
			want: []string{"README.md", "docs" + codetest.Symlink, "docs/guide.md"}},
		{name: "a commit without files", ref: "v1", wantCommit: sha(4), want: []string{}},
		{name: "a ref git would read as a revision", ref: "main~2", wantErr: invalid("ref.invalid")},
		{name: "a ref that names nothing", ref: "gone", wantErr: code.ErrRefNotFound},
		{name: "the Git server's failure", ref: "main", fail: code.ErrUnavailable, wantErr: code.ErrUnavailable},
		{name: "a viewer who doesn't read the repository", nia: true, ref: "main", wantErr: repos.ErrNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := withLogs(newFixture())
			f.objects.Commit(sha(5), map[string]string{"README.md": "# Hi\n", "docs/guide.md": "Guide\n", "docs" + codetest.Symlink: "docs/guide.md",
				"vendor" + codetest.Submodule: ""})
			f.objects.Fail = tt.fail
			v := f.max
			if tt.nia {
				v = f.nia
			}
			got, err := f.service.Paths(t.Context(), v, f.repo, tt.ref)
			if !sameError(err, tt.wantErr) {
				t.Fatalf("got error %v, want %v", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			if got.Commit != tt.wantCommit || got.Truncated || !slices.Equal(got.Paths, tt.want) {
				t.Errorf("got %+v, want commit %s and paths %q", got, tt.wantCommit, tt.want)
			}
		})
	}
}
