package agents

import (
	"context"
	"errors"
	"fmt"
	"slices"

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
	// Roles lists the roles the organization grants, the built-in ones first.
	Roles(ctx context.Context, orgID uuid.UUID) ([]orgs.Role, error)
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
	// ThreadBranch answers the branch a chat thread belongs to.
	ThreadBranch(ctx context.Context, threadID uuid.UUID) (uuid.UUID, error)
	// Tasks lists a page of at most limit of the organization's tasks, newest first, that match the filter and either
	// name no repository or one of readable.
	Tasks(ctx context.Context, orgID uuid.UUID, f TaskFilter, readable []uuid.UUID, limit int) ([]Task, error)
	// Task finds one of the organization's tasks by its number.
	Task(ctx context.Context, orgID uuid.UUID, number int64) (Task, error)
	// Assign numbers the task in its organization and records it for actor, Queued, and answers it.
	Assign(ctx context.Context, actor uuid.UUID, t Task) (Task, error)
	// Cancel cancels one of the organization's active tasks for actor, at version or at any version for AnyVersion,
	// and answers it; ErrEnded once it has ended, ErrChanged.
	Cancel(ctx context.Context, actor, orgID uuid.UUID, number, version int64) (Task, error)
}

// Service keeps an organization's agents and tasks for the viewers who read the organization: every operation checks
// org.read through the authorizer first, and answers orgs.ErrNotFound without it, as for an organization that isn't
// there. A task that names a repository is the same to a viewer who doesn't read the repository.
type Service struct {
	store Store
	authz *auth.Authorizer
}

// NewService returns a Service over store that checks permissions with authz.
func NewService(store Store, authz *auth.Authorizer) *Service {
	return &Service{store: store, authz: authz}
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

// orgSpan starts a span of an operation on the organization's agents or tasks.
func orgSpan(ctx context.Context, name string, orgID uuid.UUID, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	return tracer.Start(ctx, "agents.Service."+name, trace.WithAttributes(append(attrs, attribute.String("cobranch.organization.id", orgID.String()))...))
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
		errors.Is(err, ErrBuiltIn), errors.Is(err, ErrEnded), errors.Is(err, ErrChanged):
		return err
	}
	return fmt.Errorf("%s: %w", what, err)
}

// Agents lists the organization's agents by name, each with its grants; it takes org.read.
func (s *Service) Agents(ctx context.Context, v Viewer, orgID uuid.UUID) ([]Agent, error) {
	ctx, span := orgSpan(ctx, "Agents", orgID)
	defer span.End()
	if _, err := s.held(ctx, v, orgID); err != nil {
		return nil, failed(span, "listing the agents", err)
	}
	list, err := s.store.Agents(ctx, orgID)
	return list, failed(span, "listing the agents", err)
}

// Agent finds one of the organization's agents with its grants; it takes org.read.
func (s *Service) Agent(ctx context.Context, v Viewer, orgID, id uuid.UUID) (Agent, error) {
	ctx, span := orgSpan(ctx, "Agent", orgID, attribute.String("cobranch.agent.id", id.String()))
	defer span.End()
	if _, err := s.held(ctx, v, orgID); err != nil {
		return Agent{}, failed(span, "finding the agent", err)
	}
	a, err := s.store.Agent(ctx, orgID, id)
	return a, failed(span, "finding the agent", err)
}

// grant checks one of a new agent's first grants: a role the organization grants, which v, holding atOrg at the
// organization, holds every permission of where it applies; on a repository, the role's repository permissions, on
// one of the organization's repositories v reads. It answers the grant with its names.
func (s *Service) grant(ctx context.Context, v Viewer, orgID uuid.UUID, roles []orgs.Role, atOrg []string, g Grant) (Grant, error) {
	i := slices.IndexFunc(roles, func(r orgs.Role) bool { return r.ID == g.RoleID })
	if i < 0 {
		return g, invalid("grants", "role.invalid", "not a role this organization grants")
	}
	role := roles[i]
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
		return g, err
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
	ctx, span := orgSpan(ctx, "CreateAgent", orgID)
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
	roles, err := s.store.Roles(ctx, orgID)
	if err != nil {
		return Agent{}, failed(span, "listing the roles", err)
	}
	a := Agent{OrgID: orgID, Name: in.Name, Description: in.Description, Instructions: in.Instructions,
		CreatedBy: &Principal{ID: v.ID, DisplayName: v.DisplayName}, Version: 1}
	for _, g := range in.Grants {
		g, err := s.grant(ctx, v, orgID, roles, atOrg, g)
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
// being its creator, or org.principals.manage.
func (s *Service) UpdateAgent(ctx context.Context, v Viewer, orgID, id uuid.UUID, version int64, c Change) (Agent, error) {
	ctx, span := orgSpan(ctx, "UpdateAgent", orgID, attribute.String("cobranch.agent.id", id.String()))
	defer span.End()
	held, err := s.held(ctx, v, orgID)
	if err != nil {
		return Agent{}, failed(span, "changing the agent", err)
	}
	a, err := s.store.Agent(ctx, orgID, id)
	if err != nil {
		return Agent{}, failed(span, "finding the agent", err)
	}
	if !slices.Contains(held, orgs.PermPrincipalsManage) && (a.CreatedBy == nil || a.CreatedBy.ID != v.ID) {
		return Agent{}, failed(span, "changing the agent", fmt.Errorf("%w: changing another's agent takes %s", auth.ErrForbidden, orgs.PermPrincipalsManage))
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
	ctx, span := orgSpan(ctx, "DeleteAgent", orgID, attribute.String("cobranch.agent.id", id.String()))
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
	if len(grants) == 0 {
		return nil
	}
	roles, err := s.store.Roles(ctx, orgID)
	if err != nil {
		return fmt.Errorf("listing the roles: %w", err)
	}
	for _, g := range grants {
		i := slices.IndexFunc(roles, func(r orgs.Role) bool { return r.ID == g.RoleID })
		if i < 0 {
			continue
		}
		held, permissions := atOrg, roles[i].Permissions
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
