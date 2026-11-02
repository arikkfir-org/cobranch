package graphql

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/secrets"
)

// Secrets is what the API needs of the secrets' service.
type Secrets interface {
	List(ctx context.Context, v secrets.Viewer, orgID, repoID uuid.UUID) ([]secrets.Secret, error)
	Jobs(ctx context.Context, v secrets.Viewer, orgID, repoID uuid.UUID) ([]secrets.Principal, error)
	Create(ctx context.Context, v secrets.Viewer, orgID, repoID uuid.UUID, in secrets.Input) (secrets.Secret, error)
	Replace(ctx context.Context, v secrets.Viewer, orgID, id uuid.UUID, version int64, in secrets.Input) (secrets.Secret, error)
	Delete(ctx context.Context, v secrets.Viewer, orgID, id uuid.UUID, version int64) error
	Access(ctx context.Context, v secrets.Viewer, orgID, id uuid.UUID) ([]secrets.Principal, error)
	Grant(ctx context.Context, v secrets.Viewer, orgID, id, principalID uuid.UUID) error
	Revoke(ctx context.Context, v secrets.Viewer, orgID, id, principalID uuid.UUID) error
}

// secret is s as the API shows it, never with its value; who receives it resolves on its own.
func secret(s secrets.Secret) *Secret {
	return &Secret{ID: s.ID.String(), OrganizationID: s.OrgID.String(), RepositoryID: idOrNil(s.RepoID), Name: s.Name, Version: int(s.Version),
		CreatedBy: idOrNil(s.CreatedBy), CreatedAt: s.CreatedAt.UTC(), UpdatedBy: idOrNil(s.UpdatedBy), UpdatedAt: s.UpdatedAt.UTC()}
}

func grantee(p secrets.Principal) *Grantee {
	return &Grantee{PrincipalID: p.ID.String(), Kind: string(p.Kind), DisplayName: p.DisplayName}
}

// idOrNil is id as the API shows it, or nil for uuid.Nil, which it shows as null.
func idOrNil(id uuid.UUID) *string {
	if id == uuid.Nil {
		return nil
	}
	s := id.String()
	return &s
}

// secretFail maps the secrets' errors to a field's, and leaves the rest to fail.
func secretFail(ctx context.Context, err error, kind, what string) error {
	switch {
	case errors.Is(err, secrets.ErrNotFound):
		return refuse(ctx, notFound, kind+".not_found", "there is no such "+kind+", or you may not see it")
	case errors.Is(err, secrets.ErrChanged):
		return refuse(ctx, conflict, "precondition.failed", "the secret changed since you read it: read it again")
	case errors.Is(err, secrets.ErrNameTaken):
		return refuse(ctx, conflict, "secret.name_taken", "another secret here has this name")
	case errors.Is(err, secrets.ErrGranted):
		return refuse(ctx, conflict, "grant.exists", "the principal already receives this secret")
	case errors.Is(err, secrets.ErrNoSecretsKey):
		return refuse(ctx, conflict, "secrets_key.missing", "this installation has no secrets key to seal secrets with: its operator sets one")
	}
	return fail(ctx, err, kind, what)
}

// secretVersion is the version of a secret that expectedVersion names, from 1: secrets.AnyVersion without one.
func secretVersion(ctx context.Context, expectedVersion *int) (int64, error) {
	if expectedVersion == nil {
		return secrets.AnyVersion, nil
	}
	if *expectedVersion < 1 {
		return 0, refuse(ctx, badUserInput, "version.invalid", "a version of the secret, from 1")
	}
	return int64(*expectedVersion), nil
}

// secretTarget is the viewer and the organization and secret a mutation names, by ID.
func secretTarget(ctx context.Context, organizationID, id string) (secrets.Viewer, uuid.UUID, uuid.UUID, error) {
	v, err := viewer(ctx)
	if err != nil {
		return secrets.Viewer{}, uuid.Nil, uuid.Nil, err
	}
	orgID, err := parseID(ctx, organizationID, "organization")
	if err != nil {
		return secrets.Viewer{}, uuid.Nil, uuid.Nil, err
	}
	secretID, err := parseID(ctx, id, "secret")
	if err != nil {
		return secrets.Viewer{}, uuid.Nil, uuid.Nil, err
	}
	return v.User, orgID, secretID, nil
}
