package gitserver_test

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"cobranch.dev/internal/services/git"
)

// reviewSettings reads the website's AI review settings as the push that last moved main stored them.
func reviewSettings(t *testing.T, s *stack) git.ReviewSettings {
	t.Helper()
	var r git.ReviewSettings
	var lines int32
	if err := s.pool.QueryRow(context.Background(),
		"SELECT enabled, branches, ignore, instructions, include, max_diff_lines FROM review_settings WHERE repo_id = $1", website).
		Scan(&r.Enabled, &r.Branches, &r.Ignore, &r.Instructions, &r.Include, &lines); err != nil {
		t.Fatalf("reading the review settings: %v", err)
	}
	r.MaxDiffLines = int(lines)
	return r
}

func TestReviewSettingsFile(t *testing.T) {
	t.Parallel()
	example := git.ReviewSettings{Enabled: true, Branches: []string{"feature/*"}, Ignore: []string{"vendor/**"},
		Instructions: "Flag every TODO.\n", Include: []string{"CONTRIBUTING.md"}, MaxDiffLines: 800}
	tests := []reviewSettingsPush{
		{name: "the settings of the file", settings: "branches: [feature/*]\npaths:\n  ignore: [vendor/**]\ninstructions: |\n  Flag every TODO.\n" +
			"include: [CONTRIBUTING.md]\nmaxDiffLines: 800\n", want: example},
		{name: "an empty file is the defaults", settings: "", want: git.DefaultReviewSettings()},
		{name: "off", settings: "enabled: false\n", want: func() git.ReviewSettings { d := git.DefaultReviewSettings(); d.Enabled = false; return d }()},
		{name: "a misspelt key", settings: "maxDiffLine: 800\n", refusal: `/cobranch-review.yaml: line 1: the settings takes no "maxDiffLine"`},
		{name: "not YAML", settings: "branches: [\n", refusal: "/cobranch-review.yaml: line 1: did not find expected node content"},
		{name: "a directory", dir: true, refusal: "/cobranch-review.yaml: it must be a file"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tt.check(t, newStack(t))
		})
	}
}

// reviewSettingsPush is a push into main that brings a /cobranch-review.yaml, and the settings it stores or why it is
// refused.
type reviewSettingsPush struct {
	name     string
	settings string
	// dir, when set, makes /cobranch-review.yaml a directory.
	dir     bool
	want    git.ReviewSettings
	refusal string
}

// check pushes main without the file, then with it, and checks the settings each push leaves.
func (tt reviewSettingsPush) check(t *testing.T, s *stack) {
	t.Helper()
	dir := clone(t, s.websiteURL(s.base, olivia))
	commit(t, dir, "README.md", "# Website\n")
	mustGit(t, dir, "push", "-q", "origin", "HEAD:refs/heads/main")
	if got := reviewSettings(t, s); !reflect.DeepEqual(got, git.DefaultReviewSettings()) {
		t.Fatalf("without the file, the settings = %+v, want the defaults", got)
	}
	if tt.dir {
		if err := os.Mkdir(filepath.Join(dir, "cobranch-review.yaml"), 0o700); err != nil {
			t.Fatal(err)
		}
		commit(t, dir, "cobranch-review.yaml/README.md", "not settings\n")
	} else {
		commit(t, dir, "cobranch-review.yaml", tt.settings)
	}
	out, err := gitCmd(t, dir, "push", "origin", "HEAD:refs/heads/main")
	if tt.refusal == "" {
		if err != nil {
			t.Fatalf("git push = %v:\n%s", err, out)
		}
		if got := reviewSettings(t, s); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("the settings = %+v, want %+v", got, tt.want)
		}
		return
	}
	if err == nil || !strings.Contains(out, "remote: "+tt.refusal) {
		t.Fatalf("git push = %v:\n%s\nwant it refused with %q", err, out, tt.refusal)
	}
	if got := reviewSettings(t, s); !reflect.DeepEqual(got, git.DefaultReviewSettings()) {
		t.Errorf("after the refusal, the settings = %+v, want them kept", got)
	}
	// Elsewhere than main, the file is only a file.
	mustGit(t, dir, "push", "-q", "origin", "HEAD:refs/heads/feature")
}
