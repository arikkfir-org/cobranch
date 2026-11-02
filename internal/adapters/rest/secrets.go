package rest

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/secrets"
)

// Secrets is what the API needs of the secrets' service.
type Secrets interface {
	List(ctx context.Context, v secrets.Viewer, orgID, repoID uuid.UUID) ([]secrets.Secret, error)
	Jobs(ctx context.Context, v secrets.Viewer, orgID, repoID uuid.UUID) ([]secrets.Principal, error)
	Get(ctx context.Context, v secrets.Viewer, orgID, id uuid.UUID) (secrets.Secret, error)
	Create(ctx context.Context, v secrets.Viewer, orgID, repoID uuid.UUID, in secrets.Input) (secrets.Secret, error)
	Replace(ctx context.Context, v secrets.Viewer, orgID, id uuid.UUID, version int64, in secrets.Input) (secrets.Secret, error)
	Delete(ctx context.Context, v secrets.Viewer, orgID, id uuid.UUID, version int64) error
	Access(ctx context.Context, v secrets.Viewer, orgID, id uuid.UUID) ([]secrets.Principal, error)
	Grant(ctx context.Context, v secrets.Viewer, orgID, id, principalID uuid.UUID) error
	Revoke(ctx context.Context, v secrets.Viewer, orgID, id, principalID uuid.UUID) error
}

// secret is s as the API shows it, never with its value.
func secret(s secrets.Secret) Secret {
	out := Secret{Id: s.ID, OrgId: s.OrgID, Name: s.Name, Version: s.Version, CreatedAt: s.CreatedAt.UTC(), UpdatedAt: s.UpdatedAt.UTC()}
	for to, from := range map[**uuid.UUID]uuid.UUID{&out.RepoId: s.RepoID, &out.CreatedBy: s.CreatedBy, &out.UpdatedBy: s.UpdatedBy} {
		if from != uuid.Nil {
			*to = &from
		}
	}
	return out
}

func grantee(p secrets.Principal) Grantee {
	return Grantee{PrincipalId: p.ID, Kind: GranteeKind(p.Kind), DisplayName: p.DisplayName}
}

// secretTag is s's ETag: its version, quoted.
func secretTag(s secrets.Secret) string {
	return `"` + strconv.FormatInt(s.Version, 10) + `"`
}

// secretLocation is the URL of s, in the organization the path names.
func secretLocation(orgSlug string, s secrets.Secret) string {
	return BaseURL + "/orgs/" + url.PathEscape(orgSlug) + "/secrets/" + s.ID.String()
}

// secretFail maps the secrets' errors to problems, and leaves the rest to fail.
func secretFail(err error, kind string) error {
	switch {
	case errors.Is(err, secrets.ErrNotFound):
		return refuse(http.StatusNotFound, kind+".not_found", "There is no such "+kind+", or you may not see it.")
	case errors.Is(err, secrets.ErrChanged):
		return refuse(http.StatusPreconditionFailed, "precondition.failed", "The secret changed since you read it: read it again.")
	case errors.Is(err, secrets.ErrNameTaken):
		return refuse(http.StatusConflict, "secret.name_taken", "Another secret here has this name.")
	case errors.Is(err, secrets.ErrGranted):
		return refuse(http.StatusConflict, "grant.exists", "The principal already receives this secret.")
	case errors.Is(err, secrets.ErrNoSecretsKey):
		return refuse(http.StatusConflict, "secrets_key.missing", "This installation has no secrets key to seal secrets with: its operator sets one.")
	}
	return fail(err, kind)
}

// secretMatch is the version of a secret that If-Match names, secrets.AnyVersion for *, refusing with 428 a change
// that names none.
func secretMatch(header *IfMatchSecret) (int64, error) {
	match, err := ifMatch(header)
	switch {
	case err != nil:
		return 0, err
	case match == 0:
		return 0, refuse(http.StatusPreconditionRequired, "precondition.required", "Name the secret's ETag in If-Match.")
	case match == code.AnyVersion:
		return secrets.AnyVersion, nil
	}
	return match, nil
}

// ListSecrets lists the organization's own secrets.
func (a api) ListSecrets(ctx context.Context, req ListSecretsRequestObject) (ListSecretsResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	org, err := a.org(ctx, v, req.Org)
	if err != nil {
		return nil, err
	}
	list, err := a.secrets.List(ctx, v, org.ID, uuid.Nil)
	if err != nil {
		return nil, secretFail(err, "organization")
	}
	return ListSecrets200JSONResponse{Secrets: nonNil(mapAll(list, secret))}, nil
}

// CreateSecret adds a secret to the organization's settings.
func (a api) CreateSecret(ctx context.Context, req CreateSecretRequestObject) (CreateSecretResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	org, err := a.org(ctx, v, req.Org)
	if err != nil {
		return nil, err
	}
	s, err := a.secrets.Create(ctx, v, org.ID, uuid.Nil, secrets.Input{Name: req.Body.Name, Value: req.Body.Value})
	if err != nil {
		return nil, secretFail(err, "organization")
	}
	return CreateSecret201JSONResponse{Body: secret(s), Headers: CreateSecret201ResponseHeaders{ETag: secretTag(s), Location: secretLocation(req.Org, s)}}, nil
}

// ListRepositorySecrets lists the repository's own secrets.
func (a api) ListRepositorySecrets(ctx context.Context, req ListRepositorySecretsRequestObject) (ListRepositorySecretsResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	repo, _, err := a.repo(ctx, v, req.Org, req.Repo)
	if err != nil {
		return nil, err
	}
	list, err := a.secrets.List(ctx, v, repo.OrgID, repo.ID)
	if err != nil {
		return nil, secretFail(err, "repository")
	}
	return ListRepositorySecrets200JSONResponse{Secrets: nonNil(mapAll(list, secret))}, nil
}

// CreateRepositorySecret adds a secret to the repository's settings.
func (a api) CreateRepositorySecret(ctx context.Context, req CreateRepositorySecretRequestObject) (CreateRepositorySecretResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	repo, _, err := a.repo(ctx, v, req.Org, req.Repo)
	if err != nil {
		return nil, err
	}
	s, err := a.secrets.Create(ctx, v, repo.OrgID, repo.ID, secrets.Input{Name: req.Body.Name, Value: req.Body.Value})
	if err != nil {
		return nil, secretFail(err, "repository")
	}
	return CreateRepositorySecret201JSONResponse{Body: secret(s), Headers: CreateRepositorySecret201ResponseHeaders{ETag: secretTag(s), Location: secretLocation(req.Org, s)}}, nil
}

// GetSecret answers one of the organization's secrets, or 304 while the caller holds its version.
func (a api) GetSecret(ctx context.Context, req GetSecretRequestObject) (GetSecretResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	org, err := a.org(ctx, v, req.Org)
	if err != nil {
		return nil, err
	}
	s, err := a.secrets.Get(ctx, v, org.ID, req.Id)
	if err != nil {
		return nil, secretFail(err, "secret")
	}
	tag := secretTag(s)
	if matches(req.Params.IfNoneMatch, tag) {
		return GetSecret304Response{Headers: GetSecret304ResponseHeaders{ETag: tag}}, nil
	}
	return GetSecret200JSONResponse{Body: secret(s), Headers: GetSecret200ResponseHeaders{ETag: tag}}, nil
}

// ReplaceSecret renames a secret and, when the body gives one, replaces its value.
func (a api) ReplaceSecret(ctx context.Context, req ReplaceSecretRequestObject) (ReplaceSecretResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	org, err := a.org(ctx, v, req.Org)
	if err != nil {
		return nil, err
	}
	match, err := secretMatch(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	in := secrets.Input{Name: req.Body.Name}
	if req.Body.Value != nil {
		in.Value = *req.Body.Value
	}
	s, err := a.secrets.Replace(ctx, v, org.ID, req.Id, match, in)
	if err != nil {
		return nil, secretFail(err, "secret")
	}
	return ReplaceSecret200JSONResponse{Body: secret(s), Headers: ReplaceSecret200ResponseHeaders{ETag: secretTag(s)}}, nil
}

// DeleteSecret deletes a secret and every grant on it.
func (a api) DeleteSecret(ctx context.Context, req DeleteSecretRequestObject) (DeleteSecretResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	org, err := a.org(ctx, v, req.Org)
	if err != nil {
		return nil, err
	}
	match, err := secretMatch(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	if err := a.secrets.Delete(ctx, v, org.ID, req.Id, match); err != nil {
		return nil, secretFail(err, "secret")
	}
	return DeleteSecret204Response{}, nil
}

// ListSecretAccess lists who holds Secret reader on the secret itself.
func (a api) ListSecretAccess(ctx context.Context, req ListSecretAccessRequestObject) (ListSecretAccessResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	org, err := a.org(ctx, v, req.Org)
	if err != nil {
		return nil, err
	}
	list, err := a.secrets.Access(ctx, v, org.ID, req.Id)
	if err != nil {
		return nil, secretFail(err, "secret")
	}
	return ListSecretAccess200JSONResponse{Access: nonNil(mapAll(list, grantee))}, nil
}

// GrantSecret lets one of the organization's principals receive the secret.
func (a api) GrantSecret(ctx context.Context, req GrantSecretRequestObject) (GrantSecretResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	org, err := a.org(ctx, v, req.Org)
	if err != nil {
		return nil, err
	}
	if err := a.secrets.Grant(ctx, v, org.ID, req.Id, req.Body.PrincipalId); err != nil {
		return nil, secretFail(err, "secret")
	}
	return GrantSecret204Response{}, nil
}

// RevokeSecret takes Secret reader on the secret from a principal.
func (a api) RevokeSecret(ctx context.Context, req RevokeSecretRequestObject) (RevokeSecretResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	org, err := a.org(ctx, v, req.Org)
	if err != nil {
		return nil, err
	}
	if err := a.secrets.Revoke(ctx, v, org.ID, req.Id, req.PrincipalID); err != nil {
		return nil, secretFail(err, "grant")
	}
	return RevokeSecret204Response{}, nil
}

// ListRepositoryJobs lists the repository's jobs, the principals its secrets are granted to.
func (a api) ListRepositoryJobs(ctx context.Context, req ListRepositoryJobsRequestObject) (ListRepositoryJobsResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	repo, _, err := a.repo(ctx, v, req.Org, req.Repo)
	if err != nil {
		return nil, err
	}
	list, err := a.secrets.Jobs(ctx, v, repo.OrgID, repo.ID)
	if err != nil {
		return nil, secretFail(err, "repository")
	}
	return ListRepositoryJobs200JSONResponse{Jobs: nonNil(mapAll(list, grantee))}, nil
}
