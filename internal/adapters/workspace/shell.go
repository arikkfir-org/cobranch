package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"cobranch.dev/internal/services/harness"
)

// Limits of a shell command's output, which keeps its start and its end, where errors show.
const (
	keptHead = 16 << 10
	keptTail = harness.MaxOutput - keptHead - 256
	// waitDelay is how long a command's output may stay open after it is killed.
	waitDelay = 5 * time.Second
)

// shellSpec describes the shell, whose commands run for at most timeout.
func shellSpec(timeout time.Duration) harness.Spec {
	return spec("shell", "Runs a command with sh in the workspace, and answers what it printed, its output and its errors "+
		"together, and its exit status; a long output keeps its start and its end. Each command runs anew, for at most "+
		strconv.Itoa(int(timeout.Seconds()))+" seconds; Git works here.",
		`"command":{"type":"string","description":"The command, as sh reads it"},`+
			`"timeout":{"type":"integer","description":"How many seconds it may run, up to the most"}`, "command")
}

type shellArgs struct {
	Command string `json:"command"`
	Timeout int    `json:"timeout"`
}

// shell runs a command in the workspace, in a process group of its own that ends with it, without Cobranch's own
// variables, which hold where the task's token and the provider's key are.
func (w *Workspace) shell(ctx context.Context, a shellArgs) (string, error) {
	timeout := w.shellTimeout
	if a.Timeout > 0 {
		timeout = min(time.Duration(a.Timeout)*time.Second, timeout)
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "-c", a.Command)
	cmd.Dir, cmd.Env = w.dir, w.environment()
	out := &headTail{}
	cmd.Stdout, cmd.Stderr = out, out
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = waitDelay
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case ctx.Err() == context.DeadlineExceeded:
		return out.String(), fmt.Errorf("the command ran out of its %s", timeout)
	case errors.As(err, &exit):
		return out.String(), fmt.Errorf("the command exited with status %d", exit.ExitCode())
	case err != nil:
		return out.String(), fmt.Errorf("running the command: %w", err)
	}
	if out.total == 0 {
		return "(no output)", nil
	}
	return out.String(), nil
}

// environment is the harness's environment without Cobranch's own variables, with Git reading the workspace's
// configuration as the user's.
func (w *Workspace) environment() []string {
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "COBRANCH_") && !strings.HasPrefix(kv, "GIT_CONFIG_GLOBAL=") {
			env = append(env, kv)
		}
	}
	return append(env, "GIT_CONFIG_GLOBAL="+w.gitConfig())
}

// headTail keeps the start and the end of what is written to it.
type headTail struct {
	head, tail []byte
	total      int
}

// Write keeps p's bytes while the head has room, and the last keptTail bytes after (io.Writer).
func (h *headTail) Write(p []byte) (int, error) {
	h.total += len(p)
	rest := p
	if room := keptHead - len(h.head); room > 0 {
		n := min(room, len(rest))
		h.head, rest = append(h.head, rest[:n]...), rest[n:]
	}
	h.tail = append(h.tail, rest...)
	if len(h.tail) > 2*keptTail {
		h.tail = append(h.tail[:0], h.tail[len(h.tail)-keptTail:]...)
	}
	return len(p), nil
}

// String is what was kept, saying how much was left out between the start and the end.
func (h *headTail) String() string {
	tail := h.tail[max(len(h.tail)-keptTail, 0):]
	left := h.total - len(h.head) - len(tail)
	if left == 0 {
		return string(h.head) + string(tail)
	}
	return string(h.head) + "\n[" + strconv.Itoa(left) + " bytes left out]\n" + string(tail)
}
