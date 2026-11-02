package workspace

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/agents"
	"cobranch.dev/internal/services/harness"
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

// server makes the Git server's repository acme/website under a directory, with branches main, and tidy and tasks/7
// a commit behind, and answers the directory's URL.
func server(t *testing.T) string {
	t.Helper()
	root, src := t.TempDir(), t.TempDir()
	gitIn(t, src, "init", "--quiet", "--initial-branch=main")
	gitIn(t, src, "-c", "user.name=Olivia", "-c", "user.email=olivia@example.com", "commit", "--quiet", "--allow-empty", "-m", "First")
	gitIn(t, src, "branch", "tidy")
	gitIn(t, src, "branch", "tasks/7")
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

// prepareCase is a task's workspace to prepare, from a Git server's URL, and what it should hold.
type prepareCase struct {
	name        string
	url, branch string
	repo        string
	issue       bool
	// at is the commit the task works at, by its subject on the server; empty for none.
	at           string
	wantClone    harness.Clone
	wantErr      string
	wantSubject  string
	wantBranches string
}

// work is tc's task, which works on issue ACME-42 if tc says so.
func (tc prepareCase) work() agents.Work {
	w := work(tc.repo, tc.branch)
	if tc.issue {
		w.IssueKey, w.IssueNumber = "ACME", 42
	}
	return w
}

func TestPrepare(t *testing.T) {
	url := server(t)
	first := gitIn(t, strings.TrimPrefix(url, "file://")+"/acme/website.git", "rev-parse", "main~1")
	tt := []prepareCase{
		{name: "the task's branch", url: url, repo: "website", branch: "tidy", wantClone: harness.Clone{Branch: "tidy", Fresh: true},
			wantSubject: "First", wantBranches: "tidy"},
		{name: "an issue's task's own branch, new from the default branch", url: url, repo: "website", issue: true,
			wantClone: harness.Clone{Branch: "acme-42-task-7", Fresh: true}, wantSubject: "Second", wantBranches: "acme-42-task-7\nmain"},
		{name: "a task's own branch, which an earlier pod pushed", url: url, repo: "website",
			wantClone: harness.Clone{Branch: "tasks/7", Fresh: true}, wantSubject: "First", wantBranches: "main\ntasks/7"},
		{name: "a task at a commit, behind its branch's head", url: url, repo: "website", branch: "main", at: first,
			wantClone: harness.Clone{Branch: "main", Commit: first, Fresh: true}, wantSubject: "First", wantBranches: "(HEAD detached at " + first[:7] + ")\nmain"},
		{name: "a task without a repository", url: url},
		{name: "an installation without a Git server", repo: "website"},
		{name: "a branch the repository lacks", url: url, repo: "website", branch: "gone", wantErr: "cloning repository website"},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			w, err := New(t.TempDir(), time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = w.Close() }()
			w.WithGit(Git{URL: tc.url, TokenFile: "/run/token"})
			work := tc.work()
			work.Task.SHA = tc.at
			clone, err := w.Prepare(context.Background(), work)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("Prepare() = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil || clone != tc.wantClone {
				t.Fatalf("Prepare() = %+v, %v, want %+v", clone, err, tc.wantClone)
			}
			if _, err := os.Stat(filepath.Join(home, ".gitconfig")); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("the user's own Git configuration: %v, want none written", err)
			}
			checkPrepared(t, w, tc)
		})
	}
}

// checkPrepared checks that w's Git names the agent, and that its clone, if tc wants one, is at tc's commit, holds tc's
// branches, and isn't cloned again.
func checkPrepared(t *testing.T, w *Workspace, tc prepareCase) {
	t.Helper()
	if gitIn(t, w.dir, "config", "--file", w.gitConfig(), "user.email") != agents.Email(uuid.MustParse("0190f1b2-0000-7000-8000-00000000000a")) ||
		gitIn(t, w.dir, "config", "--file", w.gitConfig(), "user.name") != "Scout" {
		t.Errorf("Git names %s", gitIn(t, w.dir, "config", "--file", w.gitConfig(), "--list"))
	}
	if got, err := w.shell(context.Background(), shellArgs{Command: "git config --global user.name"}); err != nil || strings.TrimSpace(got) != "Scout" {
		t.Errorf("the model's git names %q, %v", got, err)
	}
	if !tc.wantClone.Fresh {
		return
	}
	if got := gitIn(t, w.dir, "log", "-1", "--format=%s"); got != tc.wantSubject {
		t.Errorf("the clone is at %q, want %q", got, tc.wantSubject)
	}
	if got := gitIn(t, w.dir, "branch", "--format=%(refname:short)"); got != tc.wantBranches {
		t.Errorf("the clone's branches are %q, want %q", got, tc.wantBranches)
	}
	again := tc.work()
	again.Task.SHA = tc.at
	if got, err := w.Prepare(context.Background(), again); err != nil || got != (harness.Clone{Branch: tc.wantClone.Branch, Commit: tc.at}) {
		t.Errorf("Prepare() again = %+v, %v, want the clone it holds", got, err)
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
		cmd.Env = append(w.environment(), "GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=", "SSH_ASKPASS=")
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
