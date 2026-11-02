package workspace

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"cobranch.dev/internal/services/agents"
	"cobranch.dev/internal/services/harness"
)

// Git is how the workspace's Git reaches Cobranch's Git server: its URL, and the file the task's token is in, which
// the controller renews in place.
type Git struct {
	URL, TokenFile string
}

var _ harness.Checkout = (*Workspace)(nil)

// WithGit has the workspace clone the task's repository from g, and its Git push there as the task's token, and
// answers it.
func (w *Workspace) WithGit(g Git) *Workspace {
	w.git = g
	return w
}

// Prepare readies the workspace for the task: Git's configuration, which names the agent as the author of its
// commits and gives the Git server the task's token, read from its file each time; and, when the task names a
// repository and the workspace holds none, the repository, cloned on the branch the task's work goes to. A task that
// names no branch gets one of its own, which a clone of an earlier pod of the task may have pushed already; one that
// works at a commit, such as a review, gets the clone at that commit, detached (harness.Checkout).
func (w *Workspace) Prepare(ctx context.Context, work agents.Work) (harness.Clone, error) {
	if err := w.configure(ctx, work); err != nil {
		return harness.Clone{}, err
	}
	t, branch := work.Task, work.Branch()
	if branch == "" {
		return harness.Clone{}, nil
	}
	if w.git.URL == "" {
		slog.WarnContext(ctx, "The installation gives its agents no Git server, so the task's repository isn't cloned", "repository", t.RepoSlug)
		return harness.Clone{}, nil
	}
	if _, err := os.Stat(filepath.Join(w.dir, ".git")); err == nil {
		return harness.Clone{Branch: branch, Commit: t.SHA}, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return harness.Clone{}, fmt.Errorf("finding the workspace's clone: %w", err)
	}
	args := []string{"clone", "--quiet", "--no-tags"}
	if t.Branch != "" {
		args = append(args, "--branch", t.Branch)
	}
	if _, err := w.run(ctx, append(args, "--", w.remote(work), w.dir)...); err != nil {
		return harness.Clone{}, fmt.Errorf("cloning repository %s: %w", t.RepoSlug, err)
	}
	var err error
	switch {
	case t.SHA != "":
		if _, err = w.run(ctx, "switch", "--quiet", "--detach", t.SHA); err != nil {
			err = fmt.Errorf("checking out commit %s: %w", t.SHA, err)
		}
	case t.Branch == "":
		err = w.own(ctx, branch)
	}
	if err != nil {
		return harness.Clone{}, err
	}
	slog.InfoContext(ctx, "Cloned the task's repository", "repository", t.RepoSlug, "branch", branch, "commit", t.SHA)
	return harness.Clone{Branch: branch, Commit: t.SHA, Fresh: true}, nil
}

// own puts the clone on branch, the task's own: the Git server's, when an earlier pod of the task pushed it, or a new
// one from the default branch's head.
func (w *Workspace) own(ctx context.Context, branch string) error {
	pushed, err := w.run(ctx, "for-each-ref", "--format=%(refname)", "refs/remotes/origin/"+branch)
	if err != nil {
		return fmt.Errorf("finding the task's branch %s: %w", branch, err)
	}
	args := []string{"switch", "--quiet", "--create", branch}
	if pushed != "" {
		args = []string{"switch", "--quiet", branch}
	}
	if _, err := w.run(ctx, args...); err != nil {
		return fmt.Errorf("switching to the task's branch %s: %w", branch, err)
	}
	return nil
}

// configure writes the workspace's Git configuration for the task: the agent as author and committer, the task's
// token as the Git server's credential, read from its file at each request so a renewed one is used, and the
// workspace, which the pod's volume makes and another user may own, as safe. New branches push to the remote's branch
// of their name.
func (w *Workspace) configure(ctx context.Context, work agents.Work) error {
	settings := [][2]string{
		{"user.name", work.Task.Agent.DisplayName},
		{"user.email", work.AgentEmail},
		{"safe.directory", w.dir},
		{"push.autoSetupRemote", "true"},
	}
	if w.git.URL != "" && w.git.TokenFile != "" {
		helper := "!f() { test \"$1\" = get || exit 0; echo username=task; printf 'password=%s\\n' \"$(cat " + quote(w.git.TokenFile) + ")\"; }; f"
		settings = append(settings, [2]string{"credential." + strings.TrimSuffix(w.git.URL, "/") + "/.helper", helper})
	}
	for _, s := range settings {
		if _, err := w.run(ctx, "config", "--file", w.gitConfig(), "--replace-all", s[0], s[1]); err != nil {
			return fmt.Errorf("setting Git's %s: %w", s[0], err)
		}
	}
	return nil
}

// remote is the HTTPS URL of the task's repository on the Git server.
func (w *Workspace) remote(work agents.Work) string {
	return strings.TrimSuffix(w.git.URL, "/") + "/" + work.OrgSlug + "/" + work.Task.RepoSlug + ".git"
}

// run runs git with args in the workspace, never prompting, and answers what it printed, trimmed, or that with its
// failure.
func (w *Workspace) run(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir, cmd.Env = w.dir, append(w.environment(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// quote is s quoted for sh.
func quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
