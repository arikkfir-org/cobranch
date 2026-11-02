package secrets

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

var tracer = otel.Tracer("cobranch.dev/internal/services/secrets")

// Service runs secrets: every operation checks its viewer's permissions through the authorizer first, and seals and
// opens values with the installation's secrets key.
type Service struct {
	store  Store
	authz  *auth.Authorizer
	sealer auth.Sealer
	newID  func() uuid.UUID
}

// NewService returns a Service over store, checking permissions with authz and sealing values with sealer: nil when
// the installation has no secrets key, which leaves secrets listed but none written or received.
func NewService(store Store, authz *auth.Authorizer, sealer auth.Sealer) *Service {
	return &Service{store: store, authz: authz, sealer: sealer, newID: newID}
}

// newID is a new secret's ID: a version 7 UUID, or a random one if the clock can't give one.
func newID() uuid.UUID {
	if id, err := uuid.NewV7(); err == nil {
		return id
	}
	return uuid.New()
}

// Viewer is who an operation runs for: a signed-in user, or an API key.
type Viewer = auth.User

// place checks that v manages the secrets where they live, the organization's (repoID uuid.Nil) or a repository's:
// ErrNotFound when v can't see it, auth.ErrForbidden without secrets.manage there.
func (s *Service) place(ctx context.Context, v Viewer, orgID, repoID uuid.UUID) error {
	scope, scopeID, read := auth.ScopeOrganization, orgID, orgs.PermRead
	if repoID != uuid.Nil {
		scope, scopeID, read = auth.ScopeRepository, repoID, repos.PermRead
	}
	held, err := s.authz.Holds(ctx, v.ID, scope, scopeID)
	if err != nil {
		return fmt.Errorf("reading the viewer's permissions: %w", err)
	}
	switch {
	case !slices.Contains(held, read):
		return ErrNotFound
	case !slices.Contains(held, PermManage):
		return fmt.Errorf("%w: %s at the %s", auth.ErrForbidden, PermManage, scope)
	}
	return nil
}

// secret finds one of the organization's secrets for v, who sees where it lives, with what v holds on it:
// ErrNotFound otherwise.
func (s *Service) secret(ctx context.Context, v Viewer, orgID, id uuid.UUID) (Secret, []string, error) {
	secret, err := s.store.Get(ctx, id)
	if err != nil {
		return Secret{}, nil, wrap("finding the secret", err)
	}
	if secret.OrgID != orgID {
		return Secret{}, nil, ErrNotFound
	}
	scope, scopeID, read := auth.ScopeOrganization, orgID, orgs.PermRead
	if secret.RepoID != uuid.Nil {
		scope, scopeID, read = auth.ScopeRepository, secret.RepoID, repos.PermRead
	}
	if err := s.authz.Can(ctx, v.ID, read, scope, scopeID); err != nil {
		if errors.Is(err, auth.ErrForbidden) {
			return Secret{}, nil, ErrNotFound
		}
		return Secret{}, nil, fmt.Errorf("checking the viewer's permissions: %w", err)
	}
	held, err := s.authz.Holds(ctx, v.ID, auth.ScopeSecret, id)
	if err != nil {
		return Secret{}, nil, fmt.Errorf("reading the viewer's permissions: %w", err)
	}
	return secret, held, nil
}

// managed finds one of the organization's secrets for v holding secrets.manage on it.
func (s *Service) managed(ctx context.Context, v Viewer, orgID, id uuid.UUID) (Secret, error) {
	secret, held, err := s.secret(ctx, v, orgID, id)
	if err != nil {
		return Secret{}, err
	}
	if !slices.Contains(held, PermManage) {
		return Secret{}, fmt.Errorf("%w: %s on the secret", auth.ErrForbidden, PermManage)
	}
	return secret, nil
}

// List lists the secrets of the organization (repoID uuid.Nil) or of one of its repositories, by name, without
// their values; it takes secrets.manage there.
func (s *Service) List(ctx context.Context, v Viewer, orgID, repoID uuid.UUID) (_ []Secret, err error) {
	ctx, span := tracer.Start(ctx, "secrets.Service.List", trace.WithAttributes(orgAttr(orgID), repoAttr(repoID)))
	defer span.End()
	defer func() { record(span, err) }()
	if err := s.place(ctx, v, orgID, repoID); err != nil {
		return nil, err
	}
	list, err := s.store.List(ctx, orgID, repoID)
	if err != nil {
		return nil, fmt.Errorf("listing the secrets: %w", err)
	}
	return list, nil
}

// Jobs lists the repository's jobs, the principals its secrets are granted to, by name: each job that has queued a
// run, which made it a principal. It takes repo.read on the repository.
func (s *Service) Jobs(ctx context.Context, v Viewer, orgID, repoID uuid.UUID) (_ []Principal, err error) {
	ctx, span := tracer.Start(ctx, "secrets.Service.Jobs", trace.WithAttributes(orgAttr(orgID), repoAttr(repoID)))
	defer span.End()
	defer func() { record(span, err) }()
	if err := s.authz.Can(ctx, v.ID, repos.PermRead, auth.ScopeRepository, repoID); err != nil {
		if errors.Is(err, auth.ErrForbidden) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("checking the viewer's permissions: %w", err)
	}
	list, err := s.store.Jobs(ctx, orgID, repoID)
	if err != nil {
		return nil, fmt.Errorf("listing the jobs: %w", err)
	}
	return list, nil
}

// Get answers one of the organization's secrets, without its value; it takes secrets.manage on it.
func (s *Service) Get(ctx context.Context, v Viewer, orgID, id uuid.UUID) (_ Secret, err error) {
	ctx, span := tracer.Start(ctx, "secrets.Service.Get", trace.WithAttributes(orgAttr(orgID), secretAttr(id)))
	defer span.End()
	defer func() { record(span, err) }()
	return s.managed(ctx, v, orgID, id)
}

// seal seals value for the secret id.
func (s *Service) seal(id uuid.UUID, value string) ([]byte, error) {
	if s.sealer == nil {
		return nil, ErrNoSecretsKey
	}
	sealed, err := s.sealer.Seal([]byte(value), About(id))
	if err != nil {
		return nil, fmt.Errorf("sealing the value: %w", err)
	}
	return sealed, nil
}

// Create adds a secret to the organization's settings (repoID uuid.Nil) or to one of its repositories'; it takes
// secrets.manage there.
func (s *Service) Create(ctx context.Context, v Viewer, orgID, repoID uuid.UUID, in Input) (_ Secret, err error) {
	ctx, span := tracer.Start(ctx, "secrets.Service.Create", trace.WithAttributes(orgAttr(orgID), repoAttr(repoID)))
	defer span.End()
	defer func() { record(span, err) }()
	if err := s.place(ctx, v, orgID, repoID); err != nil {
		return Secret{}, err
	}
	in, err = Validate(in, false)
	if err != nil {
		return Secret{}, err
	}
	id := s.newID()
	sealed, err := s.seal(id, in.Value)
	if err != nil {
		return Secret{}, err
	}
	created, err := s.store.Create(ctx, v.ID, Secret{ID: id, OrgID: orgID, RepoID: repoID, Name: in.Name}, sealed)
	return created, wrap("creating the secret", err)
}

// Replace renames the secret and, unless in leaves the value empty, replaces its value, if it is still at version
// (any with AnyVersion); it takes secrets.manage on it.
func (s *Service) Replace(ctx context.Context, v Viewer, orgID, id uuid.UUID, version int64, in Input) (_ Secret, err error) {
	ctx, span := tracer.Start(ctx, "secrets.Service.Replace", trace.WithAttributes(orgAttr(orgID), secretAttr(id)))
	defer span.End()
	defer func() { record(span, err) }()
	if _, err := s.managed(ctx, v, orgID, id); err != nil {
		return Secret{}, err
	}
	in, err = Validate(in, true)
	if err != nil {
		return Secret{}, err
	}
	var sealed []byte
	if in.Value != "" {
		if sealed, err = s.seal(id, in.Value); err != nil {
			return Secret{}, err
		}
	}
	replaced, err := s.store.Replace(ctx, v.ID, id, version, in.Name, sealed)
	return replaced, wrap("replacing the secret", err)
}

// Delete deletes the secret and every grant on it, if it is still at version (any with AnyVersion); it takes
// secrets.manage on it.
func (s *Service) Delete(ctx context.Context, v Viewer, orgID, id uuid.UUID, version int64) (err error) {
	ctx, span := tracer.Start(ctx, "secrets.Service.Delete", trace.WithAttributes(orgAttr(orgID), secretAttr(id)))
	defer span.End()
	defer func() { record(span, err) }()
	if _, err := s.managed(ctx, v, orgID, id); err != nil {
		return err
	}
	return wrap("deleting the secret", s.store.Delete(ctx, v.ID, id, version))
}

// Access lists who holds a grant on the secret, by display name; it takes secrets.manage on it, or
// org.grants.manage.
func (s *Service) Access(ctx context.Context, v Viewer, orgID, id uuid.UUID) (_ []Principal, err error) {
	ctx, span := tracer.Start(ctx, "secrets.Service.Access", trace.WithAttributes(orgAttr(orgID), secretAttr(id)))
	defer span.End()
	defer func() { record(span, err) }()
	_, held, err := s.secret(ctx, v, orgID, id)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(held, PermManage) {
		if err := s.authz.Can(ctx, v.ID, orgs.PermGrantsManage, auth.ScopeOrganization, orgID); err != nil {
			return nil, fmt.Errorf("checking the viewer's permissions: %w", err)
		}
	}
	access, err := s.store.Access(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("listing who receives the secret: %w", err)
	}
	return access, nil
}

// grantable finds the secret and checks the principal a grant or a revocation names, for a viewer with
// org.grants.manage who holds secrets.read on the secret, as granting a role takes every permission it gives.
func (s *Service) grantable(ctx context.Context, v Viewer, orgID, id, principalID uuid.UUID) (Secret, error) {
	secret, held, err := s.secret(ctx, v, orgID, id)
	if err != nil {
		return Secret{}, err
	}
	if err := s.authz.Can(ctx, v.ID, orgs.PermGrantsManage, auth.ScopeOrganization, orgID); err != nil {
		return Secret{}, fmt.Errorf("checking the viewer's permissions: %w", err)
	}
	if !slices.Contains(held, PermRead) {
		return Secret{}, fmt.Errorf("%w: granting Secret reader takes %s on the secret", auth.ErrForbidden, PermRead)
	}
	if _, err := s.store.Principal(ctx, orgID, principalID); err != nil {
		return Secret{}, wrap("finding the principal", err)
	}
	return secret, nil
}

// Grant lets one of the organization's principals receive the secret, granting it Secret reader on it; it takes
// org.grants.manage and secrets.read on the secret.
func (s *Service) Grant(ctx context.Context, v Viewer, orgID, id, principalID uuid.UUID) (err error) {
	ctx, span := tracer.Start(ctx, "secrets.Service.Grant", trace.WithAttributes(orgAttr(orgID), secretAttr(id), principalAttr(principalID)))
	defer span.End()
	defer func() { record(span, err) }()
	secret, err := s.grantable(ctx, v, orgID, id, principalID)
	if err != nil {
		return err
	}
	return wrap("granting Secret reader", s.store.Grant(ctx, v.ID, secret, principalID))
}

// Revoke takes Secret reader on the secret from the principal; it takes org.grants.manage and secrets.read on the
// secret. Grants on the repository or the organization still reach it.
func (s *Service) Revoke(ctx context.Context, v Viewer, orgID, id, principalID uuid.UUID) (err error) {
	ctx, span := tracer.Start(ctx, "secrets.Service.Revoke", trace.WithAttributes(orgAttr(orgID), secretAttr(id), principalAttr(principalID)))
	defer span.End()
	defer func() { record(span, err) }()
	secret, err := s.grantable(ctx, v, orgID, id, principalID)
	if err != nil {
		return err
	}
	return wrap("revoking Secret reader", s.store.Revoke(ctx, v.ID, secret, principalID))
}

// Receive opens the values of the secrets ids names for principalID, a job's or an agent's, if it holds
// secrets.read on every one: all of them, or a RefusedError naming the first it may not have. Its callers put the
// values where only the run or the task reads them, and never log them.
func (s *Service) Receive(ctx context.Context, principalID uuid.UUID, ids []uuid.UUID) (_ map[uuid.UUID][]byte, err error) {
	ctx, span := tracer.Start(ctx, "secrets.Service.Receive", trace.WithAttributes(principalAttr(principalID), attribute.Int("cobranch.secrets.count", len(ids))))
	defer span.End()
	defer func() { record(span, err) }()
	ids = slices.Compact(slices.SortedFunc(slices.Values(ids), func(a, b uuid.UUID) int { return slices.Compare(a[:], b[:]) }))
	if len(ids) == 0 {
		return map[uuid.UUID][]byte{}, nil
	}
	sealed, err := s.store.Sealed(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("reading the secrets: %w", err)
	}
	for _, id := range ids {
		if _, ok := sealed[id]; !ok {
			return nil, &RefusedError{ID: id, Missing: true}
		}
		if err := s.authz.Can(ctx, principalID, PermRead, auth.ScopeSecret, id); err != nil {
			if errors.Is(err, auth.ErrForbidden) {
				return nil, &RefusedError{ID: id}
			}
			return nil, fmt.Errorf("checking the principal's permissions: %w", err)
		}
	}
	if s.sealer == nil {
		return nil, ErrNoSecretsKey
	}
	values := make(map[uuid.UUID][]byte, len(ids))
	for _, id := range ids {
		value, err := s.sealer.Open(sealed[id], About(id))
		if err != nil {
			return nil, fmt.Errorf("opening secret %s: %w", id, err)
		}
		values[id] = value
	}
	return values, nil
}

// wrap keeps the domain's own errors as they are and gives any other what's context.
func wrap(what string, err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrNotFound), errors.Is(err, ErrNameTaken), errors.Is(err, ErrChanged), errors.Is(err, ErrGranted):
		return err
	}
	return fmt.Errorf("%s: %w", what, err)
}

// record records err on span unless it is one of the domain's answers to the request.
func record(span trace.Span, err error) {
	var verr *orgs.ValidationError
	var refused *RefusedError
	switch {
	case err == nil, errors.Is(err, ErrNotFound), errors.Is(err, ErrNameTaken), errors.Is(err, ErrChanged), errors.Is(err, ErrGranted),
		errors.Is(err, ErrNoSecretsKey), errors.Is(err, auth.ErrForbidden), errors.As(err, &verr), errors.As(err, &refused):
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
func secretAttr(id uuid.UUID) attribute.KeyValue {
	return attribute.String("cobranch.secret.id", id.String())
}
func principalAttr(id uuid.UUID) attribute.KeyValue {
	return attribute.String("cobranch.principal.id", id.String())
}
