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

// Prepare readies the workspace for the task: Git's configuration in the home directory, which names the agent as the
// author of its commits and gives the Git server the task's token, read from its file each time; and, when the task
// names a repository and the workspace holds none, the repository, cloned at the task's branch, or its default one.
// It reports whether it cloned (harness.Checkout).
func (w *Workspace) Prepare(ctx context.Context, work agents.Work) (bool, error) {
	if err := w.configure(ctx, work); err != nil {
		return false, err
	}
	t := work.Task
	if t.RepoSlug == "" {
		return false, nil
	}
	if w.git.URL == "" {
		slog.WarnContext(ctx, "The installation gives its agents no Git server, so the task's repository isn't cloned", "repository", t.RepoSlug)
		return false, nil
	}
	if _, err := os.Stat(filepath.Join(w.dir, ".git")); err == nil {
		return false, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return false, fmt.Errorf("finding the workspace's clone: %w", err)
	}
	args := []string{"clone", "--quiet", "--no-tags"}
	if t.Branch != "" {
		args = append(args, "--branch", t.Branch)
	}
	args = append(args, "--", w.remote(work), w.dir)
	if err := w.run(ctx, args...); err != nil {
		return false, fmt.Errorf("cloning repository %s: %w", t.RepoSlug, err)
	}
	slog.InfoContext(ctx, "Cloned the task's repository", "repository", t.RepoSlug, "branch", t.Branch)
	return true, nil
}

// configure writes Git's configuration for the task: the agent as author and committer, the task's token as the Git
// server's credential, read from its file at each request so a renewed one is used, and the workspace, which the pod's
// volume makes and another user may own, as safe. New branches push to the remote's branch of their name.
func (w *Workspace) configure(ctx context.Context, work agents.Work) error {
	settings := [][2]string{
		{"user.name", work.Task.Agent.DisplayName},
		{"user.email", work.AgentEmail},
		{"safe.directory", "*"},
		{"push.autoSetupRemote", "true"},
	}
	if w.git.URL != "" && w.git.TokenFile != "" {
		helper := "!f() { test \"$1\" = get || exit 0; echo username=task; printf 'password=%s\\n' \"$(cat " + quote(w.git.TokenFile) + ")\"; }; f"
		settings = append(settings, [2]string{"credential." + strings.TrimSuffix(w.git.URL, "/") + "/.helper", helper})
	}
	for _, s := range settings {
		if err := w.run(ctx, "config", "--global", "--replace-all", s[0], s[1]); err != nil {
			return fmt.Errorf("setting Git's %s: %w", s[0], err)
		}
	}
	return nil
}

// remote is the HTTPS URL of the task's repository on the Git server.
func (w *Workspace) remote(work agents.Work) string {
	return strings.TrimSuffix(w.git.URL, "/") + "/" + work.OrgSlug + "/" + work.Task.RepoSlug + ".git"
}

// run runs git with args in the workspace, never prompting, and answers what it printed with its failure.
func (w *Workspace) run(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir, cmd.Env = w.dir, append(environment(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(string(out)))
	}
	return nil
}

// quote is s quoted for sh.
func quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
