package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"cobranch.dev/internal/adapters/render"
	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/delivery"
	"cobranch.dev/internal/services/git"
	"cobranch.dev/internal/system/config"
)

// renderUsage is what `cobranch render` takes: its input, as flags, since it is a one-off command.
const renderUsage = "usage: cobranch render --target <target> --branch <branch> --commit <sha> [--namespace <namespace>] [--dir <checkout>] [--repository <slug>] [--organization <slug>] [--kube-version <v1.33.0>] [--api-versions <group/version,...>]"

// errUsage is a command line `cobranch render` doesn't take.
var errUsage = errors.New(renderUsage)

// runRender prints what a delivery of a branch's commit to one of its targets would apply: the roots of the
// checkout's /cobranch.yaml, rendered for the target, as one YAML stream. A delivery's render pod names the target's
// environment namespace, which main's file grants, since a branch's own targets count for nothing, and uploads the
// render rather than print it, since it may hold Secrets; a developer may leave the namespace to the checkout's
// targets instead, to see what Cobranch would apply before pushing. It returns run's exit code: 2 on a command line it
// doesn't take.
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
	dir, target, namespace, branch, commit, repository, organization, kubeVersion, apiVersions string
}

// parseRenderFlags reads args, or errUsage.
func parseRenderFlags(args []string) (renderFlags, error) {
	var f renderFlags
	set := flag.NewFlagSet("render", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	set.StringVar(&f.dir, "dir", ".", "the repository's checkout")
	set.StringVar(&f.target, "target", "", "the target to render for")
	set.StringVar(&f.namespace, "namespace", "", "the environment's namespace, rather than the checkout's target's")
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
	up, err := config.LoadRender()
	if err != nil {
		return fmt.Errorf("configuring: %w", err)
	}
	d, err := checkoutDelivery(f.dir)
	if err != nil {
		return err
	}
	target, err := renderTarget(d, f)
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
	if up.URL != "" {
		return upload(ctx, up, out, stdout)
	}
	if _, err := stdout.Write(out); err != nil {
		return fmt.Errorf("writing the render: %w", err)
	}
	return nil
}

// checkoutDelivery is the delivery settings of the checkout at dir's /cobranch.yaml: none without the file, which
// renders nothing.
func checkoutDelivery(dir string) (code.DeliverySettings, error) {
	content, err := os.ReadFile(filepath.Join(dir, git.SettingsFile))
	if errors.Is(err, fs.ErrNotExist) {
		return code.DeliverySettings{}, nil
	} else if err != nil {
		return code.DeliverySettings{}, fmt.Errorf("reading /%s: %w", git.SettingsFile, err)
	}
	settings, err := code.ParseSettings(content)
	if err != nil {
		return code.DeliverySettings{}, fmt.Errorf("parsing: %w", err)
	}
	return settings.Delivery, nil
}

// uploadTimeout is how long an upload may take.
const uploadTimeout = 2 * time.Minute

// upload posts the render to its delivery, as up says, and says how much it uploaded on stdout. The REST server keeps
// it once, while the delivery renders: anything but 201 fails, with the server's problem.
func upload(ctx context.Context, up config.Render, render []byte, stdout io.Writer) error {
	ctx, cancel := context.WithTimeout(ctx, uploadTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, up.URL, bytes.NewReader(render))
	if err != nil {
		return fmt.Errorf("uploading the render to %s: %w", up.URL, err)
	}
	req.Header.Set("Content-Type", "application/yaml")
	req.Header.Set("Authorization", "Bearer "+up.Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("uploading the render: %w", err)
	}
	defer func() { _ = resp.Body.Close() }() // read to its end or abandoned; nothing to tell
	if resp.StatusCode != http.StatusCreated {
		var problem struct{ Title, Detail string }
		_ = json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&problem) // a body that isn't a problem says nothing more
		why := slices.DeleteFunc([]string{resp.Status, problem.Title, problem.Detail}, func(s string) bool { return s == "" })
		return fmt.Errorf("uploading the render: %s", strings.Join(why, ": "))
	}
	if _, err := fmt.Fprintf(stdout, "Uploaded the render, %d bytes.\n", len(render)); err != nil {
		return fmt.Errorf("writing what it uploaded: %w", err)
	}
	return nil
}

// renderTarget is the target f renders for: the one f names with the namespace f names, or the checkout's target of
// that name without one.
func renderTarget(d code.DeliverySettings, f renderFlags) (code.Target, error) {
	if f.namespace != "" {
		return code.Target{Name: f.target, Namespace: f.namespace}, nil
	}
	target, err := delivery.Target(d, f.target)
	if err != nil {
		return code.Target{}, fmt.Errorf("%w: give the environment's namespace, or name a target the checkout declares", err)
	}
	return target, nil
}
