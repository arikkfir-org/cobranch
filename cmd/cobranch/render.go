package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"cobranch.dev/internal/adapters/render"
	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/delivery"
	"cobranch.dev/internal/services/git"
)

// renderUsage is what `cobranch render` takes: its input, as flags, since it is a one-off command.
const renderUsage = "usage: cobranch render --target <target> --branch <branch> --commit <sha> [--dir <checkout>] [--repository <slug>] [--organization <slug>] [--kube-version <v1.33.0>] [--api-versions <group/version,...>]"

// errUsage is a command line `cobranch render` doesn't take.
var errUsage = errors.New(renderUsage)

// runRender prints what a delivery of a branch's commit to one of its targets would apply: the roots of the
// checkout's /cobranch.yaml, rendered for the target that file names, as one YAML stream. It is how a developer sees
// what Cobranch would apply before pushing. It returns run's exit code: 2 on a command line it doesn't take.
func runRender(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	err := renderCheckout(ctx, args, stdout)
	switch {
	case errors.Is(err, errUsage):
		_, _ = fmt.Fprintln(stderr, err) // stderr is gone; nothing to tell
		return 2
	case err != nil:
		_, _ = fmt.Fprintf(stderr, "cobranch render: %v\n", err) // stderr is gone; nothing to tell
		return 1
	}
	return 0
}

// renderFlags are `cobranch render`'s flags.
type renderFlags struct {
	dir, target, branch, commit, repository, organization, kubeVersion, apiVersions string
}

// parseRenderFlags reads args, or errUsage.
func parseRenderFlags(args []string) (renderFlags, error) {
	var f renderFlags
	set := flag.NewFlagSet("render", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	set.StringVar(&f.dir, "dir", ".", "the repository's checkout")
	set.StringVar(&f.target, "target", "", "the target to render for")
	set.StringVar(&f.branch, "branch", "", "the branch delivered")
	set.StringVar(&f.commit, "commit", "", "the commit delivered")
	set.StringVar(&f.repository, "repository", "", "the repository's slug")
	set.StringVar(&f.organization, "organization", "", "the organization's slug")
	set.StringVar(&f.kubeVersion, "kube-version", "", "the target cluster's Kubernetes version")
	set.StringVar(&f.apiVersions, "api-versions", "", "API versions the target cluster serves, comma-separated")
	if err := set.Parse(args); err != nil || set.NArg() > 0 || f.target == "" || f.branch == "" || f.commit == "" {
		return f, errUsage
	}
	return f, nil
}

// renderCheckout renders the checkout args name and writes it to stdout.
func renderCheckout(ctx context.Context, args []string, stdout io.Writer) error {
	f, err := parseRenderFlags(args)
	if err != nil {
		return err
	}
	content, err := os.ReadFile(filepath.Join(f.dir, git.SettingsFile))
	if err != nil {
		return fmt.Errorf("reading /%s: %w", git.SettingsFile, err)
	}
	d, err := code.ParseDelivery(content)
	if err != nil {
		return fmt.Errorf("parsing: %w", err)
	}
	target, err := delivery.Target(d, f.target)
	if err != nil {
		return fmt.Errorf("planning: %w", err)
	}
	plan, err := delivery.PlanFor(d.Roots, target, delivery.Context{
		Branch: f.branch, Commit: f.commit, Repository: f.repository, Organization: f.organization})
	if err != nil {
		return fmt.Errorf("planning: %w", err)
	}
	opts := render.Options{KubeVersion: f.kubeVersion}
	if f.apiVersions != "" {
		opts.APIVersions = strings.Split(f.apiVersions, ",")
	}
	out, err := render.Render(ctx, f.dir, plan, opts)
	if err != nil {
		return fmt.Errorf("rendering: %w", err)
	}
	if _, err := stdout.Write(out); err != nil {
		return fmt.Errorf("writing the render: %w", err)
	}
	return nil
}
