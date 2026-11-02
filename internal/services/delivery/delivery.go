// Package delivery is the domain of Cobranch's built-in GitOps: which targets a branch's environments deliver to, and
// what a delivery renders for one of them, from the delivery key of /cobranch.yaml. It knows nothing of Helm,
// Kustomize or Kubernetes: adapters render and apply what it plans
// (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/delivery.md).
package delivery

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"

	"cobranch.dev/internal/services/code"
)

// ErrInvalid is a delivery that can't be planned as its /cobranch.yaml declares it: a namespace or a path that its
// variables make invalid, or a target it doesn't declare.
var ErrInvalid = errors.New("invalid delivery")

// Kind is how a root renders.
type Kind string

// The kinds of root.
const (
	Helm      Kind = "helm"
	Kustomize Kind = "kustomize"
	Manifests Kind = "manifests"
)

// Context is what a delivery's variables name besides its target: the branch it delivers, the commit, and the
// repository's and its organization's slugs, which may be empty where nothing names them.
type Context struct {
	Branch       string
	Commit       string
	Repository   string
	Organization string
}

// Plan is what a delivery renders for one environment: its namespace and its roots, with every variable expanded, and
// the variables' values, which roots with Substitute expand in their files.
type Plan struct {
	Namespace string
	Roots     []PlannedRoot
	Variables map[string]string
}

// PlannedRoot is a root ready to render: a clean path relative to the repository, how it renders, and, for a chart,
// its release, its values files relative to the repository and its values.
type PlannedRoot struct {
	Path        string
	Kind        Kind
	Release     string
	ValuesFiles []string
	Values      map[string]any
	Substitute  bool
}

// Match reports whether branch matches pattern: '*' matches within one segment, '**' across them, '?' one character
// other than '/', and anything else itself.
func Match(pattern, branch string) bool {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(pattern); i++ {
		switch c := pattern[i]; {
		case c == '*' && i+1 < len(pattern) && pattern[i+1] == '*':
			b.WriteString(".*")
			i++
		case c == '*':
			b.WriteString("[^/]*")
		case c == '?':
			b.WriteString("[^/]")
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteString("$")
	return regexp.MustCompile(b.String()).MatchString(branch)
}

// TargetsOf is the targets whose patterns match branch, in the order d declares them: those it has an environment in.
func TargetsOf(d code.Delivery, branch string) []code.Target {
	var out []code.Target
	for _, t := range d.Targets {
		if slices.ContainsFunc(t.Branches, func(p string) bool { return Match(p, branch) }) {
			out = append(out, t)
		}
	}
	return out
}

// Target is the target d names name, or ErrInvalid.
func Target(d code.Delivery, name string) (code.Target, error) {
	for _, t := range d.Targets {
		if t.Name == name {
			return t, nil
		}
	}
	return code.Target{}, fmt.Errorf("%w: no target %s", ErrInvalid, name)
}

// maxSlug is the longest ${branch.slug}, which leaves room in a namespace's 63 characters for a prefix.
const maxSlug = 40

// nonAlphanumeric is a run of characters a slug replaces with one '-'.
var nonAlphanumeric = regexp.MustCompile(`[^a-z0-9]+`)

// Slug is ${branch.slug}: branch lowercased, each run of characters other than letters and digits a '-', trimmed of
// '-', and cut to maxSlug characters; "branch" if nothing is left.
func Slug(branch string) string {
	s := strings.Trim(nonAlphanumeric.ReplaceAllString(strings.ToLower(branch), "-"), "-")
	if len(s) > maxSlug {
		s = strings.TrimRight(s[:maxSlug], "-")
	}
	if s == "" {
		return "branch"
	}
	return s
}

// dnsLabel is a Kubernetes namespace's name.
var dnsLabel = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`)

// releaseName is a Helm release's name, at most 53 characters.
var releaseName = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$`)

// PlanFor plans what a delivery of c renders for target from roots: the roots that serve target, in order, with their
// variables expanded. A namespace that isn't a DNS label, a path that leaves the repository, or a release Helm
// wouldn't take is ErrInvalid.
func PlanFor(roots []code.Root, target code.Target, c Context) (Plan, error) {
	vars := map[string]string{"target": target.Name, "branch": c.Branch, "branch.slug": Slug(c.Branch)}
	if c.Commit != "" {
		vars["commit"], vars["commit.short"] = c.Commit, c.Commit[:min(7, len(c.Commit))]
	}
	if c.Repository != "" {
		vars["repository"] = c.Repository
	}
	if c.Organization != "" {
		vars["organization"] = c.Organization
	}
	namespace, err := code.Expand(target.Namespace, vars)
	if err != nil {
		return Plan{}, fmt.Errorf("%w: target %s's namespace: %w", ErrInvalid, target.Name, err)
	}
	if !dnsLabel.MatchString(namespace) {
		return Plan{}, fmt.Errorf("%w: target %s's namespace %q is not a DNS label: 1 to 63 lowercase letters, digits and '-'", ErrInvalid, target.Name, namespace)
	}
	vars["namespace"] = namespace
	plan := Plan{Namespace: namespace, Variables: vars}
	for i, r := range roots {
		if len(r.Targets) > 0 && !slices.Contains(r.Targets, target.Name) {
			continue
		}
		p, err := planRoot(r, vars)
		if err != nil {
			return Plan{}, fmt.Errorf("%w: root %d: %w", ErrInvalid, i+1, err)
		}
		plan.Roots = append(plan.Roots, p)
	}
	return plan, nil
}

// planRoot expands one root's variables and checks its path and release.
func planRoot(r code.Root, vars map[string]string) (PlannedRoot, error) {
	dir, err := repoPath(r.Path, vars)
	if err != nil {
		return PlannedRoot{}, err
	}
	p := PlannedRoot{Path: dir, Kind: Manifests, Substitute: r.Substitute}
	switch {
	case r.Kustomize != nil:
		p.Kind = Kustomize
	case r.Helm != nil:
		p.Kind = Helm
		if p.Release, err = release(r.Helm.Release, dir, vars); err != nil {
			return PlannedRoot{}, err
		}
		for _, f := range r.Helm.ValuesFiles {
			file, err := repoPath(path.Join(r.Path, f), vars)
			if err != nil {
				return PlannedRoot{}, fmt.Errorf("values file %s: %w", f, err)
			}
			p.ValuesFiles = append(p.ValuesFiles, file)
		}
		if len(r.Helm.Values) == 0 {
			return p, nil
		}
		values, err := expandValues(r.Helm.Values, vars, "values")
		if err != nil {
			return PlannedRoot{}, err
		}
		p.Values, _ = values.(map[string]any) // expandValues keeps a map a map
	}
	return p, nil
}

// repoPath expands p's variables and cleans it, refusing a path that leaves the repository or is its root.
func repoPath(p string, vars map[string]string) (string, error) {
	expanded, err := code.Expand(p, vars)
	if err != nil {
		return "", fmt.Errorf("path %s: %w", p, err)
	}
	clean := path.Clean(expanded)
	if path.IsAbs(clean) || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("path %q must name a directory inside the repository", expanded)
	}
	return clean, nil
}

// release is a chart's release: name with its variables expanded, or dir's last element without one.
func release(name, dir string, vars map[string]string) (string, error) {
	if name == "" {
		name = path.Base(dir)
	} else {
		var err error
		if name, err = code.Expand(name, vars); err != nil {
			return "", fmt.Errorf("release: %w", err)
		}
	}
	if len(name) > 53 || !releaseName.MatchString(name) {
		return "", fmt.Errorf("release %q: at most 53 lowercase letters, digits, '-' and '.'", name)
	}
	return name, nil
}

// expandValues expands the variables in every string under v, a value a YAML mapping decoded to; at names v in an
// error, as a path of keys and indexes.
func expandValues(v any, vars map[string]string, at string) (any, error) {
	switch v := v.(type) {
	case string:
		s, err := code.Expand(v, vars)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", at, err)
		}
		return s, nil
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, e := range v {
			x, err := expandValues(e, vars, at+"."+k)
			if err != nil {
				return nil, err
			}
			out[k] = x
		}
		return out, nil
	case []any:
		out := make([]any, len(v))
		for i, e := range v {
			x, err := expandValues(e, vars, fmt.Sprintf("%s[%d]", at, i))
			if err != nil {
				return nil, err
			}
			out[i] = x
		}
		return out, nil
	default:
		return v, nil
	}
}
