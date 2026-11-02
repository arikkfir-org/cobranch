package code

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"

	"github.com/google/uuid"
	"go.yaml.in/yaml/v3"
)

// Delivery is the delivery key of a SettingsFile: the targets that map branch patterns to a cluster and a namespace,
// which count only at the default branch's tip, and the roots each branch renders for its environments, which count
// at the tip being delivered
// (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/delivery.md, "/cobranch.yaml"). Its JSON
// form is how a push records it.
type Delivery struct {
	Targets []Target `json:"targets,omitempty"`
	Roots   []Root   `json:"roots,omitempty"`
}

// Target is where the branches its patterns match deliver: a cluster, by the ID of the secret that holds its
// kubeconfig, or the installation's own cluster when Cluster is nil, and a namespace, which may name Variables.
type Target struct {
	Name      string     `json:"name"`
	Branches  []string   `json:"branches"`
	Cluster   *uuid.UUID `json:"cluster,omitempty"`
	Namespace string     `json:"namespace"`
}

// Root is a directory of the commit that a delivery renders: a Helm chart when Helm is set, a kustomization when
// Kustomize is, and otherwise every manifest under it. Targets, when set, limits it to those targets; Substitute
// replaces Variables in its files too.
type Root struct {
	Path       string         `json:"path"`
	Helm       *HelmRoot      `json:"helm,omitempty"`
	Kustomize  *KustomizeRoot `json:"kustomize,omitempty"`
	Targets    []string       `json:"targets,omitempty"`
	Substitute bool           `json:"substitute,omitempty"`
}

// HelmRoot renders a chart: its release's name (the root directory's own name when empty), the values files it reads
// in order, relative to the chart, and the values set last.
type HelmRoot struct {
	Release     string         `json:"release,omitempty"`
	ValuesFiles []string       `json:"valuesFiles,omitempty"`
	Values      map[string]any `json:"values,omitempty"`
}

// KustomizeRoot builds a kustomization; it takes no settings yet.
type KustomizeRoot struct{}

// Variables are the names a delivery's strings may use as ${name}, and that a root with Substitute may use in its
// files.
var Variables = []string{"target", "branch", "branch.slug", "commit", "commit.short", "namespace", "repository", "organization"}

// ErrUnknownVariable is a ${name} that Variables doesn't list, or that has no value where it is expanded.
var ErrUnknownVariable = errors.New("unknown variable")

// variable is ${name}, or $${name}, which writes ${name} itself.
var variable = regexp.MustCompile(`\$?\$\{([^}]*)\}`)

// Expand replaces each ${name} in s with values[name], and each $${name} with ${name}. A name values lacks is
// ErrUnknownVariable, naming it.
func Expand(s string, values map[string]string) (string, error) {
	var bad error
	out := variable.ReplaceAllStringFunc(s, func(m string) string {
		if strings.HasPrefix(m, "$$") {
			return m[1:]
		}
		name := m[2 : len(m)-1]
		v, ok := values[name]
		if !ok && bad == nil {
			bad = fmt.Errorf("%w ${%s}", ErrUnknownVariable, name)
		}
		return v
	})
	return out, bad
}

// checkVariables reports a ${name} in s that Variables doesn't list, or ${namespace} where it would name itself.
func checkVariables(s string, self bool) error {
	for _, m := range variable.FindAllStringSubmatch(s, -1) {
		if strings.HasPrefix(m[0], "$$") {
			continue
		}
		if !slices.Contains(Variables, m[1]) || (self && m[1] == "namespace") {
			return fmt.Errorf("${%s} is not a variable: use %s", m[1], "${"+strings.Join(Variables, "}, ${")+"}")
		}
	}
	return nil
}

// targetName is a target's name: 1 to 40 lowercase letters, digits and '-', starting with a letter.
var targetName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,39}$`)

// ParseDelivery reads the delivery key of a SettingsFile, or a *SettingsError naming the line at fault. A file without
// one delivers nothing. Every key under delivery is checked, so a misspelt setting refuses the file rather than
// delivering something else.
func ParseDelivery(content []byte) (Delivery, error) {
	var doc yaml.Node
	if err := yaml.NewDecoder(bytes.NewReader(content)).Decode(&doc); errors.Is(err, io.EOF) {
		return Delivery{}, nil
	} else if err != nil {
		return Delivery{}, yamlError(err)
	}
	if len(doc.Content) == 0 || isNull(doc.Content[0]) {
		return Delivery{}, nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return Delivery{}, &SettingsError{Line: root.Line, Message: "the settings must be a mapping"}
	}
	if err := uniqueKeys(root, "the settings"); err != nil {
		return Delivery{}, err
	}
	return parseDelivery(valueOf(root, "delivery"))
}

// parseDelivery reads the delivery key's value, node, which may be nil when the file has none.
func parseDelivery(node *yaml.Node) (Delivery, error) {
	var d Delivery
	if node == nil || isNull(node) {
		return d, nil
	}
	if err := onlyKeys(node, "delivery", "targets", "roots"); err != nil {
		return d, err
	}
	if n := valueOf(node, "targets"); n != nil && !isNull(n) {
		targets, err := parseTargets(n)
		if err != nil {
			return d, err
		}
		d.Targets = targets
	}
	if n := valueOf(node, "roots"); n != nil && !isNull(n) {
		roots, err := parseRoots(n)
		if err != nil {
			return d, err
		}
		d.Roots = roots
	}
	return d, nil
}

// parseTargets reads delivery.targets: a mapping of each target's name to its settings, in the file's order.
func parseTargets(node *yaml.Node) ([]Target, error) {
	if node.Kind != yaml.MappingNode {
		return nil, &SettingsError{Line: node.Line, Message: "targets must map targets' names to their settings"}
	}
	if err := uniqueKeys(node, "targets"); err != nil {
		return nil, err
	}
	targets := make([]Target, 0, len(node.Content)/2)
	for i := 0; i+1 < len(node.Content); i += 2 {
		key, value := node.Content[i], node.Content[i+1]
		if key.Kind != yaml.ScalarNode || key.Tag != "!!str" || !targetName.MatchString(key.Value) {
			return nil, &SettingsError{Line: key.Line, Message: "a target's name: 1 to 40 lowercase letters, digits and '-', starting with a letter"}
		}
		t, err := parseTarget(key.Value, value)
		if err != nil {
			return nil, err
		}
		targets = append(targets, t)
	}
	return targets, nil
}

// parseTarget reads one target's settings: its branches, its cluster and its namespace.
func parseTarget(name string, node *yaml.Node) (Target, error) {
	t := Target{Name: name}
	if isNull(node) {
		return t, &SettingsError{Line: node.Line, Message: name + " needs branches and a namespace"}
	}
	if err := onlyKeys(node, name, "branches", "cluster", "namespace"); err != nil {
		return t, err
	}
	branches := valueOf(node, "branches")
	if branches == nil {
		return t, &SettingsError{Line: node.Line, Message: name + " needs branches"}
	}
	patterns, err := stringList(branches, "branches must be a list of branch patterns")
	if err != nil {
		return t, err
	}
	if len(patterns) == 0 {
		return t, &SettingsError{Line: branches.Line, Message: "branches must name at least one branch pattern"}
	}
	t.Branches = patterns
	if n := valueOf(node, "cluster"); n != nil && !isNull(n) {
		id, err := uuid.Parse(n.Value)
		if n.Kind != yaml.ScalarNode || n.Tag != "!!str" || err != nil {
			return t, &SettingsError{Line: n.Line, Message: "cluster must be the ID of the secret that holds its kubeconfig"}
		}
		t.Cluster = &id
	}
	namespace := valueOf(node, "namespace")
	if namespace == nil || isNull(namespace) {
		return t, &SettingsError{Line: node.Line, Message: name + " needs a namespace"}
	}
	if t.Namespace, err = templated(namespace, "namespace", true); err != nil {
		return t, err
	}
	return t, nil
}

// parseRoots reads delivery.roots: a list of the directories each delivery renders, in order.
func parseRoots(node *yaml.Node) ([]Root, error) {
	if node.Kind != yaml.SequenceNode {
		return nil, &SettingsError{Line: node.Line, Message: "roots must be a list of directories to render"}
	}
	roots := make([]Root, 0, len(node.Content))
	for _, n := range node.Content {
		r, err := parseRoot(n)
		if err != nil {
			return nil, err
		}
		roots = append(roots, r)
	}
	return roots, nil
}

// parseRoot reads one root: its path, how it renders, the targets it serves and whether its files take variables.
func parseRoot(node *yaml.Node) (Root, error) {
	var r Root
	if err := onlyKeys(node, "a root", "path", "helm", "kustomize", "targets", "substitute"); err != nil {
		return r, err
	}
	path := valueOf(node, "path")
	if path == nil || isNull(path) {
		return r, &SettingsError{Line: node.Line, Message: "a root needs a path"}
	}
	var err error
	if r.Path, err = templated(path, "path", false); err != nil {
		return r, err
	}
	if r.Helm, r.Kustomize, err = parseRenderer(node); err != nil {
		return r, err
	}
	if n := valueOf(node, "targets"); n != nil && !isNull(n) {
		if r.Targets, err = targetNames(n); err != nil {
			return r, err
		}
	}
	if n := valueOf(node, "substitute"); n != nil && !isNull(n) {
		if n.Kind != yaml.ScalarNode || n.Tag != "!!bool" || n.Decode(&r.Substitute) != nil {
			return r, &SettingsError{Line: n.Line, Message: "substitute must be true or false"}
		}
	}
	return r, nil
}

// parseRenderer reads how a root renders: with Helm, with Kustomize, or neither, as plain manifests.
func parseRenderer(node *yaml.Node) (*HelmRoot, *KustomizeRoot, error) {
	helm, kustomize := valueOf(node, "helm"), valueOf(node, "kustomize")
	switch {
	case helm != nil && kustomize != nil:
		return nil, nil, &SettingsError{Line: kustomize.Line, Message: "a root renders with helm or kustomize, not both"}
	case helm != nil:
		h, err := parseHelm(helm)
		return h, nil, err
	case kustomize != nil:
		if !isNull(kustomize) {
			if err := onlyKeys(kustomize, "kustomize"); err != nil {
				return nil, nil, err
			}
		}
		return nil, &KustomizeRoot{}, nil
	}
	return nil, nil, nil
}

// parseHelm reads a root's helm settings: its release, values files and values.
func parseHelm(node *yaml.Node) (*HelmRoot, error) {
	h := &HelmRoot{}
	if isNull(node) {
		return h, nil
	}
	if err := onlyKeys(node, "helm", "release", "valuesFiles", "values"); err != nil {
		return nil, err
	}
	var err error
	if n := valueOf(node, "release"); n != nil && !isNull(n) {
		if h.Release, err = templated(n, "release", false); err != nil {
			return nil, err
		}
	}
	if n := valueOf(node, "valuesFiles"); n != nil && !isNull(n) {
		if h.ValuesFiles, err = valuesFiles(n); err != nil {
			return nil, err
		}
	}
	if n := valueOf(node, "values"); n != nil && !isNull(n) {
		if h.Values, err = values(n); err != nil {
			return nil, err
		}
	}
	return h, nil
}

// valuesFiles reads a chart's values files: a list of paths, which may name Variables.
func valuesFiles(node *yaml.Node) ([]string, error) {
	files, err := stringList(node, "valuesFiles must be a list of files")
	if err != nil {
		return nil, err
	}
	for i, f := range files {
		if err := checkVariables(f, false); err != nil {
			return nil, &SettingsError{Line: node.Content[i].Line, Message: err.Error()}
		}
	}
	return files, nil
}

// values reads a chart's values: a mapping whose strings may name Variables.
func values(node *yaml.Node) (map[string]any, error) {
	if node.Kind != yaml.MappingNode {
		return nil, &SettingsError{Line: node.Line, Message: "values must be a mapping"}
	}
	if err := checkValues(node); err != nil {
		return nil, err
	}
	var v map[string]any
	if err := node.Decode(&v); err != nil {
		return nil, &SettingsError{Line: node.Line, Message: "values: " + err.Error()}
	}
	return v, nil
}

// checkValues reports a string anywhere under node that names an unknown variable, or a key that names or writes one
// at all: only values are expanded.
func checkValues(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode && node.Tag == "!!str" {
		if err := checkVariables(node.Value, false); err != nil {
			return &SettingsError{Line: node.Line, Message: err.Error()}
		}
	}
	if node.Kind == yaml.MappingNode {
		for i := 0; i < len(node.Content); i += 2 {
			if key := node.Content[i]; variable.MatchString(key.Value) {
				return &SettingsError{Line: key.Line, Message: fmt.Sprintf("values' keys take no variables: %q", key.Value)}
			}
		}
	}
	for _, n := range node.Content {
		if err := checkValues(n); err != nil {
			return err
		}
	}
	return nil
}

// targetNames reads a root's targets: a list of targets' names, each once.
func targetNames(node *yaml.Node) ([]string, error) {
	const message = "targets must be a list of targets' names"
	names, err := stringList(node, message)
	if err != nil {
		return nil, err
	}
	for i, name := range names {
		if !targetName.MatchString(name) {
			return nil, &SettingsError{Line: node.Content[i].Line, Message: message}
		}
		if slices.Contains(names[:i], name) {
			return nil, &SettingsError{Line: node.Content[i].Line, Message: "target " + name + " is named twice"}
		}
	}
	return names, nil
}

// templated reads a non-empty string that may name Variables; self refuses ${namespace}, which would name itself.
func templated(node *yaml.Node, what string, self bool) (string, error) {
	if node.Kind != yaml.ScalarNode || node.Tag != "!!str" || node.Value == "" {
		return "", &SettingsError{Line: node.Line, Message: what + " must be a string"}
	}
	if err := checkVariables(node.Value, self); err != nil {
		return "", &SettingsError{Line: node.Line, Message: err.Error()}
	}
	return node.Value, nil
}

// stringList reads a list of non-empty strings, or a *SettingsError with message.
func stringList(node *yaml.Node, message string) ([]string, error) {
	if node.Kind != yaml.SequenceNode {
		return nil, &SettingsError{Line: node.Line, Message: message}
	}
	out := make([]string, 0, len(node.Content))
	for _, n := range node.Content {
		if n.Kind != yaml.ScalarNode || n.Tag != "!!str" || n.Value == "" {
			return nil, &SettingsError{Line: n.Line, Message: message}
		}
		out = append(out, n.Value)
	}
	return out, nil
}
