package agents

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/orgs"
	"cobranch.dev/internal/services/repos"
)

var tracer = otel.Tracer("cobranch.dev/internal/services/agents")

// Store keeps the agents and their tasks. Lookups of what isn't there answer orgs.ErrNotFound.
type Store interface {
	// Roles lists the roles the organization grants at a scope, the organization or its repositories, the built-in ones
	// first.
	Roles(ctx context.Context, orgID uuid.UUID, scope auth.Scope) ([]orgs.Role, error)
	// Repository finds one of the organization's repositories by ID and answers its slug.
	Repository(ctx context.Context, orgID, repoID uuid.UUID) (string, error)
	// Agents lists the organization's agents by name, each with its grants.
	Agents(ctx context.Context, orgID uuid.UUID) ([]Agent, error)
	// Agent finds one of the organization's agents with its grants.
	Agent(ctx context.Context, orgID, id uuid.UUID) (Agent, error)
	// CreateAgent records the agent, its principal and its grants for actor; ErrNameTaken for a name taken.
	CreateAgent(ctx context.Context, actor uuid.UUID, a Agent) error
	// UpdateAgent changes one of the organization's agents for actor, at version or at any version for AnyVersion, and
	// answers it; ErrChanged, or ErrNameTaken for a name taken.
	UpdateAgent(ctx context.Context, actor, orgID, id uuid.UUID, version int64, c Change) (Agent, error)
	// DeleteAgent deletes one of the organization's agents for actor, with its principal, grants and tasks, at version
	// or at any version for AnyVersion; ErrChanged.
	DeleteAgent(ctx context.Context, actor, orgID, id uuid.UUID, version int64) error
	// Branch finds one of the repository's live branches, by ID or else by name, and answers both.
	Branch(ctx context.Context, repoID, id uuid.UUID, name string) (uuid.UUID, string, error)
	// ThreadBranch answers the branch a chat thread belongs to; uuid.Nil for one of an issue's chat, which belongs to none.
	ThreadBranch(ctx context.Context, threadID uuid.UUID) (uuid.UUID, error)
	// Tasks lists a page of at most limit of the organization's tasks, newest first, that match the filter, either
	// name no repository or one of readable, and are of none of the kinds in hidden.
	Tasks(ctx context.Context, orgID uuid.UUID, f TaskFilter, readable []uuid.UUID, hidden []string, limit int) ([]Task, error)
	// Task finds one of the organization's tasks by its number.
	Task(ctx context.Context, orgID uuid.UUID, number int64) (Task, error)
	// TaskByID finds a task by its ID, with its organization's slug, its agent's standing instructions and email, and
	// its issue's number.
	TaskByID(ctx context.Context, id uuid.UUID) (Work, error)
	// Assign numbers the task in its organization and records it for actor, Queued, and answers it.
	Assign(ctx context.Context, actor uuid.UUID, t Task) (Task, error)
	// Cancel cancels one of the organization's active tasks for actor, at version or at any version for AnyVersion,
	// and answers it; ErrEnded once it has ended, ErrChanged.
	Cancel(ctx context.Context, actor, orgID uuid.UUID, number, version int64) (Task, error)
	// ModelProviders lists the providers the organization added, by name.
	ModelProviders(ctx context.Context, orgID uuid.UUID) ([]ModelProvider, error)
	// ModelProvider finds a provider the organization added, by name.
	ModelProvider(ctx context.Context, orgID uuid.UUID, name string) (ModelProvider, error)
	// SetModelProvider adds the provider for actor with its secret, or names another secret for the one there, as
	// Service.SetModelProvider's version says, and answers it and whether it added it; ErrChanged.
	SetModelProvider(ctx context.Context, actor, orgID uuid.UUID, name string, secretID uuid.UUID, version int64) (ModelProvider, bool, error)
	// RemoveModelProvider removes the provider for actor, at version or at any version for AnyVersion; ErrChanged,
	// ErrProviderInUse while agents run on it.
	RemoveModelProvider(ctx context.Context, actor, orgID uuid.UUID, name string, version int64) error
	// SecretRepository finds one of the organization's secrets and answers the repository holding it: uuid.Nil for
	// the organization's own.
	SecretRepository(ctx context.Context, orgID, secretID uuid.UUID) (uuid.UUID, error)
	// SetModel puts one of the organization's agents on a model of a provider it added, or on none with both empty,
	// for actor, at version or at any version for AnyVersion, and answers it; ErrChanged, and a validation error for a
	// provider the organization hasn't added.
	SetModel(ctx context.Context, actor, orgID, id uuid.UUID, version int64, provider, model string) (Agent, error)
	// SetSecrets names the secrets one of the organization's agents' tasks receive, for actor, at version or at any
	// version for AnyVersion, and answers it; ErrChanged.
	SetSecrets(ctx context.Context, actor, orgID, id uuid.UUID, version int64, named map[string]uuid.UUID) (Agent, error)
	// Models lists the organization's models by provider and name, each with the agents on it.
	Models(ctx context.Context, orgID uuid.UUID) ([]Model, error)
	// Model finds one of the organization's models, with the agents on it.
	Model(ctx context.Context, orgID uuid.UUID, provider, name string) (Model, error)
	// PutModel adds the model for actor, or changes the one there, as Service.PutModel's version says, and answers it and
	// whether it added it; ErrChanged, and a validation error for a provider the organization hasn't added.
	PutModel(ctx context.Context, actor, orgID uuid.UUID, provider, name string, in ModelInput, version int64) (Model, bool, error)
	// RemoveModel removes the model for actor, at version or at any version for AnyVersion; ErrChanged, ErrModelInUse
	// while agents run on it.
	RemoveModel(ctx context.Context, actor, orgID uuid.UUID, provider, name string, version int64) error
	// RecordSpend records a call of the task's model, priced at its organization's prices, unless the task has one of
	// that ID, and answers what the task spent; it ends a task that reaches its cap Failed. ErrEnded for a task that has
	// ended, orgs.ErrNotFound for none.
	RecordSpend(ctx context.Context, taskID uuid.UUID, u Usage) (Spend, error)
	// Spend reports what the organization's agents spent from since until until, as Report says, naming as an agent's
	// costliest task only one the viewer v describes sees.
	Spend(ctx context.Context, orgID uuid.UUID, since, until time.Time, v Shown) (Report, error)
	// Entries lists at most limit of a task's transcript entries numbered above after, in order.
	Entries(ctx context.Context, taskID uuid.UUID, after, limit int) ([]Entry, error)
	// Append records entry e of a task for actor, the next of its transcript, and, given change, moves the task to
	// change.State from one of change.From(), in one transaction; it answers the entry and whether it recorded it, or
	// the entry already numbered e.Seq unchanged. ErrEnded once the task has ended, ErrTransition, and a validation
	// error for a number past the next.
	Append(ctx context.Context, actor, taskID uuid.UUID, e Entry, change *StateChange) (Entry, bool, error)
}

// Service keeps an organization's agents and tasks for the viewers who read the organization: every operation checks
// org.read through the authorizer first, and answers orgs.ErrNotFound without it, as for an organization that isn't
// there. A task that names a repository is the same to a viewer who doesn't read the repository, and a task of a
// guarded kind to a viewer who lacks the kind's permission (Guard).
type Service struct {
	store Store
	authz *auth.Authorizer
	// providers are the names of the model providers the installation knows, sorted.
	providers []string
	guards    map[string]string
}

// NewService returns a Service over store that checks permissions with authz.
func NewService(store Store, authz *auth.Authorizer) *Service {
	return &Service{store: store, authz: authz, guards: map[string]string{}}
}

// Guard has the service show a task of kind only to viewers who hold permission at its organization, beside repo.read
// on the repository it names. A slice whose tasks carry what one of its permissions guards, such as an issue's title
// and description, guards their kind with it, so the tasks show no more than the slice does. Call it while wiring,
// before the service serves.
func (s *Service) Guard(kind, permission string) {
	s.guards[kind] = permission
}

// hidden lists the guarded kinds whose permission held lacks, in order.
func (s *Service) hidden(held []string) []string {
	out := []string{}
	for kind, permission := range s.guards {
		if !slices.Contains(held, permission) {
			out = append(out, kind)
		}
	}
	slices.Sort(out)
	return out
}

// held returns what v holds at the organization, or orgs.ErrNotFound without org.read.
func (s *Service) held(ctx context.Context, v Viewer, orgID uuid.UUID) ([]string, error) {
	held, err := s.authz.Holds(ctx, v.ID, auth.ScopeOrganization, orgID)
	if err != nil {
		return nil, fmt.Errorf("reading the viewer's permissions: %w", err)
	}
	if !slices.Contains(held, orgs.PermRead) {
		return nil, orgs.ErrNotFound
	}
	return held, nil
}

// require returns what v holds at the organization, orgs.ErrNotFound without org.read and auth.ErrForbidden without
// permission.
func (s *Service) require(ctx context.Context, v Viewer, orgID uuid.UUID, permission string) ([]string, error) {
	held, err := s.held(ctx, v, orgID)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(held, permission) {
		return nil, fmt.Errorf("%w: %s at the organization", auth.ErrForbidden, permission)
	}
	return held, nil
}

// person refuses an agent acting as itself: an agent's task acts within its assigner's permissions, and an agent that
// made agents or assigned tasks would give them its own instead.
func person(v Viewer, what string) error {
	if v.Kind == auth.KindAgent {
		return fmt.Errorf("%w: an agent %s; a person does", auth.ErrForbidden, what)
	}
	return nil
}

// reads reports whether v reads the repository, which takes repo.read there.
func (s *Service) reads(ctx context.Context, v Viewer, repoID uuid.UUID) (bool, error) {
	held, err := s.authz.Holds(ctx, v.ID, auth.ScopeRepository, repoID)
	if err != nil {
		return false, fmt.Errorf("reading the viewer's permissions on the repository: %w", err)
	}
	return slices.Contains(held, repos.PermRead), nil
}

// onOrg gives the span of an operation on the organization's agents or tasks the organization's ID and attrs.
func onOrg(orgID uuid.UUID, attrs ...attribute.KeyValue) trace.SpanStartOption {
	return trace.WithAttributes(append(attrs, attribute.String("cobranch.organization.id", orgID.String()))...)
}

// failed records err on span, wrapped with what the operation was doing unless it is one of the domain's own, and
// returns it.
func failed(span trace.Span, what string, err error) error {
	if err == nil {
		return nil
	}
	err = wrap(what, err)
	span.RecordError(err)
	span.SetStatus(codes.Error, err.Error())
	return err
}

// wrap keeps the domain's own errors as they are and gives any other what's context.
func wrap(what string, err error) error {
	var verr *orgs.ValidationError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &verr), errors.Is(err, orgs.ErrNotFound), errors.Is(err, auth.ErrForbidden), errors.Is(err, ErrNameTaken),
		errors.Is(err, ErrBuiltIn), errors.Is(err, ErrEnded), errors.Is(err, ErrChanged), errors.Is(err, ErrProviderInUse),
		errors.Is(err, ErrModelInUse):
		return err
	}
	return fmt.Errorf("%s: %w", what, err)
}

// Agents lists the organization's agents by name, each with its grants; it takes org.read.
func (s *Service) Agents(ctx context.Context, v Viewer, orgID uuid.UUID) ([]Agent, error) {
	ctx, span := tracer.Start(ctx, "agents.Service.Agents", onOrg(orgID))
	defer span.End()
	if _, err := s.held(ctx, v, orgID); err != nil {
		return nil, failed(span, "listing the agents", err)
	}
	list, err := s.store.Agents(ctx, orgID)
	return list, failed(span, "listing the agents", err)
}

// Agent finds one of the organization's agents with its grants; it takes org.read.
func (s *Service) Agent(ctx context.Context, v Viewer, orgID, id uuid.UUID) (Agent, error) {
	ctx, span := tracer.Start(ctx, "agents.Service.Agent", onOrg(orgID, attribute.String("cobranch.agent.id", id.String())))
	defer span.End()
	if _, err := s.held(ctx, v, orgID); err != nil {
		return Agent{}, failed(span, "finding the agent", err)
	}
	a, err := s.store.Agent(ctx, orgID, id)
	return a, failed(span, "finding the agent", err)
}

// roles holds the roles an organization grants at each scope, read once each.
type roles struct {
	store Store
	orgID uuid.UUID
	at    map[auth.Scope][]orgs.Role
}

// find finds the role of id the organization grants where a grant on repoID applies: at the organization for
// uuid.Nil, and otherwise on its repositories.
func (r *roles) find(ctx context.Context, id, repoID uuid.UUID) (orgs.Role, bool, error) {
	scope := auth.ScopeOrganization
	if repoID != uuid.Nil {
		scope = auth.ScopeRepository
	}
	list, ok := r.at[scope]
	if !ok {
		var err error
		if list, err = r.store.Roles(ctx, r.orgID, scope); err != nil {
			return orgs.Role{}, false, fmt.Errorf("listing the roles: %w", err)
		}
		r.at[scope] = list
	}
	i := slices.IndexFunc(list, func(role orgs.Role) bool { return role.ID == id })
	if i < 0 {
		return orgs.Role{}, false, nil
	}
	return list[i], true, nil
}

// grant checks one of a new agent's first grants: a role the organization grants where it applies, which v, holding
// atOrg at the organization, holds every permission of; on a repository, the role's repository permissions, on one
// of the organization's repositories v reads. It answers the grant with its names.
func (s *Service) grant(ctx context.Context, v Viewer, orgID uuid.UUID, known *roles, atOrg []string, g Grant) (Grant, error) {
	role, ok, err := known.find(ctx, g.RoleID, g.RepoID)
	if err != nil {
		return g, err
	}
	if !ok {
		return g, invalid("grants", "role.invalid", "not a role this organization grants there")
	}
	g.RoleName = role.Name
	if g.RepoID == uuid.Nil {
		if !auth.Covers(atOrg, role.Permissions) {
			return g, fmt.Errorf("%w: giving an agent %s takes every permission of the role", auth.ErrForbidden, role.Name)
		}
		return g, nil
	}
	slug, err := s.store.Repository(ctx, orgID, g.RepoID)
	if errors.Is(err, orgs.ErrNotFound) {
		return g, invalid("grants", "repository.invalid", "not a repository of this organization")
	}
	if err != nil {
		return g, fmt.Errorf("finding the repository: %w", err)
	}
	g.RepoSlug = slug
	onRepo, err := s.authz.Holds(ctx, v.ID, auth.ScopeRepository, g.RepoID)
	if err != nil {
		return g, fmt.Errorf("reading the viewer's permissions on the repository: %w", err)
	}
	permissions := repos.RepositoryPermissions(role.Permissions)
	switch {
	case !slices.Contains(onRepo, repos.PermRead):
		return g, invalid("grants", "repository.invalid", "not a repository of this organization")
	case len(permissions) == 0:
		return g, invalid("grants", "role.invalid", "not a role this repository grants")
	case !auth.Covers(onRepo, permissions):
		return g, fmt.Errorf("%w: giving an agent %s on %s takes every permission it gives there", auth.ErrForbidden, role.Name, slug)
	}
	return g, nil
}

// CreateAgent makes an agent of the organization with in's first grants, each a role whose permissions v holds where
// it applies; it takes agents.create, and no org.grants.manage. An agent makes no agents.
func (s *Service) CreateAgent(ctx context.Context, v Viewer, orgID uuid.UUID, in Input) (Agent, error) {
	ctx, span := tracer.Start(ctx, "agents.Service.CreateAgent", onOrg(orgID))
	defer span.End()
	if err := person(v, "makes no agents"); err != nil {
		return Agent{}, failed(span, "creating the agent", err)
	}
	atOrg, err := s.require(ctx, v, orgID, PermCreate)
	if err != nil {
		return Agent{}, failed(span, "creating the agent", err)
	}
	if in, err = ValidateInput(in); err != nil {
		return Agent{}, failed(span, "creating the agent", err)
	}
	known := &roles{store: s.store, orgID: orgID, at: map[auth.Scope][]orgs.Role{}}
	a := Agent{OrgID: orgID, Name: in.Name, Description: in.Description, Instructions: in.Instructions,
		CreatedBy: &Principal{ID: v.ID, DisplayName: v.DisplayName}, Version: 1}
	for _, g := range in.Grants {
		g, err := s.grant(ctx, v, orgID, known, atOrg, g)
		if err != nil {
			return Agent{}, failed(span, "checking the agent's grants", err)
		}
		a.Grants = append(a.Grants, g)
	}
	if a.ID, err = uuid.NewV7(); err != nil {
		return Agent{}, failed(span, "making the agent's ID", err)
	}
	span.SetAttributes(attribute.String("cobranch.agent.id", a.ID.String()))
	if err := s.store.CreateAgent(ctx, v.ID, a); err != nil {
		return Agent{}, failed(span, "recording the agent", err)
	}
	created, err := s.store.Agent(ctx, orgID, a.ID)
	return created, failed(span, "reading the agent", err)
}

// UpdateAgent changes one of the organization's agents from version, or from any version for AnyVersion; it takes
// being its creator, or org.principals.manage. Its cap is a budget, so changing it takes a person holding
// org.principals.manage, as picking its model does: neither its creator alone nor an agent, which a task's token acts as,
// lifts it.
func (s *Service) UpdateAgent(ctx context.Context, v Viewer, orgID, id uuid.UUID, version int64, c Change) (Agent, error) {
	ctx, span := tracer.Start(ctx, "agents.Service.UpdateAgent", onOrg(orgID, attribute.String("cobranch.agent.id", id.String())))
	defer span.End()
	if c.Cap != nil {
		if err := person(v, "sets no cap"); err != nil {
			return Agent{}, failed(span, "changing the agent", err)
		}
	}
	held, err := s.held(ctx, v, orgID)
	if err != nil {
		return Agent{}, failed(span, "changing the agent", err)
	}
	a, err := s.store.Agent(ctx, orgID, id)
	if err != nil {
		return Agent{}, failed(span, "finding the agent", err)
	}
	manages := slices.Contains(held, orgs.PermPrincipalsManage)
	switch {
	case !manages && (a.CreatedBy == nil || a.CreatedBy.ID != v.ID):
		return Agent{}, failed(span, "changing the agent", fmt.Errorf("%w: changing another's agent takes %s", auth.ErrForbidden, orgs.PermPrincipalsManage))
	case !manages && c.Cap != nil:
		return Agent{}, failed(span, "changing the agent", fmt.Errorf("%w: setting an agent's cap takes %s", auth.ErrForbidden, orgs.PermPrincipalsManage))
	}
	if c, err = ValidateChange(c); err != nil {
		return Agent{}, failed(span, "changing the agent", err)
	}
	a, err = s.store.UpdateAgent(ctx, v.ID, orgID, id, version, c)
	return a, failed(span, "changing the agent", err)
}

// DeleteAgent deletes one of the organization's agents with its grants and tasks, from version, or from any version
// for AnyVersion; it takes org.principals.manage and every permission of the roles the agent holds where they apply,
// as removing a member does. A built-in agent is never deleted: ErrBuiltIn.
func (s *Service) DeleteAgent(ctx context.Context, v Viewer, orgID, id uuid.UUID, version int64) error {
	ctx, span := tracer.Start(ctx, "agents.Service.DeleteAgent", onOrg(orgID, attribute.String("cobranch.agent.id", id.String())))
	defer span.End()
	atOrg, err := s.require(ctx, v, orgID, orgs.PermPrincipalsManage)
	if err != nil {
		return failed(span, "deleting the agent", err)
	}
	a, err := s.store.Agent(ctx, orgID, id)
	if err != nil {
		return failed(span, "finding the agent", err)
	}
	if a.BuiltIn {
		return failed(span, "deleting the agent", ErrBuiltIn)
	}
	if err := s.coversGrants(ctx, v, orgID, atOrg, a.Grants); err != nil {
		return failed(span, "deleting the agent", err)
	}
	return failed(span, "deleting the agent", s.store.DeleteAgent(ctx, v.ID, orgID, id, version))
}

// coversGrants refuses grants v, holding atOrg at the organization, doesn't hold every permission of where they apply.
func (s *Service) coversGrants(ctx context.Context, v Viewer, orgID uuid.UUID, atOrg []string, grants []Grant) error {
	known := &roles{store: s.store, orgID: orgID, at: map[auth.Scope][]orgs.Role{}}
	for _, g := range grants {
		role, ok, err := known.find(ctx, g.RoleID, g.RepoID)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		held, permissions := atOrg, role.Permissions
		if g.RepoID != uuid.Nil {
			if held, err = s.authz.Holds(ctx, v.ID, auth.ScopeRepository, g.RepoID); err != nil {
				return fmt.Errorf("reading the viewer's permissions on the repository: %w", err)
			}
			permissions = repos.RepositoryPermissions(permissions)
		}
		if !auth.Covers(held, permissions) {
			return fmt.Errorf("%w: deleting an agent that holds %s takes every permission it gives", auth.ErrForbidden, g.RoleName)
		}
	}
	return nil
}
