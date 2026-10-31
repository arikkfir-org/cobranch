// Package repos is the domain of repositories: an organization's Git repositories, their slugs and settings, and the
// grants that reach them. It knows nothing of HTTP, Git's protocol or PostgreSQL: adapters bring those through its
// port, Store, and every permission check goes through sign-in's authorizer
// (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/git-hosting.md, "Repositories").
package repos

import (
	"errors"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/orgs"
)

// Visibility is who reaches a repository, as its grants say: the API infers it, and nobody sets it.
type Visibility string

// The visibilities. Public needs grants to Anonymous, which come later with the settings that refuse them.
const (
	// VisibilityPrivate is a repository only its creator and its organization's Owners reach.
	VisibilityPrivate Visibility = "private"
	// VisibilityInternal is a repository someone else reaches too, through a grant on it or a role at the
	// organization.
	VisibilityInternal Visibility = "internal"
)

// ObjectFormatSHA1 is the object format of every repository for now; SHA-256 repositories arrive later, with no
// migration of the data.
const ObjectFormatSHA1 = "sha1"

// DefaultBranch is the branch a repository opens on unless its creator names another.
const DefaultBranch = "main"

// Repository is one of an organization's Git repositories: a slug for its URLs, unique in the organization, a name,
// a description and the branch it opens on.
type Repository struct {
	ID            uuid.UUID
	OrgID         uuid.UUID
	Slug          string
	Name          string
	Description   string
	DefaultBranch string
	ObjectFormat  string
	// CreatedBy is who created it; uuid.Nil once they are gone.
	CreatedBy  uuid.UUID
	CreatedAt  time.Time
	Visibility Visibility
}

// Input is what makes or changes a repository. An empty Name is the slug; an empty DefaultBranch is DefaultBranch.
type Input struct {
	Slug          string
	Name          string
	Description   string
	DefaultBranch string
}

// Principal is who may hold grants on an organization's repositories: one of its members, or one of its API keys.
type Principal struct {
	ID          uuid.UUID
	Kind        auth.Kind
	DisplayName string
}

// Access is a principal's grants on a repository: the roles it holds there.
type Access struct {
	Principal Principal
	Roles     []orgs.Role
}

// The errors the domain answers; an adapter maps each to its status.
var (
	// ErrNotFound is a repository, or a principal it may grant to, that isn't there, or a repository the viewer may
	// not see.
	ErrNotFound = errors.New("not found")
	// ErrSlugTaken is a slug another repository of the organization has.
	ErrSlugTaken = errors.New("another repository of this organization has this slug")
	// ErrGranted is a role the principal already holds on the repository.
	ErrGranted = errors.New("the principal already holds this role on the repository")
)

// slugPattern is a letter or digit, then letters, digits, hyphens, dots and underscores.
var slugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// ReservedSlugs are the Console's pages under an organization, which no repository may take.
var ReservedSlugs = []string{"members", "roles", "api-keys", "settings", "repos", "new"}

// ValidateSlug reports whether slug may name a repository: 1 to 100 lowercase letters, digits, hyphens, dots and
// underscores, starting with a letter or digit, not ending in .git, and none of the Console's pages.
func ValidateSlug(slug string) error {
	switch {
	case len(slug) > 100 || !slugPattern.MatchString(slug) || strings.HasSuffix(slug, ".git"):
		return &orgs.ValidationError{Field: "slug", Code: "slug.invalid",
			Message: "a slug is 1 to 100 lowercase letters, digits, hyphens, dots and underscores, starting with a letter or digit and not ending in .git"}
	case slices.Contains(ReservedSlugs, slug):
		return &orgs.ValidationError{Field: "slug", Code: "slug.reserved", Message: "this slug is one of the Console's pages"}
	}
	return nil
}

// ValidateBranch reports whether branch may name a branch, as git check-ref-format says of refs/heads/<branch>.
func ValidateBranch(branch string) error {
	if !validBranch(branch) {
		return &orgs.ValidationError{Field: "defaultBranch", Code: "branch.invalid", Message: "not a branch name Git accepts"}
	}
	return nil
}

// validBranch applies git check-ref-format's rules to a branch's name, at most 255 bytes long.
func validBranch(b string) bool {
	if b == "" || len(b) > 255 || b == "@" || strings.HasPrefix(b, "-") || strings.HasSuffix(b, ".") ||
		strings.HasPrefix(b, "/") || strings.HasSuffix(b, "/") ||
		strings.Contains(b, "..") || strings.Contains(b, "//") || strings.Contains(b, "@{") {
		return false
	}
	for _, r := range b {
		if r < 0x20 || r == 0x7f || unicode.IsSpace(r) || strings.ContainsRune(`~^:?*[\`, r) {
			return false
		}
	}
	for part := range strings.SplitSeq(b, "/") {
		if strings.HasPrefix(part, ".") || strings.HasSuffix(part, ".lock") {
			return false
		}
	}
	return true
}

// Validate normalizes in, or refuses it: its slug, a name of 1 to 100 characters (the slug when empty), a
// description of at most 500 and a valid default branch (DefaultBranch when empty).
func Validate(in Input) (Input, error) {
	in.Name, in.Description, in.DefaultBranch = strings.TrimSpace(in.Name), strings.TrimSpace(in.Description), strings.TrimSpace(in.DefaultBranch)
	if in.Name == "" {
		in.Name = in.Slug
	}
	if in.DefaultBranch == "" {
		in.DefaultBranch = DefaultBranch
	}
	var description error
	if len(in.Description) > 500 {
		description = &orgs.ValidationError{Field: "description", Code: "description.invalid", Message: "a description is at most 500 characters"}
	}
	if err := errors.Join(ValidateSlug(in.Slug), orgs.ValidateName(in.Name), description, ValidateBranch(in.DefaultBranch)); err != nil {
		return in, err
	}
	return in, nil
}

// RepositoryPermissions keeps those of permissions that apply at a repository, as a role's do on a repository it is
// granted at: a custom role of the organization may hold organization permissions too.
func RepositoryPermissions(permissions []string) []string {
	var out []string
	for _, p := range permissions {
		if slices.Contains(auth.RepositoryPermissions, p) {
			out = append(out, p)
		}
	}
	return out
}
