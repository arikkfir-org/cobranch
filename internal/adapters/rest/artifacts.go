package rest

import (
	"context"
	"errors"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/artifacts"
	"cobranch.dev/internal/services/code"
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

// artifactPolicy is an artifact's body's Content Security Policy: it runs nothing, loads data URLs and its own inline
// styles alone, and calls nowhere, so a page or a diagram an agent wrote can't act for whoever opens it.
const artifactPolicy = "sandbox; default-src 'none'; img-src data:; style-src 'unsafe-inline'"

// artifactTypes are the media types a version's body is served as, and the extensions of the files it downloads as.
var artifactTypes = map[string][2]string{
	artifacts.MediaMarkdown: {"text/markdown; charset=utf-8", ".md"},
	artifacts.MediaSVG:      {"image/svg+xml", ".svg"},
	artifacts.MediaHTML:     {"text/html; charset=utf-8", ".html"},
}

// artifactPrincipal is p as the API names it, nil for nobody.
func artifactPrincipal(p artifacts.Principal) *Principal {
	if p.ID == uuid.Nil {
		return nil
	}
	return &Principal{Id: p.ID, DisplayName: p.DisplayName}
}

// taskNumber is a task's number, nil for none.
func taskNumber(n int64) *int64 {
	if n == 0 {
		return nil
	}
	return &n
}

// artifact is a as the API shows it: to a caller who may only list it, its ID, organization, title, kind and owner.
func artifact(a artifacts.Artifact) Artifact {
	out := Artifact{Id: a.ID, OrgId: a.OrgID, Owner: artifactPrincipal(a.Owner), Title: a.Title, Kind: ArtifactKind(a.Kind)}
	if a.Permissions != nil {
		out.Permissions = &a.Permissions
	}
	if a.Withheld() {
		return out
	}
	repoAccess, orgAccess, audience, media := ArtifactReach(a.RepositoryAccess), ArtifactReach(a.OrganizationAccess), ArtifactAudience(a.Audience()),
		ArtifactMediaType(a.MediaType)
	created, updated := a.CreatedAt.UTC(), a.UpdatedAt.UTC()
	out.Summary, out.RepositoryAccess, out.OrganizationAccess, out.Audience = &a.Summary, &repoAccess, &orgAccess, &audience
	out.Agent, out.TaskNumber, out.Branch, out.Head, out.MediaType = artifactPrincipal(a.Agent), taskNumber(a.TaskNumber), &a.Branch, &a.Head, &media
	out.Version, out.CreatedBy, out.CreatedAt, out.UpdatedBy, out.UpdatedAt = &a.Version, artifactPrincipal(a.CreatedBy), &created,
		artifactPrincipal(a.UpdatedBy), &updated
	if a.RepoID != uuid.Nil {
		out.Repository = &ArtifactRepository{Id: a.RepoID, Slug: a.RepoSlug}
	}
	return out
}

// artifactVersion is ver as the API shows it, with its body and HTML only when it has them.
func artifactVersion(ver artifacts.Version) ArtifactVersion {
	out := ArtifactVersion{Id: ver.ID, ArtifactId: ver.ArtifactID, Number: ver.Number, MediaType: ArtifactMediaType(ver.MediaType), Size: ver.Size,
		Author: artifactPrincipal(ver.Author), TaskNumber: taskNumber(ver.TaskNumber), Note: ver.Note, CreatedAt: ver.CreatedAt.UTC()}
	if ver.Body != "" {
		out.Body = &ver.Body
	}
	if ver.HTML != "" {
		out.Html = &ver.HTML
	}
	return out
}

func artifactGrant(g artifacts.Grant) ArtifactGrant {
	return ArtifactGrant{PrincipalId: g.Principal.ID, Kind: ArtifactGrantKind(g.Principal.Kind), DisplayName: g.Principal.DisplayName,
		Role: ArtifactRole(g.Role)}
}

// artifactTag is a's ETag: its version, quoted.
func artifactTag(a artifacts.Artifact) string {
	return `"` + strconv.FormatInt(a.Version, 10) + `"`
}

// artifactVersionTag is ver's ETag: its ID, quoted, since a version never changes.
func artifactVersionTag(ver artifacts.Version) string {
	return `"` + ver.ID.String() + `"`
}

// artifactLocation is the URL of a, in the organization the path names.
func artifactLocation(orgSlug string, id uuid.UUID) string {
	return BaseURL + "/orgs/" + url.PathEscape(orgSlug) + "/artifacts/" + id.String()
}

// versionLocation is the URL of a's version number.
func versionLocation(orgSlug string, id uuid.UUID, number int32) string {
	return artifactLocation(orgSlug, id) + "/versions/" + strconv.Itoa(int(number))
}

// artifactFail maps the artifacts' errors to problems, and leaves the rest to fail.
func artifactFail(err error, kind string) error {
	switch {
	case errors.Is(err, artifacts.ErrNotFound):
		return refuse(http.StatusNotFound, kind+".not_found", "There is no such "+kind+", or you may not see it.")
	case errors.Is(err, artifacts.ErrChanged):
		return refuse(http.StatusPreconditionFailed, "precondition.failed", "The artifact changed since you read it: read it again.")
	case errors.Is(err, artifacts.ErrExists):
		return refuse(http.StatusConflict, "artifact.exists", "Another artifact or version has this ID.")
	}
	return fail(err, kind)
}

// artifactMatch is the version of an artifact that If-Match names, artifacts.AnyVersion for *, refusing with 428 a
// change that names none.
func artifactMatch(header *IfMatchArtifact) (int64, error) {
	match, err := ifMatch(header)
	switch {
	case err != nil:
		return 0, err
	case match == 0:
		return 0, refuse(http.StatusPreconditionRequired, "precondition.required", "Name the artifact's ETag in If-Match.")
	case match == code.AnyVersion:
		return artifacts.AnyVersion, nil
	}
	return match, nil
}

// orNone is *s, or "" when absent.
func orNone[T ~string](s *T) T {
	if s == nil {
		return ""
	}
	return *s
}

// ListArtifacts lists the repository's artifacts that the caller reads.
func (a api) ListArtifacts(ctx context.Context, req ListArtifactsRequestObject) (ListArtifactsResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	repo, _, err := a.repo(ctx, v, req.Org, req.Repo)
	if err != nil {
		return nil, err
	}
	list, err := a.artifacts.List(ctx, v, repo.OrgID, repo.ID, artifacts.Kind(orNone(req.Params.Kind)))
	if err != nil {
		return nil, artifactFail(err, "repository")
	}
	return ListArtifacts200JSONResponse{Artifacts: nonNil(mapAll(list, artifact))}, nil
}

// ListOrganizationArtifacts lists the organization's artifacts that the caller may list or read.
func (a api) ListOrganizationArtifacts(ctx context.Context, req ListOrganizationArtifactsRequestObject) (ListOrganizationArtifactsResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	org, err := a.org(ctx, v, req.Org)
	if err != nil {
		return nil, err
	}
	f := artifacts.Filter{Kind: artifacts.Kind(orNone(req.Params.Kind)), Mine: req.Params.Mine != nil && *req.Params.Mine}
	if req.Params.RepositoryId != nil {
		f.RepoID = *req.Params.RepositoryId
	}
	list, err := a.artifacts.ListOrganization(ctx, v, org.ID, f)
	if err != nil {
		return nil, artifactFail(err, "organization")
	}
	return ListOrganizationArtifacts200JSONResponse{Artifacts: nonNil(mapAll(list, artifact))}, nil
}

// artifactInput is what b makes.
func artifactInput(b NewArtifact) artifacts.Input {
	in := artifacts.Input{Title: b.Title, Summary: orNone(b.Summary), Kind: artifacts.Kind(b.Kind),
		RepositoryAccess: artifacts.Reach(orNone(b.RepositoryAccess)), OrganizationAccess: artifacts.Reach(orNone(b.OrganizationAccess)),
		Branch: orNone(b.Branch), MediaType: string(b.MediaType), Body: b.Body, Note: orNone(b.Note)}
	if b.Id != nil {
		in.ID = *b.Id
	}
	return in
}

// CreateOrganizationArtifact makes an artifact attached to no repository with its first version, or answers the one
// the body's ID made.
func (a api) CreateOrganizationArtifact(ctx context.Context, req CreateOrganizationArtifactRequestObject) (CreateOrganizationArtifactResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	org, err := a.org(ctx, v, req.Org)
	if err != nil {
		return nil, err
	}
	made, created, err := a.artifacts.Create(ctx, v, org.ID, uuid.Nil, artifactInput(*req.Body))
	if err != nil {
		return nil, artifactFail(err, "organization")
	}
	tag, location := artifactTag(made), artifactLocation(req.Org, made.ID)
	if !created {
		return CreateOrganizationArtifact200JSONResponse{Body: artifact(made), Headers: CreateOrganizationArtifact200ResponseHeaders{ETag: tag, Location: location}}, nil
	}
	return CreateOrganizationArtifact201JSONResponse{Body: artifact(made), Headers: CreateOrganizationArtifact201ResponseHeaders{ETag: tag, Location: location}}, nil
}

// CreateArtifact makes an artifact attached to the repository with its first version, or answers the one the body's
// ID made.
func (a api) CreateArtifact(ctx context.Context, req CreateArtifactRequestObject) (CreateArtifactResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	repo, _, err := a.repo(ctx, v, req.Org, req.Repo)
	if err != nil {
		return nil, err
	}
	made, created, err := a.artifacts.Create(ctx, v, repo.OrgID, repo.ID, artifactInput(*req.Body))
	if err != nil {
		return nil, artifactFail(err, "repository")
	}
	tag, location := artifactTag(made), artifactLocation(req.Org, made.ID)
	if !created {
		return CreateArtifact200JSONResponse{Body: artifact(made), Headers: CreateArtifact200ResponseHeaders{ETag: tag, Location: location}}, nil
	}
	return CreateArtifact201JSONResponse{Body: artifact(made), Headers: CreateArtifact201ResponseHeaders{ETag: tag, Location: location}}, nil
}

// GetArtifact answers one of the organization's artifacts, or 304 while the caller holds its version.
func (a api) GetArtifact(ctx context.Context, req GetArtifactRequestObject) (GetArtifactResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	org, err := a.org(ctx, v, req.Org)
	if err != nil {
		return nil, err
	}
	got, err := a.artifacts.Get(ctx, v, org.ID, req.Id)
	if err != nil {
		return nil, artifactFail(err, "artifact")
	}
	tag := artifactTag(got)
	if matches(req.Params.IfNoneMatch, tag) {
		return GetArtifact304Response{Headers: GetArtifact304ResponseHeaders{ETag: tag}}, nil
	}
	return GetArtifact200JSONResponse{Body: artifact(got), Headers: GetArtifact200ResponseHeaders{ETag: tag}}, nil
}

// UpdateArtifact changes the fields the merge patch names.
func (a api) UpdateArtifact(ctx context.Context, req UpdateArtifactRequestObject) (UpdateArtifactResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	match, err := artifactMatch(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	org, err := a.org(ctx, v, req.Org)
	if err != nil {
		return nil, err
	}
	b := req.Body
	p := artifacts.Patch{Title: b.Title, Summary: b.Summary}
	if b.RepositoryAccess != nil {
		r := artifacts.Reach(*b.RepositoryAccess)
		p.RepositoryAccess = &r
	}
	if b.OrganizationAccess != nil {
		r := artifacts.Reach(*b.OrganizationAccess)
		p.OrganizationAccess = &r
	}
	updated, err := a.artifacts.Update(ctx, v, org.ID, req.Id, match, p)
	if err != nil {
		return nil, artifactFail(err, "artifact")
	}
	return UpdateArtifact200JSONResponse{Body: artifact(updated), Headers: UpdateArtifact200ResponseHeaders{ETag: artifactTag(updated)}}, nil
}

// DeleteArtifact deletes an artifact, its versions and every grant on it.
func (a api) DeleteArtifact(ctx context.Context, req DeleteArtifactRequestObject) (DeleteArtifactResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	match, err := artifactMatch(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	org, err := a.org(ctx, v, req.Org)
	if err != nil {
		return nil, err
	}
	if err := a.artifacts.Delete(ctx, v, org.ID, req.Id, match); err != nil {
		return nil, artifactFail(err, "artifact")
	}
	return DeleteArtifact204Response{}, nil
}

// ListArtifactVersions lists an artifact's versions, newest first, without their bodies.
func (a api) ListArtifactVersions(ctx context.Context, req ListArtifactVersionsRequestObject) (ListArtifactVersionsResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	org, err := a.org(ctx, v, req.Org)
	if err != nil {
		return nil, err
	}
	list, err := a.artifacts.Versions(ctx, v, org.ID, req.Id)
	if err != nil {
		return nil, artifactFail(err, "artifact")
	}
	return ListArtifactVersions200JSONResponse{Versions: nonNil(mapAll(list, artifactVersion))}, nil
}

// AddArtifactVersion adds an artifact's next version, or answers the one the body's ID added.
func (a api) AddArtifactVersion(ctx context.Context, req AddArtifactVersionRequestObject) (AddArtifactVersionResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	match, err := artifactMatch(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	org, err := a.org(ctx, v, req.Org)
	if err != nil {
		return nil, err
	}
	in := artifacts.VersionInput{MediaType: string(req.Body.MediaType), Body: req.Body.Body, Note: orNone(req.Body.Note)}
	if req.Body.Id != nil {
		in.ID = *req.Body.Id
	}
	updated, ver, added, err := a.artifacts.AddVersion(ctx, v, org.ID, req.Id, match, in)
	if err != nil {
		return nil, artifactFail(err, "artifact")
	}
	tag, location := artifactTag(updated), versionLocation(req.Org, req.Id, ver.Number)
	if !added {
		return AddArtifactVersion200JSONResponse{Body: artifactVersion(ver), Headers: AddArtifactVersion200ResponseHeaders{ETag: tag, Location: location}}, nil
	}
	return AddArtifactVersion201JSONResponse{Body: artifactVersion(ver), Headers: AddArtifactVersion201ResponseHeaders{ETag: tag, Location: location}}, nil
}

// GetArtifactVersion answers a version of an artifact with its body, or 304 while the caller holds it.
func (a api) GetArtifactVersion(ctx context.Context, req GetArtifactVersionRequestObject) (GetArtifactVersionResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	org, err := a.org(ctx, v, req.Org)
	if err != nil {
		return nil, err
	}
	_, ver, err := a.artifacts.Version(ctx, v, org.ID, req.Id, req.Version)
	if err != nil {
		return nil, artifactFail(err, "artifact")
	}
	tag := artifactVersionTag(ver)
	if matches(req.Params.IfNoneMatch, tag) {
		return GetArtifactVersion304Response{Headers: GetArtifactVersion304ResponseHeaders{ETag: tag}}, nil
	}
	return GetArtifactVersion200JSONResponse{Body: artifactVersion(ver), Headers: GetArtifactVersion200ResponseHeaders{ETag: tag}}, nil
}

// GetArtifactVersionRaw answers a version's body as its format, sandboxed so it runs nothing, or 304 while the caller
// holds it.
func (a api) GetArtifactVersionRaw(ctx context.Context, req GetArtifactVersionRawRequestObject) (GetArtifactVersionRawResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	org, err := a.org(ctx, v, req.Org)
	if err != nil {
		return nil, err
	}
	got, ver, err := a.artifacts.Version(ctx, v, org.ID, req.Id, req.Version)
	if err != nil {
		return nil, artifactFail(err, "artifact")
	}
	const cache = "private, no-cache"
	tag := artifactVersionTag(ver)
	if matches(req.Params.IfNoneMatch, tag) {
		return GetArtifactVersionRaw304Response{Headers: GetArtifactVersionRaw304ResponseHeaders{ETag: tag, CacheControl: cache}}, nil
	}
	media := artifactTypes[ver.MediaType]
	headers := GetArtifactVersionRaw200ResponseHeaders{ETag: tag, CacheControl: cache, ContentSecurityPolicy: artifactPolicy,
		XContentTypeOptions: "nosniff", CrossOriginResourcePolicy: "same-site"}
	if req.Params.Download != nil && *req.Params.Download {
		disposition := mime.FormatMediaType("attachment", map[string]string{"filename": fileName(got.Title) + media[1]})
		headers.ContentDisposition = &disposition
	}
	return GetArtifactVersionRaw200AsteriskResponse{Body: strings.NewReader(ver.Body), Headers: headers, ContentType: media[0],
		ContentLength: int64(len(ver.Body))}, nil
}

// fileName is a file's name for title: its path separators and control characters dashes, "artifact" for none.
func fileName(title string) string {
	name := strings.TrimSpace(strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || unicode.IsControl(r) {
			return '-'
		}
		return r
	}, title))
	if name == "" || name == "." || name == ".." {
		return "artifact"
	}
	return name
}

// GetArtifactAccess answers who reaches an artifact.
func (a api) GetArtifactAccess(ctx context.Context, req GetArtifactAccessRequestObject) (GetArtifactAccessResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	org, err := a.org(ctx, v, req.Org)
	if err != nil {
		return nil, err
	}
	access, err := a.artifacts.Access(ctx, v, org.ID, req.Id)
	if err != nil {
		return nil, artifactFail(err, "artifact")
	}
	return GetArtifactAccess200JSONResponse{RepositoryAccess: ArtifactReach(access.RepositoryAccess),
		OrganizationAccess: ArtifactReach(access.OrganizationAccess), Grants: nonNil(mapAll(access.Grants, artifactGrant))}, nil
}

// ShareArtifact gives a principal a role on an artifact.
func (a api) ShareArtifact(ctx context.Context, req ShareArtifactRequestObject) (ShareArtifactResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	org, err := a.org(ctx, v, req.Org)
	if err != nil {
		return nil, err
	}
	if err := a.artifacts.Share(ctx, v, org.ID, req.Id, req.Body.PrincipalId, artifacts.Role(req.Body.Role)); err != nil {
		return nil, artifactFail(err, "artifact")
	}
	return ShareArtifact204Response{}, nil
}

// UnshareArtifact takes every role a principal holds on an artifact itself.
func (a api) UnshareArtifact(ctx context.Context, req UnshareArtifactRequestObject) (UnshareArtifactResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	org, err := a.org(ctx, v, req.Org)
	if err != nil {
		return nil, err
	}
	if err := a.artifacts.Unshare(ctx, v, org.ID, req.Id, req.PrincipalID); err != nil {
		return nil, artifactFail(err, "grant")
	}
	return UnshareArtifact204Response{}, nil
}

// ListMyArtifactAccess lists who lists or reads every artifact the caller owns.
func (a api) ListMyArtifactAccess(ctx context.Context, _ ListMyArtifactAccessRequestObject) (ListMyArtifactAccessResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	grants, err := a.artifacts.MyAccess(ctx, v)
	if err != nil {
		return nil, artifactFail(err, "grant")
	}
	return ListMyArtifactAccess200JSONResponse{Grants: nonNil(mapAll(grants, artifactGrant))}, nil
}

// ShareMyArtifacts gives a principal lister or viewer on the caller.
func (a api) ShareMyArtifacts(ctx context.Context, req ShareMyArtifactsRequestObject) (ShareMyArtifactsResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	if err := a.artifacts.ShareMine(ctx, v, req.Body.PrincipalId, artifacts.Role(req.Body.Role)); err != nil {
		return nil, artifactFail(err, "principal")
	}
	return ShareMyArtifacts204Response{}, nil
}

// UnshareMyArtifacts takes every role a principal holds on the caller.
func (a api) UnshareMyArtifacts(ctx context.Context, req UnshareMyArtifactsRequestObject) (UnshareMyArtifactsResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	if err := a.artifacts.UnshareMine(ctx, v, req.PrincipalID); err != nil {
		return nil, artifactFail(err, "grant")
	}
	return UnshareMyArtifacts204Response{}, nil
}
