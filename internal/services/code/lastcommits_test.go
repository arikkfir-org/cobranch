package code_test

import (
	"maps"
	"slices"
	"testing"

	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/repos"
)

func TestTreeCommits(t *testing.T) {
	tests := []struct {
		name        string
		nia         bool
		ref, path   string
		fail        error
		last        code.LastCommits
		wantLatest  string
		wantEntries map[string]string
		wantCommits []string
		wantErr     error
	}{
		{name: "each entry's latest commit, each commit once, the newest committed first", ref: "main",
			last:       code.LastCommits{Count: 6, Latest: sha(5), Entries: map[string]string{"README.md": sha(5), "docs": sha(7), "go.mod": sha(5)}},
			wantLatest: sha(5), wantEntries: map[string]string{"README.md": sha(5), "docs": sha(7), "go.mod": sha(5)}, wantCommits: []string{"7", "5"}},
		{name: "without what no push recorded", ref: "main", path: "docs",
			last:        code.LastCommits{Count: 3, Latest: sha(77), Entries: map[string]string{"guide.md": sha(6), "new.md": sha(77)}},
			wantEntries: map[string]string{"guide.md": sha(6)}, wantCommits: []string{"6"}},
		{name: "a directory nothing changed in the walk", ref: "main", path: "old", last: code.LastCommits{Count: 0, Entries: map[string]string{}},
			wantEntries: map[string]string{}},
		{name: "a ref git would read as a revision", ref: "main~2", wantErr: invalid("ref.invalid")},
		{name: "a path that climbs out", ref: "main", path: "..", wantErr: invalid("path.invalid")},
		{name: "a file's path", ref: "main", path: "README.md", wantErr: code.ErrNotTree},
		{name: "the Git server's failure", ref: "main", fail: code.ErrPathNotFound, wantErr: code.ErrPathNotFound},
		{name: "a viewer who doesn't read the repository", nia: true, ref: "main", wantErr: repos.ErrNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := withLogs(newFixture())
			f.objects.Commit(sha(5), map[string]string{"README.md": "# Hi\n", "go.mod": "module x\n", "docs/guide.md": "Guide\n", "old/a.txt": "a\n"})
			f.store.Add(f.repo, f.push, code.Commit{SHA: sha(7), Parents: []string{sha(5)}, Committer: code.Person{At: now}})
			tt.last.Commit, tt.last.Path = sha(5), tt.path
			f.objects.LastChanged(tt.last)
			f.objects.Fail = tt.fail
			v := f.max
			if tt.nia {
				v = f.nia
			}
			got, err := f.service.TreeCommits(t.Context(), v, f.repo, tt.ref, tt.path)
			if !sameError(err, tt.wantErr) {
				t.Fatalf("got error %v, want %v", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			if got.Commit != sha(5) || got.Path != tt.path || got.Count != tt.last.Count || got.Latest != tt.wantLatest ||
				!maps.Equal(got.Entries, tt.wantEntries) || !slices.Equal(shas(got.Commits), tt.wantCommits) {
				t.Errorf("got %+v with commits %v, want latest %q, entries %v, commits %v", got, shas(got.Commits), tt.wantLatest, tt.wantEntries, tt.wantCommits)
			}
		})
	}
}
