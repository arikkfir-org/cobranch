// Package workspace holds the tools an agent's model works its task's workspace with: its files, listed, read,
// searched, written and edited, and a shell there. They implement the harness's Tool port. The pod is what confines
// the shell; the file tools keep to the workspace, symbolic links included.
package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cobranch.dev/internal/services/harness"
)

// Workspace is the directory a task works in, and its tools.
type Workspace struct {
	dir          string
	root         *os.Root
	shellTimeout time.Duration
	git          Git
	// gitDir holds the Git configuration that every git the workspace runs, the model's commands' included, reads in
	// place of the user's own (gitConfig). It lies outside the home directory, so that running the harness by hand
	// leaves the user's configuration as it was.
	gitDir string
}

// New returns the Workspace of dir, whose shell commands run for at most shellTimeout each.
func New(dir string, shellTimeout time.Duration) (*Workspace, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("finding the workspace %s: %w", dir, err)
	}
	root, err := os.OpenRoot(abs)
	if err != nil {
		return nil, fmt.Errorf("opening the workspace %s: %w", abs, err)
	}
	gitDir, err := os.MkdirTemp("", "cobranch-git-")
	if err != nil {
		_ = root.Close() // opened for reading only; the error that matters is the one above
		return nil, fmt.Errorf("making a directory for the workspace's Git configuration: %w", err)
	}
	return &Workspace{dir: abs, root: root, shellTimeout: shellTimeout, gitDir: gitDir}, nil
}

// Close closes the workspace's directory, and removes its Git configuration.
func (w *Workspace) Close() error {
	var errs []error
	if err := w.root.Close(); err != nil {
		errs = append(errs, fmt.Errorf("closing the workspace: %w", err))
	}
	if err := os.RemoveAll(w.gitDir); err != nil {
		errs = append(errs, fmt.Errorf("removing the workspace's Git configuration: %w", err))
	}
	return errors.Join(errs...)
}

// gitConfig is the file of the workspace's Git configuration, which every git it runs reads as the user's.
func (w *Workspace) gitConfig() string {
	return filepath.Join(w.gitDir, "config")
}

// Tools are the workspace's tools, for the harness to offer the model.
func (w *Workspace) Tools() []harness.Tool {
	return []harness.Tool{
		tool[listArgs]{spec: listSpec, run: w.list},
		tool[readArgs]{spec: readSpec, run: w.read},
		tool[searchArgs]{spec: searchSpec, run: w.search},
		tool[writeArgs]{spec: writeSpec, run: w.write},
		tool[editArgs]{spec: editSpec, run: w.edit},
		tool[shellArgs]{spec: shellSpec(w.shellTimeout), run: w.shell},
	}
}

// tool is a tool whose arguments are an A.
type tool[A any] struct {
	spec harness.Spec
	run  func(ctx context.Context, args A) (string, error)
}

// Spec describes the tool (harness.Tool).
func (t tool[A]) Spec() harness.Spec { return t.spec }

// Run reads the call's arguments, refusing fields the tool doesn't take, and runs it (harness.Tool).
func (t tool[A]) Run(ctx context.Context, c harness.Call) (string, error) {
	var args A
	d := json.NewDecoder(bytes.NewReader(c.Arguments))
	d.DisallowUnknownFields()
	if err := d.Decode(&args); err != nil {
		return "", fmt.Errorf("reading the arguments: %w", err)
	}
	return t.run(ctx, args)
}

// spec is a tool's Spec, whose parameters are an object of the properties, the required ones named.
func spec(name, description, properties string, required ...string) harness.Spec {
	names, _ := json.Marshal(required) // a slice of strings always marshals
	if required == nil {
		names = []byte("[]")
	}
	return harness.Spec{Name: name, Description: description,
		Parameters: json.RawMessage(`{"type":"object","properties":{` + properties + `},"required":` + string(names) + `}`)}
}

// rel is path, relative to the workspace or absolute within it, as a path relative to it: "." for the workspace.
func (w *Workspace) rel(path string) (string, error) {
	if path == "" {
		return ".", nil
	}
	r := filepath.Clean(path)
	if filepath.IsAbs(path) {
		var err error
		if r, err = filepath.Rel(w.dir, path); err != nil {
			r = ".."
		}
	}
	if r == ".." || strings.HasPrefix(r, "../") {
		return "", fmt.Errorf("%s is outside the workspace, %s", path, w.dir)
	}
	return r, nil
}
