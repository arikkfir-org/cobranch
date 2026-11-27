package agents_test

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/agents"
	"cobranch.dev/internal/services/orgs"
)

func TestValidateDeclared(t *testing.T) {
	t.Parallel()
	secret := uuid.New()
	tests := []struct {
		name string
		in   agents.Declared
		want agents.Declared
		err  string
	}{
		{name: "nothing but its name: read and post", in: agents.Declared{Name: "checker"},
			want: agents.Declared{Name: "checker", Permissions: []string{"messages.post", "repo.read"}}},
		{name: "everything, trimmed and sorted", in: agents.Declared{Name: "checker", Description: " Checks ", Instructions: " Run it ",
			Image: " python:3.14.0 ", Provider: "anthropic", Model: "claude-opus-5-5", Permissions: []string{"repo.read", "annotations.write"},
			Secrets: map[string]uuid.UUID{"TOKEN": secret}},
			want: agents.Declared{Name: "checker", Description: "Checks", Instructions: "Run it", Image: "python:3.14.0", Provider: "anthropic",
				Model: "claude-opus-5-5", Permissions: []string{"annotations.write", "repo.read"}, Secrets: map[string]uuid.UUID{"TOKEN": secret}}},
		{name: "no permissions", in: agents.Declared{Name: "checker", Permissions: []string{}},
			want: agents.Declared{Name: "checker", Permissions: []string{}}},
		{name: "an organization's permission", in: agents.Declared{Name: "checker", Permissions: []string{"org.manage"}}, err: "agent.permissions_invalid"},
		{name: "artifacts.create", in: agents.Declared{Name: "checker", Permissions: []string{"artifacts.create"}}, err: "agent.permissions_invalid"},
		{name: "secrets.manage", in: agents.Declared{Name: "checker", Permissions: []string{"secrets.manage"}}, err: "agent.permissions_invalid"},
		{name: "a permission twice", in: agents.Declared{Name: "checker", Permissions: []string{"repo.push", "repo.push"}}, err: "agent.permissions_invalid"},
		{name: "a bad image", in: agents.Declared{Name: "checker", Image: "UPPER CASE"}, err: "agent.image_invalid"},
		{name: "a model without a provider", in: agents.Declared{Name: "checker", Model: "opus"}, err: "agent.model_invalid"},
		{name: "a reserved secret", in: agents.Declared{Name: "checker", Secrets: map[string]uuid.UUID{"PATH": secret}}, err: "agent.secrets_invalid"},
		{name: "a long description", in: agents.Declared{Name: "checker", Description: strings.Repeat("a", 1001)}, err: "agent.description_invalid"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := agents.ValidateDeclared(tt.in)
			if tt.err != "" {
				if !is(err, invalid(tt.err)) {
					t.Fatalf("ValidateDeclared() = %v, want %s", err, tt.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ValidateDeclared() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestSplitModel(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in, provider, model string
		ok                  bool
	}{
		{in: "anthropic/claude-opus-5-5", provider: "anthropic", model: "claude-opus-5-5", ok: true},
		{in: " openrouter/meta/llama ", provider: "openrouter", model: "meta/llama", ok: true},
		{in: "claude-opus-5-5"},
		{in: "/claude"},
		{in: "anthropic/"},
	}
	for _, tt := range tests {
		provider, model, err := agents.SplitModel(tt.in)
		if (err == nil) != tt.ok || provider != tt.provider || model != tt.model {
			t.Errorf("SplitModel(%q) = %q, %q, %v", tt.in, provider, model, err)
		}
	}
}

// repoAgents adds checker, website's agent, vault's sealer, and website's retired agent to f.
func (f *fixture) repoAgents() (checker, sealer, retired agents.Agent) {
	gone := now
	checker = f.store.AddAgent(agents.Agent{OrgID: f.org, RepoID: f.website, RepoSlug: "website", Name: "checker", Permissions: []string{"repo.read"}})
	sealer = f.store.AddAgent(agents.Agent{OrgID: f.org, RepoID: f.vault, RepoSlug: "vault", Name: "sealer", Permissions: []string{"repo.read"}})
	retired = f.store.AddAgent(agents.Agent{OrgID: f.org, RepoID: f.website, RepoSlug: "website", Name: "old", RetiredAt: &gone})
	return checker, sealer, retired
}

func TestRepositoryAgentsShown(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	scout := f.agent(t, f.olive, "Scout")
	checker, sealer, retired := f.repoAgents()
	tests := []struct {
		name   string
		viewer agents.Viewer
		want   []uuid.UUID
	}{
		{name: "an owner reads every repository", viewer: f.olive, want: []uuid.UUID{checker.ID, retired.ID, scout.ID, sealer.ID}},
		{name: "a developer of website", viewer: f.mel, want: []uuid.UUID{checker.ID, retired.ID, scout.ID}},
		{name: "a guest reads no repository", viewer: f.gus, want: []uuid.UUID{scout.ID}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			list, err := f.service.Agents(context.Background(), tt.viewer, f.org)
			if err != nil {
				t.Fatal(err)
			}
			got := make([]uuid.UUID, 0, len(list))
			for _, a := range list {
				got = append(got, a.ID)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("Agents() = %v, want %v", got, tt.want)
			}
			for _, id := range []uuid.UUID{checker.ID, sealer.ID} {
				_, err := f.service.Agent(context.Background(), tt.viewer, f.org, id)
				if shown := slices.Contains(tt.want, id); shown != (err == nil) || (!shown && !is(err, orgs.ErrNotFound)) {
					t.Errorf("Agent(%s) = %v, want shown %v", id, err, shown)
				}
			}
		})
	}
}

func TestRepositoryAgentsReadOnly(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	checker, _, _ := f.repoAgents()
	ctx, name := context.Background(), "renamed"
	tests := []struct {
		name string
		call func() error
	}{
		{name: "update", call: func() error {
			_, err := f.service.UpdateAgent(ctx, f.olive, f.org, checker.ID, agents.AnyVersion, agents.Change{Name: &name})
			return fmt.Errorf("updating: %w", err)
		}},
		{name: "delete", call: func() error { return f.service.DeleteAgent(ctx, f.olive, f.org, checker.ID, agents.AnyVersion) }},
		{name: "model", call: func() error {
			_, err := f.service.SetModel(ctx, f.olive, f.org, checker.ID, "anthropic", "claude", agents.AnyVersion)
			return fmt.Errorf("setting the model: %w", err)
		}},
		{name: "image", call: func() error {
			_, err := f.service.SetImage(ctx, f.olive, f.org, checker.ID, "python:3.14.0", agents.AnyVersion)
			return fmt.Errorf("setting the image: %w", err)
		}},
		{name: "secrets", call: func() error {
			_, err := f.service.SetSecrets(ctx, f.olive, f.org, checker.ID, map[string]uuid.UUID{}, agents.AnyVersion)
			return fmt.Errorf("setting the secrets: %w", err)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.call(); !is(err, agents.ErrDeclared) {
				t.Errorf("%s = %v, want ErrDeclared", tt.name, err)
			}
		})
	}
}

func TestAssignRepositoryAgent(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	checker, sealer, retired := f.repoAgents()
	tests := []struct {
		name   string
		viewer agents.Viewer
		agent  agents.Agent
		repo   uuid.UUID
		want   error
	}{
		{name: "in its repository", viewer: f.mel, agent: checker, repo: f.website},
		{name: "nowhere", viewer: f.mel, agent: checker, want: invalid("agent.elsewhere")},
		{name: "in another repository", viewer: f.olive, agent: checker, repo: f.vault, want: invalid("agent.elsewhere")},
		{name: "in a repository the assigner doesn't read", viewer: f.mel, agent: sealer, repo: f.vault, want: invalid("repository.invalid")},
		{name: "retired", viewer: f.mel, agent: retired, repo: f.website, want: invalid("agent.retired")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			task, err := f.service.Assign(context.Background(), tt.viewer, f.org, tt.agent.ID, agents.TaskInput{Title: "Check", RepoID: tt.repo})
			if !is(err, tt.want) {
				t.Fatalf("Assign() = %v, want %v", err, tt.want)
			}
			if err == nil && (task.Agent.ID != tt.agent.ID || task.RepoID != tt.repo) {
				t.Errorf("Assign() = %+v", task)
			}
		})
	}
}
