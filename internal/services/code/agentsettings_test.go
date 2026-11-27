package code_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/agents"
	"cobranch.dev/internal/services/code"
)

func TestParseSettingsAgents(t *testing.T) {
	designs := `agents:
  site-checker:
    description: Checks the site's pages and links
    instructions: |
      Run the link checker.
    image: ghcr.io/acme/site-tools:1.4
    model: anthropic/claude-opus-5-5
    permissions: [repo.read, messages.post, annotations.write]
    secrets:
      SITE_TOKEN: ` + secretID + "\n"
	tests := []struct {
		name     string
		settings string
		want     []agents.Declared
	}{
		{name: "no agents", settings: "jobs:\n", want: []agents.Declared{}},
		{name: "agents of none", settings: "agents:\n", want: []agents.Declared{}},
		{name: "the design's example", settings: designs, want: []agents.Declared{{Name: "site-checker",
			Description: "Checks the site's pages and links", Instructions: "Run the link checker.", Image: "ghcr.io/acme/site-tools:1.4",
			Provider: "anthropic", Model: "claude-opus-5-5", Permissions: []string{"annotations.write", "messages.post", "repo.read"},
			Secrets: map[string]uuid.UUID{"SITE_TOKEN": uuid.MustParse(secretID)}, Line: 2}}},
		{name: "an agent of nothing but its name, by name", settings: "agents:\n  writer:\n  checker: {}\n", want: []agents.Declared{
			{Name: "checker", Permissions: []string{"messages.post", "repo.read"}, Line: 3},
			{Name: "writer", Permissions: []string{"messages.post", "repo.read"}, Line: 2}}},
		{name: "no permissions", settings: "agents:\n  quiet:\n    permissions: []\n", want: []agents.Declared{
			{Name: "quiet", Permissions: []string{}, Line: 2}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := code.ParseSettings([]byte(tt.settings))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got.Agents, tt.want) {
				t.Errorf("ParseSettings().Agents = %+v, want %+v", got.Agents, tt.want)
			}
		})
	}
}

func TestParseSettingsAgentsRefused(t *testing.T) {
	tests := []struct {
		name     string
		settings string
		wantErr  string
	}{
		{name: "agents as a list", settings: "agents: [checker]\n", wantErr: "/cobranch.yaml: line 1: agents must map agent names to their agents"},
		{name: "a bad name", settings: "agents:\n  Site Checker:\n",
			wantErr: "/cobranch.yaml: line 2: an agent's name must be 1 to 100 lowercase letters, digits, '-', '_', '.' and '/', starting with a letter or digit"},
		{name: "a name twice", settings: "agents:\n  checker:\n  checker:\n", wantErr: `/cobranch.yaml: line 3: agents names "checker" twice`},
		{name: "a misspelt key", settings: "agents:\n  checker:\n    images: alpine\n", wantErr: `/cobranch.yaml: line 3: agent checker takes no "images"`},
		{name: "an agent's job key", settings: "agents:\n  checker:\n    script: make\n", wantErr: `/cobranch.yaml: line 3: agent checker takes no "script"`},
		{name: "a bad image", settings: "agents:\n  checker:\n\n    image: Not An Image\n",
			wantErr: "/cobranch.yaml: line 4: agent checker: an image is a reference such as ghcr.io/acme/tools:1.4, of at most 512 characters, or empty for the installation's"},
		{name: "a model without its provider", settings: "agents:\n  checker:\n    model: claude-opus-5-5\n",
			wantErr: "/cobranch.yaml: line 3: a model is named as provider/model, such as anthropic/claude-opus-5-5"},
		{name: "a model as a number", settings: "agents:\n  checker:\n    model: 5\n", wantErr: "/cobranch.yaml: line 3: model must name provider/model"},
		{name: "a description as a list", settings: "agents:\n  checker:\n    description: [a]\n", wantErr: "/cobranch.yaml: line 3: description must be text"},
		{name: "an unknown permission", settings: "agents:\n  checker:\n    permissions: [repo.write]\n",
			wantErr: `/cobranch.yaml: line 3: agent checker: "repo.write" is no repository permission`},
		{name: "a secret's permission", settings: "agents:\n  checker:\n    permissions: [secrets.read]\n",
			wantErr: `/cobranch.yaml: line 3: agent checker: "secrets.read" is no permission a repository agent declares: it reads a secret once granted Secret reader on it`},
		{name: "an artifact's permission", settings: "agents:\n  checker:\n    permissions: [artifacts.read]\n",
			wantErr: `/cobranch.yaml: line 3: agent checker: "artifacts.read" is no permission a repository agent declares: grant it a role on the repository for artifacts`},
		{name: "a permission twice", settings: "agents:\n  checker:\n    permissions: [repo.read, repo.read]\n",
			wantErr: "/cobranch.yaml: line 3: agent checker: permission repo.read is listed twice"},
		{name: "permissions as a name", settings: "agents:\n  checker:\n    permissions: repo.read\n",
			wantErr: "/cobranch.yaml: line 3: permissions must be a list of repository permissions"},
		{name: "a secret the harness sets", settings: "agents:\n  checker:\n    secrets:\n      GIT_TOKEN: " + secretID + "\n",
			wantErr: `/cobranch.yaml: line 4: agent checker: "GIT_TOKEN" is an environment variable the harness sets itself`},
		{name: "a secret by name", settings: "agents:\n  checker:\n    secrets:\n      TOKEN: site-token\n",
			wantErr: "/cobranch.yaml: line 4: a secret is named by its ID"},
		{name: "a long description", settings: "agents:\n  checker:\n    description: " + longText(1001) + "\n",
			wantErr: "/cobranch.yaml: line 3: agent checker: an agent's description is at most 1000 characters"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := code.ParseSettings([]byte(tt.settings))
			var bad *code.SettingsError
			if !errors.As(err, &bad) || err.Error() != tt.wantErr {
				t.Fatalf("ParseSettings() = %v, want *SettingsError %q", err, tt.wantErr)
			}
		})
	}
}

// longText is n letters.
func longText(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = 'a'
	}
	return string(b)
}
