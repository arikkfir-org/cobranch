package graphql

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/artifacts"
)

// Artifacts is what the API needs of the artifacts' service.
type Artifacts interface {
	List(ctx context.Context, v artifacts.Viewer, orgID, repoID uuid.UUID, kind artifacts.Kind) ([]artifacts.Artifact, error)
	ListOrganization(ctx context.Context, v artifacts.Viewer, orgID uuid.UUID, f artifacts.Filter) ([]artifacts.Artifact, error)
	Get(ctx context.Context, v artifacts.Viewer, orgID, id uuid.UUID) (artifacts.Artifact, error)
	Create(ctx context.Context, v artifacts.Viewer, orgID, repoID uuid.UUID, in artifacts.Input) (artifacts.Artifact, bool, error)
	Update(ctx context.Context, v artifacts.Viewer, orgID, id uuid.UUID, version int64, p artifacts.Patch) (artifacts.Artifact, error)
	Delete(ctx context.Context, v artifacts.Viewer, orgID, id uuid.UUID, version int64) error
	AddVersion(ctx context.Context, v artifacts.Viewer, orgID, id uuid.UUID, version int64, in artifacts.VersionInput) (artifacts.Artifact, artifacts.Version, bool, error)
	Versions(ctx context.Context, v artifacts.Viewer, orgID, id uuid.UUID) ([]artifacts.Version, error)
	Version(ctx context.Context, v artifacts.Viewer, orgID, id uuid.UUID, number int32) (artifacts.Artifact, artifacts.Version, error)
	Access(ctx context.Context, v artifacts.Viewer, orgID, id uuid.UUID) (artifacts.Access, error)
	Share(ctx context.Context, v artifacts.Viewer, orgID, id, principalID uuid.UUID, role artifacts.Role) error
	Unshare(ctx context.Context, v artifacts.Viewer, orgID, id, principalID uuid.UUID) error
	MyAccess(ctx context.Context, v artifacts.Viewer) ([]artifacts.Grant, error)
	ShareMine(ctx context.Context, v artifacts.Viewer, principalID uuid.UUID, role artifacts.Role) error
	UnshareMine(ctx context.Context, v artifacts.Viewer, principalID uuid.UUID) error
}

// artifactPrincipal is p as the API names it, nil for nobody.
func artifactPrincipal(p artifacts.Principal) *ArtifactPrincipal {
	if p.ID == uuid.Nil {
		return nil
	}
	return &ArtifactPrincipal{ID: p.ID.String(), DisplayName: p.DisplayName}
}

// taskNumber is a task's number, nil for none.
func taskNumber(n int64) *int64 {
	if n == 0 {
		return nil
	}
	return &n
}

// artifact is a as the API shows it; its versions and who reaches it resolve on their own.
func artifact(a artifacts.Artifact) *Artifact {
	var repo *ArtifactRepository
	if a.RepoID != uuid.Nil {
		repo = &ArtifactRepository{ID: a.RepoID.String(), Slug: a.RepoSlug}
	}
	return &Artifact{ID: a.ID.String(), OrganizationID: a.OrgID.String(), Repository: repo, Owner: artifactPrincipal(a.Owner), Title: a.Title, Summary: a.Summary, Kind: ArtifactKind(strings.ToUpper(string(a.Kind))),
		RepositoryAccess: reach(a.RepositoryAccess), OrganizationAccess: reach(a.OrganizationAccess),
		Audience: ArtifactAudience(strings.ToUpper(a.Audience())), Agent: artifactPrincipal(a.Agent), TaskNumber: taskNumber(a.TaskNumber),
		Branch: a.Branch, Head: int(a.Head), MediaType: a.MediaType, Version: int(a.Version), CreatedBy: artifactPrincipal(a.CreatedBy),
		CreatedAt: a.CreatedAt.UTC(), UpdatedBy: artifactPrincipal(a.UpdatedBy), UpdatedAt: a.UpdatedAt.UTC(), Permissions: a.Permissions}
}

// artifactVersion is ver as the API shows it, with its body and HTML only when it has them.
func artifactVersion(ver artifacts.Version) *ArtifactVersion {
	return &ArtifactVersion{ID: ver.ID.String(), ArtifactID: ver.ArtifactID.String(), Number: int(ver.Number), MediaType: ver.MediaType, Size: ver.Size,
		Author: artifactPrincipal(ver.Author), TaskNumber: taskNumber(ver.TaskNumber), Note: ver.Note, CreatedAt: ver.CreatedAt.UTC(),
		Body: nonEmpty(ver.Body), HTML: nonEmpty(ver.HTML)}
}

func artifactGrant(g artifacts.Grant) *ArtifactGrant {
	return &ArtifactGrant{PrincipalID: g.Principal.ID.String(), Kind: string(g.Principal.Kind), DisplayName: g.Principal.DisplayName,
		Role: ArtifactRole(strings.ToUpper(string(g.Role)))}
}

func reach(r artifacts.Reach) ArtifactReach {
	return ArtifactReach(strings.ToUpper(string(r)))
}

// reachOf is the domain's reach of r, "" when absent.
func reachOf(r *ArtifactReach) artifacts.Reach {
	if r == nil {
		return ""
	}
	return artifacts.Reach(strings.ToLower(string(*r)))
}

// reachPtr is the domain's reach of r, nil when absent.
func reachPtr(r *ArtifactReach) *artifacts.Reach {
	if r == nil {
		return nil
	}
	out := reachOf(r)
	return &out
}

// callID is the ID a call picks for what it makes, uuid.Nil when it picks none, or a BAD_USER_INPUT error.
func callID(ctx context.Context, id *string) (uuid.UUID, error) {
	if id == nil {
		return uuid.Nil, nil
	}
	parsed, err := uuid.Parse(*id)
	if err != nil {
		return uuid.Nil, refuse(ctx, badUserInput, "artifact.id_invalid", "an ID is a UUID")
	}
	return parsed, nil
}

// artifactFail maps the artifacts' errors to a field's, and leaves the rest to fail.
func artifactFail(ctx context.Context, err error, kind, what string) error {
	switch {
	case errors.Is(err, artifacts.ErrNotFound):
		return refuse(ctx, notFound, kind+".not_found", "there is no such "+kind+", or you may not see it")
	case errors.Is(err, artifacts.ErrChanged):
		return refuse(ctx, conflict, "precondition.failed", "the artifact changed since you read it: read it again")
	case errors.Is(err, artifacts.ErrExists):
		return refuse(ctx, conflict, "artifact.exists", "another artifact or version has this ID")
	}
	return fail(ctx, err, kind, what)
}

// artifactExpected is the version of an artifact that expectedVersion names, from 1: artifacts.AnyVersion without one.
func artifactExpected(ctx context.Context, expectedVersion *int) (int64, error) {
	if expectedVersion == nil {
		return artifacts.AnyVersion, nil
	}
	if *expectedVersion < 1 {
		return 0, refuse(ctx, badUserInput, "version.invalid", "a version of the artifact, from 1")
	}
	return int64(*expectedVersion), nil
}

// artifactTarget is the viewer and the organization and artifact a field names, by ID.
func artifactTarget(ctx context.Context, organizationID, id string) (artifacts.Viewer, uuid.UUID, uuid.UUID, error) {
	v, err := viewer(ctx)
	if err != nil {
		return artifacts.Viewer{}, uuid.Nil, uuid.Nil, err
	}
	orgID, err := parseID(ctx, organizationID, "organization")
	if err != nil {
		return artifacts.Viewer{}, uuid.Nil, uuid.Nil, err
	}
	artifactID, err := parseID(ctx, id, "artifact")
	if err != nil {
		return artifacts.Viewer{}, uuid.Nil, uuid.Nil, err
	}
	return v.User, orgID, artifactID, nil
}
