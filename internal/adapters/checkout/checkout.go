// Package checkout fetches a run's commit into its workspace: the init container that every run of a job that checks
// out runs first, with Cobranch's own image, over the Git server's HTTPS URL and the run's token
// (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/jobs.md, "Pods and scripts").
package checkout

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// Commit is what a checkout fetches: the commit SHA of the repository at URL, with token, into Dir.
type Commit struct {
	URL, Token, SHA, Dir string
}

// Run fetches the commit alone, shallow, into its directory, and checks it out detached, with git's progress and
// errors on stderr. The token travels in git's configuration from the environment, never on a command line, and the
// workspace, which the pod's volume makes and another user owns, is trusted.
func Run(ctx context.Context, c Commit, stderr io.Writer) error {
	format := "sha1"
	if len(c.SHA) == 64 {
		format = "sha256"
	}
	env := append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_COUNT=2",
		"GIT_CONFIG_KEY_0=http.extraHeader", "GIT_CONFIG_VALUE_0=Authorization: Bearer "+c.Token,
		"GIT_CONFIG_KEY_1=safe.directory", "GIT_CONFIG_VALUE_1=*")
	if os.Getenv("HOME") == "" || os.Getenv("HOME") == "/" {
		env = append(env, "HOME="+os.TempDir())
	}
	for _, args := range [][]string{
		{"init", "--quiet", "--object-format=" + format, c.Dir},
		{"-C", c.Dir, "fetch", "--quiet", "--no-tags", "--depth=1", c.URL, c.SHA},
		{"-C", c.Dir, "checkout", "--quiet", "--detach", "FETCH_HEAD"},
	} {
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Env, cmd.Stdout, cmd.Stderr = env, stderr, stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("git %s: %w", strings.Join(args[:min(len(args), 3)], " "), err)
		}
	}
	return nil
}
