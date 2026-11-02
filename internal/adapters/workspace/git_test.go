package workspace

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/agents"
)

// gitIn runs git with args in dir, and answers what it printed.
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// server makes the Git server's repository acme/website under a directory, with branches main and tidy, and answers
// the directory's URL.
func server(t *testing.T) string {
	t.Helper()
	root, src := t.TempDir(), t.TempDir()
	gitIn(t, src, "init", "--quiet", "--initial-branch=main")
	gitIn(t, src, "-c", "user.name=Olivia", "-c", "user.email=olivia@example.com", "commit", "--quiet", "--allow-empty", "-m", "First")
	gitIn(t, src, "branch", "tidy")
	gitIn(t, src, "-c", "user.name=Olivia", "-c", "user.email=olivia@example.com", "commit", "--quiet", "--allow-empty", "-m", "Second")
	if err := os.MkdirAll(filepath.Join(root, "acme"), 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn(t, root, "clone", "--quiet", "--bare", src, filepath.Join(root, "acme", "website.git"))
	return "file://" + root
}

// work is a task of agent Scout's, on branch in repository repo of acme.
func work(repo, branch string) agents.Work {
	agent := uuid.MustParse("0190f1b2-0000-7000-8000-00000000000a")
	return agents.Work{OrgSlug: "acme", AgentEmail: agents.Email(agent),
		Task: agents.Task{Number: 7, Agent: agents.Principal{ID: agent, DisplayName: "Scout"}, RepoSlug: repo, Branch: branch}}
}

func TestPrepare(t *testing.T) {
	url := server(t)
	tt := []struct {
		name         string
		url, branch  string
		repo         string
		wantCloned   bool
		wantErr      string
		wantSubject  string
		wantBranches string
	}{
		{name: "the task's branch", url: url, repo: "website", branch: "tidy", wantCloned: true, wantSubject: "First", wantBranches: "tidy"},
		{name: "the default branch", url: url, repo: "website", wantCloned: true, wantSubject: "Second", wantBranches: "main"},
		{name: "a task without a repository", url: url},
		{name: "an installation without a Git server", repo: "website"},
		{name: "a branch the repository lacks", url: url, repo: "website", branch: "gone", wantErr: "cloning repository website"},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			w, err := New(t.TempDir(), time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = w.Close() }()
			w.WithGit(Git{URL: tc.url, TokenFile: "/run/token"})
			cloned, err := w.Prepare(context.Background(), work(tc.repo, tc.branch))
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("Prepare() = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil || cloned != tc.wantCloned {
				t.Fatalf("Prepare() = %v, %v, want %v", cloned, err, tc.wantCloned)
			}
			if gitIn(t, w.dir, "config", "--global", "user.email") != agents.Email(uuid.MustParse("0190f1b2-0000-7000-8000-00000000000a")) ||
				gitIn(t, w.dir, "config", "--global", "user.name") != "Scout" {
				t.Errorf("Git names %s", gitIn(t, w.dir, "config", "--global", "--list"))
			}
			if !tc.wantCloned {
				return
			}
			if got := gitIn(t, w.dir, "log", "-1", "--format=%s"); got != tc.wantSubject {
				t.Errorf("the clone is at %q, want %q", got, tc.wantSubject)
			}
			if got := gitIn(t, w.dir, "branch", "--format=%(refname:short)"); got != tc.wantBranches {
				t.Errorf("the clone's branches are %q, want %q", got, tc.wantBranches)
			}
			if again, err := w.Prepare(context.Background(), work(tc.repo, tc.branch)); err != nil || again {
				t.Errorf("Prepare() again = %v, %v, want no clone", again, err)
			}
		})
	}
}

// TestCredential checks that Git hands the Git server, and no other host, the token in the file, read anew each time.
func TestCredential(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	token := filepath.Join(t.TempDir(), "it's the token")
	if err := os.WriteFile(token, []byte("cba_first\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	w, err := New(t.TempDir(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()
	w.WithGit(Git{URL: "http://git.cobranch.test:8080/", TokenFile: token})
	if _, err := w.Prepare(context.Background(), work("", "")); err != nil {
		t.Fatal(err)
	}
	fill := func(url string) string {
		cmd := exec.Command("git", "credential", "fill")
		cmd.Dir, cmd.Stdin = w.dir, strings.NewReader("url="+url+"\n\n")
		cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=", "SSH_ASKPASS=")
		out, _ := cmd.CombinedOutput() // a host without a credential fails, which the answer shows
		return string(out)
	}
	if got := fill("http://git.cobranch.test:8080/acme/website.git"); !strings.Contains(got, "password=cba_first\n") {
		t.Errorf("the Git server gets %q", got)
	}
	if err := os.WriteFile(token, []byte("cba_second\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := fill("http://git.cobranch.test:8080/acme/website.git"); !strings.Contains(got, "password=cba_second\n") {
		t.Errorf("after a renewal, the Git server gets %q", got)
	}
	if got := fill("https://example.com/other.git"); strings.Contains(got, "cba_") {
		t.Errorf("another host gets %q", got)
	}
}
