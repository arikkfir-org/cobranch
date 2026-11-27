package agents

import (
	"errors"
	"slices"
	"strings"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/orgs"
)

// DefaultDeclaredPermissions are what a repository agent may do on its repository when main's /cobranch.yaml lists
// none: read it and post messages, sorted.
var DefaultDeclaredPermissions = []string{"messages.post", "repo.read"}

// Declared is a repository agent as main's /cobranch.yaml declares it under agents, by its name: what it is, the
// image its tasks run in (empty for the installation's), the model it runs on (none for the organization's default),
// what it may do on its repository and the secrets its tasks receive. Line is the line of its name in the file, which
// a refusal and the Console name.
type Declared struct {
	Name         string               `json:"name"`
	Description  string               `json:"description,omitempty"`
	Instructions string               `json:"instructions,omitempty"`
	Image        string               `json:"image,omitempty"`
	Provider     string               `json:"provider,omitempty"`
	Model        string               `json:"model,omitempty"`
	Permissions  []string             `json:"permissions"`
	Secrets      map[string]uuid.UUID `json:"secrets,omitempty"`
	Line         int                  `json:"line"`
}

// ValidateDeclared normalizes a repository agent's fields, or refuses one as an organization agent's are refused,
// naming the field: its description and instructions, its image, its model and its secrets. Its permissions are
// repository permissions, each once, sorted, and none of an artifact's or a secret's: it reads a secret once granted
// Secret reader on it, as any principal does. The name is the settings file's to check.
func ValidateDeclared(d Declared) (Declared, error) {
	in, err := ValidateInput(Input{Name: d.Name, Description: d.Description, Instructions: d.Instructions})
	if err != nil {
		return d, err
	}
	d.Description, d.Instructions = in.Description, in.Instructions
	if d.Image, err = ValidateImage(d.Image); err != nil {
		return d, err
	}
	if d.Provider, d.Model, err = ValidateModel(d.Provider, d.Model); err != nil {
		return d, err
	}
	if err := ValidateSecrets(d.Secrets); err != nil {
		return d, err
	}
	if d.Permissions, err = declaredPermissions(d.Permissions); err != nil {
		return d, err
	}
	return d, nil
}

// declaredPermissions sorts a repository agent's permissions, or refuses them: repository permissions, each once,
// none of an artifact's or a secret's.
func declaredPermissions(list []string) ([]string, error) {
	if list == nil {
		return slices.Clone(DefaultDeclaredPermissions), nil
	}
	out := make([]string, 0, len(list))
	for _, p := range list {
		switch {
		case !slices.Contains(auth.RepositoryPermissions, p):
			return nil, invalid("permissions", "agent.permissions_invalid", "\""+p+"\" is no repository permission")
		case p == "artifacts.create" || slices.Contains(auth.ArtifactPermissions, p):
			return nil, invalid("permissions", "agent.permissions_invalid",
				"\""+p+"\" is no permission a repository agent declares: grant it a role on the repository for artifacts")
		case slices.Contains(auth.SecretPermissions, p):
			return nil, invalid("permissions", "agent.permissions_invalid",
				"\""+p+"\" is no permission a repository agent declares: it reads a secret once granted Secret reader on it")
		case slices.Contains(out, p):
			return nil, invalid("permissions", "agent.permissions_invalid", "permission "+p+" is listed twice")
		}
		out = append(out, p)
	}
	slices.Sort(out)
	return out, nil
}

// SplitModel reads a model as a repository agent names it, provider/model, such as anthropic/claude-opus-5-5: the
// provider is everything before the first slash.
func SplitModel(s string) (string, string, error) {
	provider, model, ok := strings.Cut(strings.TrimSpace(s), "/")
	if !ok || provider == "" || model == "" {
		return "", "", invalid("model", "agent.model_invalid", "a model is named as provider/model, such as anthropic/claude-opus-5-5")
	}
	return provider, model, nil
}

// ErrDeclared is a change through the API to a repository agent, which its repository's main /cobranch.yaml defines.
var ErrDeclared = errors.New("a repository agent changes only in its repository's main /cobranch.yaml")

// ErrRetired is a task for a repository agent that main's /cobranch.yaml no longer declares.
var ErrRetired = &orgs.ValidationError{Field: "agentId", Code: "agent.retired",
	Message: "the agent is retired: its repository's main /cobranch.yaml no longer declares it"}
