package code

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"

	"cobranch.dev/internal/services/git"
	"cobranch.dev/internal/services/jobs"
)

// SettingsError is a SettingsFile that doesn't parse, naming the line at fault, as a push into the default branch that
// brings it is refused.
type SettingsError struct {
	Line    int
	Message string
}

func (e *SettingsError) Error() string {
	if e.Line == 0 {
		return fmt.Sprintf("/%s: %s", git.SettingsFile, e.Message)
	}
	return fmt.Sprintf("/%s: line %d: %s", git.SettingsFile, e.Line, e.Message)
}

// Settings are what a SettingsFile says: of the repository, read from the default branch's only; the branches' merge
// requirements, read from the default branch's only; and the jobs, read from the file at the tip an event happened
// on. The JSON a store keeps of them leaves the merge requirements out, which it keeps on their own.
type Settings struct {
	Repository jobs.RepositorySettings `json:"repository"`
	Branches   []git.Requirements      `json:"-"`
	// Jobs are by name.
	Jobs []jobs.Job `json:"jobs"`
}

// ParseSettings reads a SettingsFile, or answers a *SettingsError naming the line at fault: its top-level keys are
// repository, branches and jobs, and every key under them is checked, so a misspelt requirement or trigger refuses
// the file rather than requiring or running nothing. An empty file says nothing: the repository's defaults, no
// requirements and no jobs.
func ParseSettings(content []byte) (Settings, error) {
	out := Settings{Repository: jobs.DefaultRepositorySettings, Jobs: []jobs.Job{}}
	var doc yaml.Node
	dec := yaml.NewDecoder(bytes.NewReader(content))
	if err := dec.Decode(&doc); errors.Is(err, io.EOF) {
		return out, nil
	} else if err != nil {
		return Settings{}, yamlError(err)
	}
	if len(doc.Content) == 0 || isNull(doc.Content[0]) {
		return out, nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return Settings{}, &SettingsError{Line: root.Line, Message: "the settings must be a mapping"}
	}
	if err := onlyKeys(root, "the settings", "repository", "branches", "jobs"); err != nil {
		return Settings{}, err
	}
	var err error
	if out.Repository, err = repositorySettings(valueOf(root, "repository")); err != nil {
		return Settings{}, err
	}
	if out.Branches, err = branchesRequirements(valueOf(root, "branches")); err != nil {
		return Settings{}, err
	}
	if out.Jobs, err = parseJobs(valueOf(root, "jobs")); err != nil {
		return Settings{}, err
	}
	return out, nil
}

// branchesRequirements reads the merge requirements under branches, one per branch it names, in the order it names
// them.
func branchesRequirements(branches *yaml.Node) ([]git.Requirements, error) {
	if branches == nil || isNull(branches) {
		return nil, nil
	}
	if branches.Kind != yaml.MappingNode {
		return nil, &SettingsError{Line: branches.Line, Message: "branches must map branch names to their settings"}
	}
	if err := uniqueKeys(branches, "branches"); err != nil {
		return nil, err
	}
	var out []git.Requirements
	for i := 0; i+1 < len(branches.Content); i += 2 {
		key, value := branches.Content[i], branches.Content[i+1]
		if key.Kind != yaml.ScalarNode || key.Tag != "!!str" || key.Value == "" {
			return nil, &SettingsError{Line: key.Line, Message: "a branch's name must be a string"}
		}
		r, err := branchRequirements(key.Value, value)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

// branchRequirements reads one branch's settings under branches: requires, with its annotations and linearHistory.
func branchRequirements(branch string, node *yaml.Node) (git.Requirements, error) {
	r := git.Requirements{Branch: branch}
	if isNull(node) {
		return r, nil
	}
	if err := onlyKeys(node, branch+"'s settings", "requires"); err != nil {
		return r, err
	}
	requires := valueOf(node, "requires")
	if requires == nil || isNull(requires) {
		return r, nil
	}
	if err := onlyKeys(requires, "requires", "annotations", "linearHistory"); err != nil {
		return r, err
	}
	if n := valueOf(requires, "annotations"); n != nil && !isNull(n) {
		names, err := annotationNames(n)
		if err != nil {
			return r, err
		}
		r.Annotations = names
	}
	if n := valueOf(requires, "linearHistory"); n != nil && !isNull(n) {
		var linear bool
		if n.Kind != yaml.ScalarNode || n.Tag != "!!bool" || n.Decode(&linear) != nil {
			return r, &SettingsError{Line: n.Line, Message: "linearHistory must be true or false"}
		}
		r.LinearHistory = linear
	}
	return r, nil
}

// annotationNames reads a list of annotations' names, each once.
func annotationNames(node *yaml.Node) ([]string, error) {
	if node.Kind != yaml.SequenceNode {
		return nil, &SettingsError{Line: node.Line, Message: "annotations must be a list of names"}
	}
	names := make([]string, 0, len(node.Content))
	for _, n := range node.Content {
		if n.Kind != yaml.ScalarNode || n.Tag != "!!str" || ValidateAnnotationName(n.Value) != nil {
			return nil, &SettingsError{Line: n.Line, Message: "annotations must be a list of names: 1 to 100 lowercase letters, digits, '-', '_', '.' and '/', starting with a letter or digit"}
		}
		if slices.Contains(names, n.Value) {
			return nil, &SettingsError{Line: n.Line, Message: "annotation " + n.Value + " is required twice"}
		}
		names = append(names, n.Value)
	}
	return names, nil
}

// onlyKeys checks that node is a mapping of no other keys than allowed, each once; what names it in the message.
func onlyKeys(node *yaml.Node, what string, allowed ...string) error {
	if node.Kind != yaml.MappingNode {
		return &SettingsError{Line: node.Line, Message: what + " must be a mapping"}
	}
	for i := 0; i < len(node.Content); i += 2 {
		if key := node.Content[i]; !slices.Contains(allowed, key.Value) {
			return &SettingsError{Line: key.Line, Message: fmt.Sprintf("%s takes no %q", what, key.Value)}
		}
	}
	return uniqueKeys(node, what)
}

// uniqueKeys checks that mapping node names each key once, as YAML requires; what names it in the message.
func uniqueKeys(node *yaml.Node, what string) error {
	seen := map[string]bool{}
	for i := 0; i < len(node.Content); i += 2 {
		key := node.Content[i]
		if seen[key.Value] {
			return &SettingsError{Line: key.Line, Message: fmt.Sprintf("%s names %q twice", what, key.Value)}
		}
		seen[key.Value] = true
	}
	return nil
}

// valueOf is the value of key in mapping node, or nil without one.
func valueOf(node *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}

// isNull reports whether node is YAML's null: empty, ~ or null.
func isNull(node *yaml.Node) bool {
	return node.Kind == yaml.ScalarNode && node.Tag == "!!null"
}

// yamlLine is how the YAML parser names the line of a syntax error.
var yamlLine = regexp.MustCompile(`^yaml: line (\d+): (.*)$`)

// yamlError is a YAML syntax error as a *SettingsError, with the line the parser names.
func yamlError(err error) error {
	if m := yamlLine.FindStringSubmatch(err.Error()); m != nil {
		line, _ := strconv.Atoi(m[1]) // the pattern matched digits
		return &SettingsError{Line: line, Message: m[2]}
	}
	return &SettingsError{Message: strings.TrimPrefix(err.Error(), "yaml: ")}
}
