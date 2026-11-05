// Package artifacts keeps the documents, diagrams, reports and pages that agents write when asked: each the person's
// who asked for it, attached to a repository or to none, kept in versions that never change, and reached through grants
// on it, on its owner, on its repository and at its organization, and through its two audiences
// (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/artifacts.md).
// An artifact's body is untrusted: nothing here runs it, and the adapters serve it only as something that runs nothing.
package artifacts

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/orgs"
)

// Kind is what an artifact is for; it picks its icon and its filter, never its format.
type Kind string

// The kinds.
const (
	KindDocument Kind = "document"
	KindDiagram  Kind = "diagram"
	KindReport   Kind = "report"
	KindPage     Kind = "page"
)

// Kinds lists the kinds, in the order the Console shows them.
var Kinds = []Kind{KindDocument, KindDiagram, KindReport, KindPage}

// Reach is how far an audience reaches an artifact: not at all, to view it, or to view and edit it.
type Reach string

// The reaches.
const (
	ReachNone Reach = "none"
	ReachView Reach = "view"
	ReachEdit Reach = "edit"
)

// The formats a version is written in.
const (
	MediaMarkdown = "text/markdown"
	MediaSVG      = "image/svg+xml"
	MediaHTML     = "text/html"
)

// MediaTypes lists the formats.
var MediaTypes = []string{MediaMarkdown, MediaSVG, MediaHTML}

// The limits of what an artifact and a version hold.
const (
	MaxTitleLength   = 200
	MaxSummaryLength = 500
	MaxNoteLength    = 500
	MaxBranchLength  = 255
	MaxBodyBytes     = 1 << 20
)

// AnyVersion changes or deletes whichever version of an artifact is there (If-Match: *).
const AnyVersion int64 = -1

// The permissions this package checks.
const (
	// PermCreate creates artifacts attached to a repository.
	PermCreate = "artifacts.create"
	// PermList shows an artifact in lists, without opening it.
	PermList = "artifacts.list"
	// PermRead views an artifact, its versions and who reaches it.
	PermRead = "artifacts.read"
	// PermWrite adds an artifact's versions and changes its title and summary.
	PermWrite = "artifacts.write"
	// PermManage changes an artifact's audiences, shares and unshares it, and deletes it.
	PermManage = "artifacts.manage"
)

// Role is a role an artifact or a person grants, by its name in the API.
type Role string

// The roles an artifact grants, from viewing to managing, and the role that lists a person's artifacts.
const (
	RoleViewer Role = "viewer"
	RoleEditor Role = "editor"
	RoleOwner  Role = "owner"
	RoleLister Role = "lister"
)

// roleIDs are the built-in roles behind the API's role names.
var roleIDs = map[Role]uuid.UUID{RoleViewer: auth.RoleArtifactViewer, RoleEditor: auth.RoleArtifactEditor, RoleOwner: auth.RoleArtifactOwner,
	RoleLister: auth.RoleArtifactLister}

// rolePermissions are what each role holds, as migrations 49 and 55 define them.
var rolePermissions = map[Role][]string{
	RoleViewer: {PermRead},
	RoleEditor: {PermRead, PermWrite},
	RoleOwner:  {PermRead, PermWrite, PermManage},
	RoleLister: {PermList},
}

// RoleID is the built-in role behind r, and whether r names one.
func RoleID(r Role) (uuid.UUID, bool) {
	id, ok := roleIDs[r]
	return id, ok
}

// RoleOf is the API's name of the built-in role id, and whether it is one of the artifact roles.
func RoleOf(id uuid.UUID) (Role, bool) {
	for r, rid := range roleIDs {
		if rid == id {
			return r, true
		}
	}
	return "", false
}

// The errors the domain answers; an adapter maps each to its status.
var (
	// ErrNotFound is an artifact, a version, a repository or a principal that isn't there, or that the viewer may not
	// see.
	ErrNotFound = errors.New("not found")
	// ErrExists is an ID another principal or another repository's artifact took.
	ErrExists = errors.New("another artifact or version has this ID")
	// ErrChanged is a change that names a version of the artifact it no longer is.
	ErrChanged = errors.New("the artifact changed since it was read")
)

// Principal is one of the organization's principals as an artifact names it: who wrote it or a version of it, or who
// holds a role on it.
type Principal struct {
	ID          uuid.UUID
	Kind        auth.Kind
	DisplayName string
}

// Artifact is an artifact as its readers see it: everything but its versions' bodies.
type Artifact struct {
	ID      uuid.UUID
	OrgID   uuid.UUID
	OrgSlug string
	// RepoID and RepoSlug are the repository it is attached to; zero for none.
	RepoID   uuid.UUID
	RepoSlug string
	// Owner is whose it is: the person who asked for it, or the agent of a task Cobranch started; zero once they are
	// gone.
	Owner   Principal
	Title   string
	Summary string
	Kind    Kind
	// RepositoryAccess and OrganizationAccess are its audiences: the principals reading its repository, and those
	// reading its organization.
	RepositoryAccess   Reach
	OrganizationAccess Reach
	// Agent is the agent that wrote it, and TaskID and TaskNumber the task it wrote it in; zero when a person or a
	// script wrote it, or once they are gone.
	Agent      Principal
	TaskID     uuid.UUID
	TaskNumber int64
	// Branch is the branch it came from; empty for none.
	Branch string
	// Head is its newest version's number, and MediaType that version's format.
	Head      int32
	MediaType string
	// Version counts the artifact's changes: its ETag.
	Version   int64
	CreatedBy Principal
	CreatedAt time.Time
	UpdatedBy Principal
	UpdatedAt time.Time
	// Permissions are those the viewer holds on it, sorted: Get answers them all, a list which of artifacts.list and
	// artifacts.read, so that one listed without artifacts.read shows as one that doesn't open.
	Permissions []string
}

// Audience is the widest audience it has, as the list's badge names it: "organization", "repository" or "private".
func (a Artifact) Audience() string {
	switch {
	case a.OrganizationAccess != ReachNone:
		return "organization"
	case a.RepositoryAccess != ReachNone:
		return "repository"
	}
	return "private"
}

// Version is a version of an artifact.
type Version struct {
	ID         uuid.UUID
	ArtifactID uuid.UUID
	Number     int32
	MediaType  string
	// Size is its body's length in bytes.
	Size       int64
	Author     Principal
	TaskID     uuid.UUID
	TaskNumber int64
	Note       string
	CreatedAt  time.Time
	// Body is its text, and HTML a Markdown body rendered, sanitized, as the Console shows it; only Get answers them.
	Body string
	HTML string
}

// Grant is a role a principal holds on an artifact itself, or on a person.
type Grant struct {
	Principal Principal
	Role      Role
}

// Access is who reaches an artifact: its audiences, and the roles principals hold on it itself. Grants on its
// repository and at its organization reach it too.
type Access struct {
	RepositoryAccess   Reach
	OrganizationAccess Reach
	Grants             []Grant
}

// Input is what makes an artifact with its first version.
type Input struct {
	// ID names the artifact, and its first version: the call that makes it, so that the call run again makes nothing
	// more; uuid.Nil makes a new one.
	ID                 uuid.UUID
	Title              string
	Summary            string
	Kind               Kind
	RepositoryAccess   Reach
	OrganizationAccess Reach
	// Branch is the branch it came from; an agent's task names its own when it names none.
	Branch    string
	MediaType string
	Body      string
	Note      string
}

// VersionInput is what adds a version.
type VersionInput struct {
	// ID names the version: the call that adds it, as Input's; uuid.Nil makes a new one.
	ID        uuid.UUID
	MediaType string
	Body      string
	Note      string
}

// Patch is what changes an artifact's row, each field only when given: its title and summary take artifacts.write, its
// audiences artifacts.manage.
type Patch struct {
	Title              *string
	Summary            *string
	RepositoryAccess   *Reach
	OrganizationAccess *Reach
}

// Changes is an artifact's row as a change leaves it.
type Changes struct {
	Title              string
	Summary            string
	RepositoryAccess   Reach
	OrganizationAccess Reach
}

// New is an artifact as the store writes it, with its first version and the roles its makers get on it.
type New struct {
	Artifact Artifact
	First    VersionInput
	Owners   []NewGrant
}

// Filter narrows a list of the organization's artifacts.
type Filter struct {
	// RepoID keeps those attached to the repository; uuid.Nil keeps all.
	RepoID uuid.UUID
	// Kind keeps those of the kind; "" keeps all.
	Kind Kind
	// Mine keeps the viewer's own.
	Mine bool
}

// NewGrant is a role a principal gets on a new artifact.
type NewGrant struct {
	PrincipalID uuid.UUID
	RoleID      uuid.UUID
}

func invalid(field, code, message string) error {
	return &orgs.ValidationError{Field: field, Code: code, Message: message}
}

// text checks a line of text people read: at most limit characters, and no control characters.
func text(s string, limit int) bool {
	return utf8.RuneCountInString(s) <= limit && strings.IndexFunc(s, unicode.IsControl) < 0
}

// Validate checks what makes an artifact and returns it normalized: its title trimmed, and its audiences none when
// left out.
func Validate(in Input) (Input, error) {
	in.Title, in.Summary, in.Branch = strings.TrimSpace(in.Title), strings.TrimSpace(in.Summary), strings.TrimSpace(in.Branch)
	if in.RepositoryAccess == "" {
		in.RepositoryAccess = ReachNone
	}
	if in.OrganizationAccess == "" {
		in.OrganizationAccess = ReachNone
	}
	if err := validateRow(Changes{Title: in.Title, Summary: in.Summary, RepositoryAccess: in.RepositoryAccess, OrganizationAccess: in.OrganizationAccess}); err != nil {
		return in, err
	}
	switch {
	case !validKind(in.Kind):
		return in, invalid("kind", "artifact.kind_invalid", "an artifact is a document, a diagram, a report or a page")
	case len(in.Branch) > MaxBranchLength || strings.IndexFunc(in.Branch, unicode.IsControl) >= 0:
		return in, invalid("branch", "artifact.branch_invalid", fmt.Sprintf("a branch's name is at most %d bytes, none of them control characters", MaxBranchLength))
	}
	v, err := ValidateVersion(VersionInput{ID: in.ID, MediaType: in.MediaType, Body: in.Body, Note: in.Note})
	if err != nil {
		return in, err
	}
	in.Note = v.Note
	return in, nil
}

// validateRow checks an artifact's row as a change leaves it.
func validateRow(c Changes) error {
	switch {
	case c.Title == "" || !text(c.Title, MaxTitleLength):
		return invalid("title", "artifact.title_invalid", fmt.Sprintf("an artifact's title is 1 to %d characters, none of them control characters", MaxTitleLength))
	case utf8.RuneCountInString(c.Summary) > MaxSummaryLength:
		return invalid("summary", "artifact.summary_invalid", fmt.Sprintf("an artifact's summary is at most %d characters", MaxSummaryLength))
	case !validReach(c.RepositoryAccess):
		return invalid("repositoryAccess", "artifact.access_invalid", "an audience reaches none, view or edit")
	case !validReach(c.OrganizationAccess):
		return invalid("organizationAccess", "artifact.access_invalid", "an audience reaches none, view or edit")
	}
	return nil
}

// ValidateVersion checks what adds a version and returns it normalized, its note trimmed.
func ValidateVersion(in VersionInput) (VersionInput, error) {
	in.Note = strings.TrimSpace(in.Note)
	switch {
	case !validMediaType(in.MediaType):
		return in, invalid("mediaType", "artifact.media_type_invalid", "a version is text/markdown, image/svg+xml or text/html")
	case in.Body == "" || len(in.Body) > MaxBodyBytes || !utf8.ValidString(in.Body) || strings.ContainsRune(in.Body, 0):
		return in, invalid("body", "artifact.body_invalid", fmt.Sprintf("a version's body is 1 byte to %d bytes of UTF-8 text, without NUL characters", MaxBodyBytes))
	case utf8.RuneCountInString(in.Note) > MaxNoteLength:
		return in, invalid("note", "artifact.note_invalid", fmt.Sprintf("a version's note is at most %d characters", MaxNoteLength))
	}
	return in, nil
}

func validKind(k Kind) bool {
	for _, known := range Kinds {
		if k == known {
			return true
		}
	}
	return false
}

func validReach(r Reach) bool {
	return r == ReachNone || r == ReachView || r == ReachEdit
}

func validMediaType(t string) bool {
	for _, known := range MediaTypes {
		if t == known {
			return true
		}
	}
	return false
}

// Store is what the database keeps for artifacts. Each write is one transaction, which acts for actor, so the audit
// log records who changed each row, and records the organization's event.
type Store interface {
	// With lists the IDs of the organization's artifacts, or of those attached to the repository when repoID isn't
	// uuid.Nil, where the principal's grants, its own and the artifacts' audiences give it permission.
	With(ctx context.Context, principalID, orgID, repoID uuid.UUID, permission string) ([]uuid.UUID, error)
	// List reads the artifacts ids names, newest change first.
	List(ctx context.Context, ids []uuid.UUID) ([]Artifact, error)
	// Get finds an artifact, or ErrNotFound.
	Get(ctx context.Context, id uuid.UUID) (Artifact, error)
	// Create writes the artifact, its first version and its makers' roles: ErrNotFound when its repository, if any,
	// isn't its organization's, ErrExists when its ID or its version's is taken.
	Create(ctx context.Context, actor uuid.UUID, n New) (Artifact, error)
	// Update changes the artifact's row if it is still at version (any with AnyVersion): ErrChanged otherwise,
	// ErrNotFound when it's gone.
	Update(ctx context.Context, actor uuid.UUID, id uuid.UUID, version int64, c Changes) (Artifact, error)
	// Delete deletes the artifact, its versions and the grants on it if it is still at version: ErrChanged otherwise,
	// ErrNotFound when it's gone.
	Delete(ctx context.Context, actor uuid.UUID, id uuid.UUID, version int64) error
	// AddVersion adds the artifact's next version, written in taskID (uuid.Nil for none), if it is still at version:
	// ErrChanged otherwise, ErrNotFound when it's gone, ErrExists when the version's ID is taken.
	AddVersion(ctx context.Context, actor uuid.UUID, id uuid.UUID, version int64, in VersionInput, taskID uuid.UUID) (Artifact, error)
	// Versions lists the artifact's versions, newest first, without their bodies.
	Versions(ctx context.Context, id uuid.UUID) ([]Version, error)
	// Version finds a version of the artifact with its body, or ErrNotFound.
	Version(ctx context.Context, id uuid.UUID, number int32) (Version, error)
	// VersionByID finds the version a call wrote, by its ID, with its body, or ErrNotFound.
	VersionByID(ctx context.Context, versionID uuid.UUID) (Version, error)
	// Grants lists the roles principals hold on the artifact itself, by display name.
	Grants(ctx context.Context, id uuid.UUID) ([]Grant, error)
	// Principal finds one of the organization's members, or one of its own principals, or ErrNotFound.
	Principal(ctx context.Context, orgID, principalID uuid.UUID) (Principal, error)
	// Share gives the principal the role on the artifact in place of any artifact role it held there, and records it.
	Share(ctx context.Context, actor uuid.UUID, a Artifact, principalID, roleID uuid.UUID) error
	// Unshare takes every role the principal holds on the artifact itself and records it, or ErrNotFound when it holds
	// none.
	Unshare(ctx context.Context, actor uuid.UUID, a Artifact, principalID uuid.UUID) error
	// PersonGrants lists the roles principals hold on the user, by display name: those that reach the artifacts the
	// user owns.
	PersonGrants(ctx context.Context, userID uuid.UUID) ([]Grant, error)
	// Colleague finds a user who is a member of one of the user's organizations, or an agent of one, or ErrNotFound.
	Colleague(ctx context.Context, userID, principalID uuid.UUID) (Principal, error)
	// SharePerson gives the principal the role on actor, a user, in place of any role it held there.
	SharePerson(ctx context.Context, actor, principalID, roleID uuid.UUID) error
	// UnsharePerson takes every role the principal holds on actor, a user, or ErrNotFound when it holds none.
	UnsharePerson(ctx context.Context, actor, principalID uuid.UUID) error
}
