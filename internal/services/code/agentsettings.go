package code

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"cobranch.dev/internal/services/agents"
	"cobranch.dev/internal/services/orgs"
)

// parseAgents reads the agents key of a SettingsFile: the repository's agents, one per name, by name. Only the default
// branch's file defines them, but every branch's must parse, so a branch that breaks the key can't merge it.
func parseAgents(node *yaml.Node) ([]agents.Declared, error) {
	out := []agents.Declared{}
	if node == nil || isNull(node) {
		return out, nil
	}
	if node.Kind != yaml.MappingNode {
		return nil, &SettingsError{Line: node.Line, Message: "agents must map agent names to their agents"}
	}
	if err := uniqueKeys(node, "agents"); err != nil {
		return nil, err
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		key, value := node.Content[i], node.Content[i+1]
		if key.Kind != yaml.ScalarNode || key.Tag != "!!str" || ValidateAnnotationName(key.Value) != nil {
			return nil, &SettingsError{Line: key.Line, Message: "an agent's name must be 1 to 100 lowercase letters, digits, '-', '_', '.' and '/', starting with a letter or digit"}
		}
		d, err := parseAgent(key.Value, key.Line, value)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	slices.SortFunc(out, func(a, b agents.Declared) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

// agentKeys are the keys of one agent under agents.
var agentKeys = []string{"description", "instructions", "image", "model", "permissions", "secrets"}

// parseAgent reads one agent under agents, of name, whose name is on line, and checks it as agents.ValidateDeclared
// does, naming the line of the field at fault.
func parseAgent(name string, line int, node *yaml.Node) (agents.Declared, error) {
	d := agents.Declared{Name: name, Line: line}
	if isNull(node) {
		node = &yaml.Node{Kind: yaml.MappingNode, Line: line}
	}
	if err := onlyKeys(node, "agent "+name, agentKeys...); err != nil {
		return d, err
	}
	if err := agentFields(&d, node); err != nil {
		return d, err
	}
	checked, err := agents.ValidateDeclared(d)
	var verr *orgs.ValidationError
	if errors.As(err, &verr) {
		at := line
		if n := valueOf(node, verr.Field); n != nil {
			at = n.Line
		}
		return d, &SettingsError{Line: at, Message: "agent " + name + ": " + verr.Message}
	}
	if err != nil {
		return d, fmt.Errorf("checking agent %s: %w", name, err)
	}
	return checked, nil
}

// agentFields reads the fields of one agent under agents, node, into d, as written, for the domain to check.
func agentFields(d *agents.Declared, node *yaml.Node) error {
	var err error
	for _, f := range []struct {
		key  string
		into *string
	}{{"description", &d.Description}, {"instructions", &d.Instructions}, {"image", &d.Image}} {
		if n := valueOf(node, f.key); n != nil && !isNull(n) {
			if *f.into, err = stringOf(n, f.key+" must be text"); err != nil {
				return err
			}
		}
	}
	if n := valueOf(node, "model"); n != nil && !isNull(n) {
		model, err := stringOf(n, "model must name provider/model")
		if err == nil {
			d.Provider, d.Model, err = agents.SplitModel(model)
		}
		if err != nil {
			return settingsError(n.Line, err)
		}
	}
	if n := valueOf(node, "permissions"); n != nil && !isNull(n) {
		if d.Permissions, err = permissionNames(n); err != nil {
			return err
		}
	}
	d.Secrets, err = parseSecrets(valueOf(node, "secrets"))
	return err
}

// permissionNames reads a list of permissions' names, as written, for the domain to check.
func permissionNames(node *yaml.Node) ([]string, error) {
	if node.Kind != yaml.SequenceNode {
		return nil, &SettingsError{Line: node.Line, Message: "permissions must be a list of repository permissions"}
	}
	out := make([]string, 0, len(node.Content))
	for _, n := range node.Content {
		if n.Kind != yaml.ScalarNode || n.Tag != "!!str" {
			return nil, &SettingsError{Line: n.Line, Message: "permissions must be a list of repository permissions"}
		}
		out = append(out, n.Value)
	}
	return out, nil
}

// settingsError is err, a validation error, as a *SettingsError at line.
func settingsError(line int, err error) error {
	var verr *orgs.ValidationError
	if errors.As(err, &verr) {
		return &SettingsError{Line: line, Message: verr.Message}
	}
	var serr *SettingsError
	if errors.As(err, &serr) {
		return err
	}
	return &SettingsError{Line: line, Message: err.Error()}
}
