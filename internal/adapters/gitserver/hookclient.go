package gitserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"cobranch.dev/internal/services/git"
)

// HookEnv is what a push's pre-receive hook reads from its environment: where its Git server listens and the token of
// its push, from the server, and where Git keeps the push's objects, from Git.
type HookEnv struct {
	URL             string
	Token           string
	Quarantine      string
	ObjectDirectory string
	Alternates      string
}

// RunHook is a push's pre-receive hook, cobranch git-hook: it sends the ref updates Git gives it on stdin to the Git
// server that started the push, prints the server's answer to stderr, which Git shows the client as remote: lines,
// and returns the exit code Git reads, 0 to accept the push and 1 to refuse it whole. It refuses the push when it
// can't reach the server, so nothing changes.
func RunHook(ctx context.Context, env HookEnv, stdin io.Reader, stderr io.Writer) int {
	err := callHook(ctx, env, stdin, stderr)
	switch {
	case err == nil:
		return 0
	case !errors.Is(err, errRefused):
		_, _ = fmt.Fprintln(stderr, git.MessageNotRecorded) // stderr is the client's; nothing else to tell
	}
	return 1
}

// errRefused is a push its Git server refused, whose reason the hook printed already.
var errRefused = errors.New("refused")

func callHook(ctx context.Context, env HookEnv, stdin io.Reader, stderr io.Writer) error {
	updates, err := io.ReadAll(stdin)
	if err != nil {
		return fmt.Errorf("reading the ref updates: %w", err)
	}
	body, err := json.Marshal(hookRequest{Updates: string(updates), Quarantine: env.Quarantine, ObjectDirectory: env.ObjectDirectory, Alternates: env.Alternates})
	if err != nil {
		return fmt.Errorf("encoding the call: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, env.URL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("making the call: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+env.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("calling the Git server: %w", err)
	}
	defer func() { _ = resp.Body.Close() }() // read whole below
	switch resp.StatusCode {
	case http.StatusOK, http.StatusConflict:
		if _, err := io.Copy(stderr, resp.Body); err != nil {
			return fmt.Errorf("reading the answer: %w", err)
		}
		if resp.StatusCode == http.StatusConflict {
			return errRefused
		}
		return nil
	default:
		return fmt.Errorf("the Git server answered %s", resp.Status)
	}
}
