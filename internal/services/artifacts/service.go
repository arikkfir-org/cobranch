package artifacts

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/orgs"
	"cobranch.dev/internal/services/repos"
)

var tracer = otel.Tracer("cobranch.dev/internal/services/artifacts")

// Service runs artifacts: every operation checks its viewer's permissions on the artifact, on its repository or at its
// organization, through the authorizer first, and answers ErrNotFound for an artifact the viewer may not read.
type Service struct {
	store    Store
	authz    *auth.Authorizer
	markdown code.Renderer
	newID    func() uuid.UUID
}

// NewService returns a Service over store, checking permissions with authz and rendering Markdown bodies with
// markdown.
func NewService(store Store, authz *auth.Authorizer, markdown code.Renderer) *Service {
	return &Service{store: store, authz: authz, markdown: markdown, newID: newID}
}

// newID is a new artifact's or version's ID: a version 7 UUID, or a random one if the clock can't give one.
func newID() uuid.UUID {
	if id, err := uuid.NewV7(); err == nil {
		return id
	}
	return uuid.New()
}

// Viewer is who an operation runs for: a signed-in user, an API key, or an agent on a task.
type Viewer = auth.User

// reads checks that v reads the repository, or the organization for uuid.Nil: ErrNotFound otherwise.
func (s *Service) reads(ctx context.Context, v Viewer, orgID, repoID uuid.UUID) error {
	permission, scope, scopeID := repos.PermRead, auth.ScopeRepository, repoID
	if repoID == uuid.Nil {
		permission, scope, scopeID = orgs.PermRead, auth.ScopeOrganization, orgID
	}
	if err := s.authz.Can(ctx, v.ID, permission, scope, scopeID); err != nil {
		if errors.Is(err, auth.ErrForbidden) {
			return ErrNotFound
		}
		return fmt.Errorf("checking the viewer's permissions on the %s: %w", scope, err)
	}
	return nil
}

// artifact finds one of the organization's artifacts that v reads, with what v holds on it: ErrNotFound otherwise.
func (s *Service) artifact(ctx context.Context, v Viewer, orgID, id uuid.UUID) (Artifact, []string, error) {
	a, err := s.store.Get(ctx, id)
	if err != nil {
		return Artifact{}, nil, wrap("finding the artifact", err)
	}
	if a.OrgID != orgID {
		return Artifact{}, nil, ErrNotFound
	}
	held, err := s.authz.Holds(ctx, v.ID, auth.ScopeArtifact, id)
	if err != nil {
		return Artifact{}, nil, fmt.Errorf("reading the viewer's permissions on the artifact: %w", err)
	}
	if !slices.Contains(held, PermRead) {
		return Artifact{}, nil, ErrNotFound
	}
	return a, held, nil
}

// holding finds one of the organization's artifacts that v reads and holds every one of permissions on.
func (s *Service) holding(ctx context.Context, v Viewer, orgID, id uuid.UUID, permissions ...string) (Artifact, []string, error) {
	a, held, err := s.artifact(ctx, v, orgID, id)
	if err != nil {
		return Artifact{}, nil, err
	}
	for _, p := range permissions {
		if !slices.Contains(held, p) {
			return Artifact{}, nil, fmt.Errorf("%w: %s on the artifact", auth.ErrForbidden, p)
		}
	}
	return a, held, nil
}

// List lists the artifacts attached to the repository that v may list or read, of kind (all of them for ""), newest
// change first; it takes repo.read on the repository.
func (s *Service) List(ctx context.Context, v Viewer, orgID, repoID uuid.UUID, kind Kind) (_ []Artifact, err error) {
	ctx, span := tracer.Start(ctx, "artifacts.Service.List", trace.WithAttributes(orgAttr(orgID), repoAttr(repoID)))
	defer span.End()
	defer func() { record(span, err) }()
	if kind != "" && !validKind(kind) {
		return nil, invalid("kind", "artifact.kind_invalid", "an artifact is a document, a diagram, a report or a page")
	}
	if err := s.reads(ctx, v, orgID, repoID); err != nil {
		return nil, err
	}
	return s.listed(ctx, v, orgID, Filter{RepoID: repoID, Kind: kind})
}

// ListOrganization lists the organization's artifacts that v may list or read and f keeps, newest change first: those
// attached to its repositories, v's own, and those shared with v. It takes org.read.
func (s *Service) ListOrganization(ctx context.Context, v Viewer, orgID uuid.UUID, f Filter) (_ []Artifact, err error) {
	ctx, span := tracer.Start(ctx, "artifacts.Service.ListOrganization", trace.WithAttributes(orgAttr(orgID)))
	defer span.End()
	defer func() { record(span, err) }()
	if f.Kind != "" && !validKind(f.Kind) {
		return nil, invalid("kind", "artifact.kind_invalid", "an artifact is a document, a diagram, a report or a page")
	}
	if err := s.reads(ctx, v, orgID, uuid.Nil); err != nil {
		return nil, err
	}
	return s.listed(ctx, v, orgID, f)
}

// listed lists the organization's artifacts that f keeps and v may list or read, each with which of artifacts.list and
// artifacts.read v holds on it.
func (s *Service) listed(ctx context.Context, v Viewer, orgID uuid.UUID, f Filter) ([]Artifact, error) {
	held := map[uuid.UUID][]string{}
	for _, permission := range []string{PermList, PermRead} {
		ids, err := s.authz.Among(ctx, v.ID, permission, func(ctx context.Context, principalID uuid.UUID) ([]uuid.UUID, error) {
			return s.store.With(ctx, principalID, orgID, f.RepoID, permission)
		})
		if err != nil {
			return nil, fmt.Errorf("listing the artifacts where the viewer holds %s: %w", permission, err)
		}
		for _, id := range ids {
			held[id] = append(held[id], permission)
		}
	}
	if len(held) == 0 {
		return []Artifact{}, nil
	}
	list, err := s.store.List(ctx, slices.Collect(maps.Keys(held)))
	if err != nil {
		return nil, fmt.Errorf("reading the artifacts: %w", err)
	}
	list = slices.DeleteFunc(list, func(a Artifact) bool {
		return a.OrgID != orgID || (f.Kind != "" && a.Kind != f.Kind) || (f.Mine && a.Owner.ID != v.ID)
	})
	for i := range list {
		list[i].Permissions = held[list[i].ID]
	}
	return list, nil
}

// Get answers one of the organization's artifacts with what v holds on it; it takes artifacts.read on it.
func (s *Service) Get(ctx context.Context, v Viewer, orgID, id uuid.UUID) (_ Artifact, err error) {
	ctx, span := tracer.Start(ctx, "artifacts.Service.Get", trace.WithAttributes(orgAttr(orgID), artifactAttr(id)))
	defer span.End()
	defer func() { record(span, err) }()
	a, held, err := s.artifact(ctx, v, orgID, id)
	if err != nil {
		return Artifact{}, err
	}
	a.Permissions = held
	return a, nil
}

// Create makes an artifact of the organization with its first version, attached to one of its repositories or, for
// uuid.Nil, to none, and answers whether it made it: an ID that v made there already answers that artifact, as it is,
// so a call run again makes nothing more. Attaching it to a repository takes artifacts.create there, and one attached
// to none takes org.read, since it reaches nobody else until shared. Its maker owns it; an agent's task makes its
// assigner the owner and the agent an editor.
func (s *Service) Create(ctx context.Context, v Viewer, orgID, repoID uuid.UUID, in Input) (_ Artifact, created bool, err error) {
	ctx, span := tracer.Start(ctx, "artifacts.Service.Create", trace.WithAttributes(orgAttr(orgID), repoAttr(repoID)))
	defer span.End()
	defer func() { record(span, err) }()
	if err := s.reads(ctx, v, orgID, repoID); err != nil {
		return Artifact{}, false, err
	}
	if in, err = Validate(in); err != nil {
		return Artifact{}, false, err
	}
	if repoID == uuid.Nil && in.RepositoryAccess != ReachNone {
		return Artifact{}, false, unattachedAudience()
	}
	if in.ID != uuid.Nil {
		if a, err := s.replay(ctx, v, orgID, repoID, in.ID); !errors.Is(err, ErrNotFound) {
			return a, false, err
		}
	} else {
		in.ID = s.newID()
	}
	if repoID != uuid.Nil {
		if err := s.authz.Can(ctx, v.ID, PermCreate, auth.ScopeRepository, repoID); err != nil {
			return Artifact{}, false, fmt.Errorf("checking the viewer's permissions on the repository: %w", err)
		}
	}
	span.SetAttributes(artifactAttr(in.ID))
	a, err := s.store.Create(ctx, v.ID, made(ctx, v, orgID, repoID, in))
	if err != nil {
		return Artifact{}, false, wrap("creating the artifact", err)
	}
	return a, true, nil
}

// made is the artifact in makes, with its owner and its makers' roles: v owns it, unless v is an agent on a task, whose
// assigner owns it, the agent holding Artifact editor.
func made(ctx context.Context, v Viewer, orgID, repoID uuid.UUID, in Input) New {
	n := New{
		Artifact: Artifact{ID: in.ID, OrgID: orgID, RepoID: repoID, Owner: Principal{ID: v.ID}, Title: in.Title, Summary: in.Summary, Kind: in.Kind,
			RepositoryAccess: in.RepositoryAccess, OrganizationAccess: in.OrganizationAccess, Branch: in.Branch},
		First:  VersionInput{ID: in.ID, MediaType: in.MediaType, Body: in.Body, Note: in.Note},
		Owners: []NewGrant{{PrincipalID: v.ID, RoleID: auth.RoleArtifactOwner}},
	}
	if task, ok := auth.TaskOf(ctx, v.ID); ok {
		n.Artifact.Agent, n.Artifact.TaskID = Principal{ID: v.ID}, task.ID
		if task.AssignerID != v.ID {
			n.Artifact.Owner = Principal{ID: task.AssignerID}
			n.Owners = []NewGrant{{PrincipalID: task.AssignerID, RoleID: auth.RoleArtifactOwner}, {PrincipalID: v.ID, RoleID: auth.RoleArtifactEditor}}
		}
	}
	return n
}

// unattachedAudience refuses a repository audience for an artifact attached to no repository.
func unattachedAudience() error {
	return invalid("repositoryAccess", "artifact.access_invalid", "an artifact attached to no repository has no repository audience")
}

// replay answers the artifact id names when v made it in the repository and still reads it, ErrExists when another
// principal or repository took the ID, and ErrNotFound when it isn't there.
func (s *Service) replay(ctx context.Context, v Viewer, orgID, repoID, id uuid.UUID) (Artifact, error) {
	existing, err := s.store.Get(ctx, id)
	if err != nil {
		return Artifact{}, wrap("finding the artifact", err)
	}
	if existing.RepoID != repoID || existing.CreatedBy.ID != v.ID {
		return Artifact{}, ErrExists
	}
	a, _, err := s.artifact(ctx, v, orgID, id)
	if errors.Is(err, ErrNotFound) {
		return Artifact{}, ErrExists
	}
	return a, err
}

// Update changes the title, the summary and the audiences p names of an artifact still at version (any with
// AnyVersion); its title and summary take artifacts.write on it, its audiences artifacts.manage.
func (s *Service) Update(ctx context.Context, v Viewer, orgID, id uuid.UUID, version int64, p Patch) (_ Artifact, err error) {
	ctx, span := tracer.Start(ctx, "artifacts.Service.Update", trace.WithAttributes(orgAttr(orgID), artifactAttr(id)))
	defer span.End()
	defer func() { record(span, err) }()
	var needs []string
	if p.Title != nil || p.Summary != nil {
		needs = append(needs, PermWrite)
	}
	if p.RepositoryAccess != nil || p.OrganizationAccess != nil {
		needs = append(needs, PermManage)
	}
	a, _, err := s.holding(ctx, v, orgID, id, needs...)
	if err != nil {
		return Artifact{}, err
	}
	c := Changes{Title: a.Title, Summary: a.Summary, RepositoryAccess: a.RepositoryAccess, OrganizationAccess: a.OrganizationAccess}
	for to, from := range map[*string]*string{&c.Title: p.Title, &c.Summary: p.Summary} {
		if from != nil {
			*to = strings.TrimSpace(*from)
		}
	}
	for to, from := range map[*Reach]*Reach{&c.RepositoryAccess: p.RepositoryAccess, &c.OrganizationAccess: p.OrganizationAccess} {
		if from != nil {
			*to = *from
		}
	}
	if err := validateRow(c); err != nil {
		return Artifact{}, err
	}
	if a.RepoID == uuid.Nil && c.RepositoryAccess != ReachNone {
		return Artifact{}, unattachedAudience()
	}
	updated, err := s.store.Update(ctx, v.ID, id, version, c)
	return updated, wrap("changing the artifact", err)
}

// Delete deletes an artifact still at version (any with AnyVersion), its versions and every grant on it; it takes
// artifacts.manage on it.
func (s *Service) Delete(ctx context.Context, v Viewer, orgID, id uuid.UUID, version int64) (err error) {
	ctx, span := tracer.Start(ctx, "artifacts.Service.Delete", trace.WithAttributes(orgAttr(orgID), artifactAttr(id)))
	defer span.End()
	defer func() { record(span, err) }()
	if _, _, err := s.holding(ctx, v, orgID, id, PermManage); err != nil {
		return err
	}
	return wrap("deleting the artifact", s.store.Delete(ctx, v.ID, id, version))
}

// AddVersion adds the next version of an artifact still at version (any with AnyVersion), and answers the artifact as
// it now is, the version without its body, and whether it added it: an ID of a version v added to the artifact
// already answers that version, so a call run again adds nothing more. It takes artifacts.write on it.
func (s *Service) AddVersion(ctx context.Context, v Viewer, orgID, id uuid.UUID, version int64, in VersionInput) (_ Artifact, _ Version, added bool, err error) {
	ctx, span := tracer.Start(ctx, "artifacts.Service.AddVersion", trace.WithAttributes(orgAttr(orgID), artifactAttr(id)))
	defer span.End()
	defer func() { record(span, err) }()
	a, _, err := s.holding(ctx, v, orgID, id, PermWrite)
	if err != nil {
		return Artifact{}, Version{}, false, err
	}
	if in, err = ValidateVersion(in); err != nil {
		return Artifact{}, Version{}, false, err
	}
	if in.ID != uuid.Nil {
		existing, err := s.store.VersionByID(ctx, in.ID)
		switch {
		case err == nil && existing.ArtifactID == id && existing.Author.ID == v.ID:
			return a, bodiless(existing), false, nil
		case err == nil:
			return Artifact{}, Version{}, false, ErrExists
		case !errors.Is(err, ErrNotFound):
			return Artifact{}, Version{}, false, wrap("finding the version", err)
		}
	} else {
		in.ID = s.newID()
	}
	taskID, _ := auth.WorkingTask(ctx, v.ID)
	updated, err := s.store.AddVersion(ctx, v.ID, id, version, in, taskID)
	if err != nil {
		return Artifact{}, Version{}, false, wrap("adding the version", err)
	}
	ver, err := s.store.Version(ctx, id, updated.Head)
	if err != nil {
		return Artifact{}, Version{}, false, wrap("reading the version", err)
	}
	return updated, bodiless(ver), true, nil
}

// bodiless is ver without its body, as a list answers it.
func bodiless(ver Version) Version {
	ver.Body, ver.HTML = "", ""
	return ver
}

// Versions lists an artifact's versions, newest first, without their bodies; it takes artifacts.read on it.
func (s *Service) Versions(ctx context.Context, v Viewer, orgID, id uuid.UUID) (_ []Version, err error) {
	ctx, span := tracer.Start(ctx, "artifacts.Service.Versions", trace.WithAttributes(orgAttr(orgID), artifactAttr(id)))
	defer span.End()
	defer func() { record(span, err) }()
	if _, _, err := s.artifact(ctx, v, orgID, id); err != nil {
		return nil, err
	}
	list, err := s.store.Versions(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("listing the versions: %w", err)
	}
	return list, nil
}

// Version answers a version of an artifact, its newest for number 0, with its body and, for Markdown, the HTML the
// Console shows; it takes artifacts.read on it.
func (s *Service) Version(ctx context.Context, v Viewer, orgID, id uuid.UUID, number int32) (_ Artifact, _ Version, err error) {
	ctx, span := tracer.Start(ctx, "artifacts.Service.Version", trace.WithAttributes(orgAttr(orgID), artifactAttr(id),
		attribute.Int("cobranch.artifact.version", int(number))))
	defer span.End()
	defer func() { record(span, err) }()
	a, _, err := s.artifact(ctx, v, orgID, id)
	if err != nil {
		return Artifact{}, Version{}, err
	}
	if number == 0 {
		number = a.Head
	}
	ver, err := s.store.Version(ctx, id, number)
	if err != nil {
		return Artifact{}, Version{}, wrap("reading the version", err)
	}
	if ver.MediaType == MediaMarkdown {
		doc := code.Document{Org: a.OrgSlug, Path: "artifacts/" + id.String()}
		if ver.HTML, err = s.markdown.Render(ctx, []byte(ver.Body), doc); err != nil {
			return Artifact{}, Version{}, fmt.Errorf("rendering the version's Markdown: %w", err)
		}
	}
	return a, ver, nil
}

// Access answers who reaches an artifact: its audiences and the roles principals hold on it itself. It takes
// artifacts.read on it.
func (s *Service) Access(ctx context.Context, v Viewer, orgID, id uuid.UUID) (_ Access, err error) {
	ctx, span := tracer.Start(ctx, "artifacts.Service.Access", trace.WithAttributes(orgAttr(orgID), artifactAttr(id)))
	defer span.End()
	defer func() { record(span, err) }()
	a, _, err := s.artifact(ctx, v, orgID, id)
	if err != nil {
		return Access{}, err
	}
	grants, err := s.store.Grants(ctx, id)
	if err != nil {
		return Access{}, fmt.Errorf("listing who holds a role on the artifact: %w", err)
	}
	return Access{RepositoryAccess: a.RepositoryAccess, OrganizationAccess: a.OrganizationAccess, Grants: grants}, nil
}

// Share gives one of the organization's members, API keys or agents role on an artifact, in place of any role it held
// on it; it takes artifacts.manage on it and every permission the role gives, since nobody grants more than they hold.
func (s *Service) Share(ctx context.Context, v Viewer, orgID, id, principalID uuid.UUID, role Role) (err error) {
	ctx, span := tracer.Start(ctx, "artifacts.Service.Share", trace.WithAttributes(orgAttr(orgID), artifactAttr(id), principalAttr(principalID)))
	defer span.End()
	defer func() { record(span, err) }()
	roleID, ok := RoleID(role)
	if !ok || role == RoleLister {
		return invalid("role", "artifact.role_invalid", "an artifact grants viewer, editor or owner")
	}
	a, held, err := s.holding(ctx, v, orgID, id, PermManage)
	if err != nil {
		return err
	}
	if !auth.Covers(held, rolePermissions[role]) {
		return fmt.Errorf("%w: sharing an artifact as its %s takes every permission the role gives", auth.ErrForbidden, role)
	}
	p, err := s.store.Principal(ctx, orgID, principalID)
	if err != nil {
		return wrap("finding the principal", err)
	}
	if p.Kind != auth.KindUser && p.Kind != auth.KindAPIKey && p.Kind != auth.KindAgent {
		return invalid("principalId", "artifact.grantee_invalid", "an artifact is shared with the organization's members, API keys and agents")
	}
	return wrap("sharing the artifact", s.store.Share(ctx, v.ID, a, principalID, roleID))
}

// Unshare takes every role a principal holds on an artifact itself; it takes artifacts.manage on it and every
// permission those roles give. Grants on its repository or at its organization, and its audiences, still reach it.
func (s *Service) Unshare(ctx context.Context, v Viewer, orgID, id, principalID uuid.UUID) (err error) {
	ctx, span := tracer.Start(ctx, "artifacts.Service.Unshare", trace.WithAttributes(orgAttr(orgID), artifactAttr(id), principalAttr(principalID)))
	defer span.End()
	defer func() { record(span, err) }()
	a, held, err := s.holding(ctx, v, orgID, id, PermManage)
	if err != nil {
		return err
	}
	grants, err := s.store.Grants(ctx, id)
	if err != nil {
		return fmt.Errorf("listing who holds a role on the artifact: %w", err)
	}
	for _, g := range grants {
		if g.Principal.ID == principalID && !auth.Covers(held, rolePermissions[g.Role]) {
			return fmt.Errorf("%w: unsharing an artifact's %s takes every permission the role gives", auth.ErrForbidden, g.Role)
		}
	}
	return wrap("unsharing the artifact", s.store.Unshare(ctx, v.ID, a, principalID))
}

// wrap keeps the domain's own errors as they are and gives any other what's context.
func wrap(what string, err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrNotFound), errors.Is(err, ErrExists), errors.Is(err, ErrChanged):
		return err
	}
	return fmt.Errorf("%s: %w", what, err)
}

// record records err on span unless it is one of the domain's answers to the request.
func record(span trace.Span, err error) {
	var verr *orgs.ValidationError
	switch {
	case err == nil, errors.Is(err, ErrNotFound), errors.Is(err, ErrExists), errors.Is(err, ErrChanged),
		errors.Is(err, auth.ErrForbidden), errors.As(err, &verr):
		return
	}
	span.RecordError(err)
	span.SetStatus(codes.Error, err.Error())
}

func orgAttr(id uuid.UUID) attribute.KeyValue {
	return attribute.String("cobranch.organization.id", id.String())
}
func repoAttr(id uuid.UUID) attribute.KeyValue {
	return attribute.String("cobranch.repository.id", id.String())
}
func artifactAttr(id uuid.UUID) attribute.KeyValue {
	return attribute.String("cobranch.artifact.id", id.String())
}
func principalAttr(id uuid.UUID) attribute.KeyValue {
	return attribute.String("cobranch.principal.id", id.String())
}
