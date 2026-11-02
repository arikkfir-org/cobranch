package agents

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"

	"cobranch.dev/internal/services/orgs"
	"cobranch.dev/internal/services/secrets"
)

// ModelProvider is a model provider an organization added: one the installation knows, by its name, and the
// organization secret that holds its API key, which an agent on one of its models must read for its tasks to run.
type ModelProvider struct {
	OrgID uuid.UUID
	// Name is the provider's name, one of those the installation knows.
	Name string
	// SecretID is the organization secret that holds its API key; uuid.Nil once that secret is deleted.
	SecretID uuid.UUID
	// SecretName is that secret's name, shown to a viewer holding secrets.manage at the organization; empty otherwise.
	SecretName string
	// CreatedBy is who added it; nil once they are gone.
	CreatedBy *Principal
	CreatedAt time.Time
	UpdatedAt time.Time
	// Version counts its changes from 1: it is its ETag.
	Version int64
}

// Broken reports whether the provider has lost its secret, so the tasks of agents on it can't run.
func (p ModelProvider) Broken() bool {
	return p.SecretID == uuid.Nil
}

// NoVersion adds a provider only where the organization has none of that name (If-None-Match: *).
const NoVersion int64 = 0

// Limits of an agent's model and secrets.
const (
	MaxModel   = 200
	MaxSecrets = 50
	MaxEnvName = 100
)

// The model and secrets' own errors.
var (
	// ErrProviderInUse is a provider removed while agents run on it.
	ErrProviderInUse = errors.New("agents run on this provider")
)

// envName is the name of an environment variable a secret goes in.
var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// reservedEnv are the environment variables, and the prefixes of those, that the harness sets for its tools itself.
var reservedEnv = []string{"COBRANCH_", "GIT_", "LD_", "PATH", "HOME"}

// ValidateModel normalizes an agent's provider and model, or refuses them: both or neither, the model 1 to 200
// characters on one line.
func ValidateModel(provider, model string) (string, string, error) {
	provider, model = strings.TrimSpace(provider), strings.TrimSpace(model)
	switch {
	case (provider == "") != (model == ""):
		return provider, model, invalid("model", "agent.model_invalid", "an agent runs on a provider's model, both named, or on none")
	case utf8.RuneCountInString(model) > MaxModel || strings.IndexFunc(model, unicode.IsControl) >= 0:
		return provider, model, invalid("model", "agent.model_invalid", "a model's name is 1 to 200 characters on one line")
	}
	return provider, model, nil
}

// ValidateSecrets refuses an agent's secrets unless there are at most 50, each named as an environment variable the
// harness doesn't set itself.
func ValidateSecrets(named map[string]uuid.UUID) error {
	if len(named) > MaxSecrets {
		return invalid("secrets", "agent.secrets_invalid", "an agent receives at most 50 secrets")
	}
	for name, id := range named {
		upper := strings.ToUpper(name)
		reserved := slices.ContainsFunc(reservedEnv, func(r string) bool {
			return upper == r || (strings.HasSuffix(r, "_") && strings.HasPrefix(upper, r))
		})
		switch {
		case len(name) > MaxEnvName || !envName.MatchString(name):
			return invalid("secrets", "agent.secrets_invalid", fmt.Sprintf("%q isn't the name of an environment variable", name))
		case reserved:
			return invalid("secrets", "agent.secrets_invalid", fmt.Sprintf("%q is an environment variable the harness sets itself", name))
		case id == uuid.Nil:
			return invalid("secrets", "agent.secrets_invalid", fmt.Sprintf("%q names no secret", name))
		}
	}
	return nil
}

// WithProviders has s take the providers of names, those the installation has adapters for, and no others.
func (s *Service) WithProviders(names ...string) *Service {
	s.providers = slices.Sorted(slices.Values(names))
	return s
}

// ProviderNames lists the providers the installation knows, by name.
func (s *Service) ProviderNames() []string {
	return slices.Clone(s.providers)
}

// shown leaves out the names of the providers' secrets unless the viewer, who holds held at the organization, manages
// secrets there.
func shown(held []string, list ...ModelProvider) []ModelProvider {
	if !slices.Contains(held, secrets.PermManage) {
		for i := range list {
			list[i].SecretName = ""
		}
	}
	return list
}

// ModelProviders lists the providers the organization added, by name; it takes org.read.
func (s *Service) ModelProviders(ctx context.Context, v Viewer, orgID uuid.UUID) ([]ModelProvider, error) {
	ctx, span := tracer.Start(ctx, "agents.Service.ModelProviders", onOrg(orgID))
	defer span.End()
	held, err := s.held(ctx, v, orgID)
	if err != nil {
		return nil, failed(span, "listing the model providers", err)
	}
	list, err := s.store.ModelProviders(ctx, orgID)
	if err != nil {
		return nil, failed(span, "listing the model providers", err)
	}
	return shown(held, list...), nil
}

// ModelProvider finds a provider the organization added, by name; it takes org.read.
func (s *Service) ModelProvider(ctx context.Context, v Viewer, orgID uuid.UUID, name string) (ModelProvider, error) {
	ctx, span := tracer.Start(ctx, "agents.Service.ModelProvider", onOrg(orgID, attribute.String("cobranch.model_provider", name)))
	defer span.End()
	held, err := s.held(ctx, v, orgID)
	if err != nil {
		return ModelProvider{}, failed(span, "finding the model provider", err)
	}
	p, err := s.store.ModelProvider(ctx, orgID, name)
	if err != nil {
		return ModelProvider{}, failed(span, "finding the model provider", err)
	}
	return shown(held, p)[0], nil
}

// SetModelProvider adds a provider to the organization, or names another secret for the one it has, with the
// organization's own secret that holds its API key, and answers it and whether it added it. It takes org.manage. A
// version of NoVersion only adds one, AnyVersion changes the one there, and any other changes it at that version:
// ErrChanged otherwise.
func (s *Service) SetModelProvider(ctx context.Context, v Viewer, orgID uuid.UUID, name string, secretID uuid.UUID, version int64) (ModelProvider, bool, error) {
	ctx, span := tracer.Start(ctx, "agents.Service.SetModelProvider", onOrg(orgID, attribute.String("cobranch.model_provider", name)))
	defer span.End()
	held, err := s.require(ctx, v, orgID, orgs.PermManage)
	if err != nil {
		return ModelProvider{}, false, failed(span, "setting the model provider", err)
	}
	if !slices.Contains(s.providers, name) {
		return ModelProvider{}, false, failed(span, "setting the model provider", orgs.ErrNotFound)
	}
	repoID, err := s.store.SecretRepository(ctx, orgID, secretID)
	if errors.Is(err, orgs.ErrNotFound) || (err == nil && repoID != uuid.Nil) {
		err = invalid("secretId", "model_provider.secret_invalid", "a provider's key is one of the organization's own secrets")
	}
	if err != nil {
		return ModelProvider{}, false, failed(span, "setting the model provider", err)
	}
	p, created, err := s.store.SetModelProvider(ctx, v.ID, orgID, name, secretID, version)
	if err != nil {
		return ModelProvider{}, false, failed(span, "setting the model provider", err)
	}
	return shown(held, p)[0], created, nil
}

// RemoveModelProvider removes a provider from the organization, at version or at any version for AnyVersion; it takes
// org.manage, and answers ErrProviderInUse while agents run on it.
func (s *Service) RemoveModelProvider(ctx context.Context, v Viewer, orgID uuid.UUID, name string, version int64) error {
	ctx, span := tracer.Start(ctx, "agents.Service.RemoveModelProvider", onOrg(orgID, attribute.String("cobranch.model_provider", name)))
	defer span.End()
	if _, err := s.require(ctx, v, orgID, orgs.PermManage); err != nil {
		return failed(span, "removing the model provider", err)
	}
	return failed(span, "removing the model provider", s.store.RemoveModelProvider(ctx, v.ID, orgID, name, version))
}

// SetModel puts one of the organization's agents on a model of a provider the organization added, or on none with
// both empty, at version or at any version for AnyVersion, and answers it. It takes org.principals.manage, and a
// person: an agent picks no model.
func (s *Service) SetModel(ctx context.Context, v Viewer, orgID, id uuid.UUID, provider, model string, version int64) (Agent, error) {
	ctx, span := tracer.Start(ctx, "agents.Service.SetModel", onOrg(orgID, attribute.String("cobranch.agent.id", id.String())))
	defer span.End()
	if err := person(v, "picks no model"); err != nil {
		return Agent{}, failed(span, "picking the agent's model", err)
	}
	if _, err := s.require(ctx, v, orgID, orgs.PermPrincipalsManage); err != nil {
		return Agent{}, failed(span, "picking the agent's model", err)
	}
	provider, model, err := ValidateModel(provider, model)
	if err != nil {
		return Agent{}, failed(span, "picking the agent's model", err)
	}
	a, err := s.store.SetModel(ctx, v.ID, orgID, id, version, provider, model)
	return a, failed(span, "picking the agent's model", err)
}

// SetSecrets names the secrets the tasks of one of the organization's agents receive, each an environment variable
// from one of the organization's secrets, at version or at any version for AnyVersion, and answers the agent. It
// takes org.principals.manage, and a person. A task receives only those its agent holds secrets.read on.
func (s *Service) SetSecrets(ctx context.Context, v Viewer, orgID, id uuid.UUID, named map[string]uuid.UUID, version int64) (Agent, error) {
	ctx, span := tracer.Start(ctx, "agents.Service.SetSecrets", onOrg(orgID, attribute.String("cobranch.agent.id", id.String())))
	defer span.End()
	if err := person(v, "names no secrets"); err != nil {
		return Agent{}, failed(span, "naming the agent's secrets", err)
	}
	if _, err := s.require(ctx, v, orgID, orgs.PermPrincipalsManage); err != nil {
		return Agent{}, failed(span, "naming the agent's secrets", err)
	}
	if err := ValidateSecrets(named); err != nil {
		return Agent{}, failed(span, "naming the agent's secrets", err)
	}
	for name, secretID := range named {
		_, err := s.store.SecretRepository(ctx, orgID, secretID)
		if errors.Is(err, orgs.ErrNotFound) {
			err = invalid("secrets", "agent.secrets_invalid", fmt.Sprintf("%q names none of the organization's secrets", name))
		}
		if err != nil {
			return Agent{}, failed(span, "naming the agent's secrets", err)
		}
	}
	a, err := s.store.SetSecrets(ctx, v.ID, orgID, id, version, named)
	return a, failed(span, "naming the agent's secrets", err)
}
