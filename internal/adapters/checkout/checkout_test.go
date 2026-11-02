package checkout_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"cobranch.dev/internal/adapters/checkout"
)

// git runs git in dir and answers its output.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Olivia", "GIT_AUTHOR_EMAIL=olivia@example.com",
		"GIT_COMMITTER_NAME=Olivia", "GIT_COMMITTER_EMAIL=olivia@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestRun(t *testing.T) {
	for _, format := range []string{"sha1", "sha256"} {
		t.Run(format, func(t *testing.T) {
			origin := t.TempDir()
			git(t, origin, "init", "--quiet", "--object-format="+format)
			if err := os.WriteFile(filepath.Join(origin, "Makefile"), []byte("all:\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			git(t, origin, "add", ".")
			git(t, origin, "commit", "--quiet", "-m", "First")
			first := git(t, origin, "rev-parse", "HEAD")
			git(t, origin, "commit", "--quiet", "--allow-empty", "-m", "Second")
			workspace := filepath.Join(t.TempDir(), "workspace")
			var stderr bytes.Buffer
			err := checkout.Run(context.Background(), checkout.Commit{URL: "file://" + origin, Token: "cbr_t", SHA: first, Dir: workspace}, &stderr)
			if err != nil {
				t.Fatalf("Run() = %v: %s", err, stderr.String())
			}
			if head := git(t, workspace, "rev-parse", "HEAD"); head != first {
				t.Errorf("HEAD = %s, want the commit asked for, %s", head, first)
			}
			if _, err := os.Stat(filepath.Join(workspace, "Makefile")); err != nil {
				t.Errorf("the commit's files aren't checked out: %v", err)
			}
		})
	}
}

func TestRunNamesWhatFailed(t *testing.T) {
	err := checkout.Run(context.Background(), checkout.Commit{URL: "file://" + t.TempDir(), SHA: strings.Repeat("a", 40),
		Dir: filepath.Join(t.TempDir(), "w")}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "git -C") || !strings.Contains(err.Error(), "fetch") {
		t.Errorf("Run() of a commit that isn't there = %v, want the fetch named", err)
	}
}
