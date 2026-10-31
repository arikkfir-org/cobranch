// Package admin is the domain of an installation's administration, which the Admin API serves to the Back-office:
// listing the installation's organizations and users, creating an organization for a user, and entering one. It
// knows nothing of HTTP or PostgreSQL: adapters bring those through its port, Store, and every permission check goes
// through sign-in's authorizer, at the installation
// (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/back-office.md).
package admin

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/orgs"
)

// The permissions this slice checks, all at the installation.
const (
	// PermManage runs the installation: its organizations, entering them, and its settings.
	PermManage = "installation.manage"
	// PermUsersManage sees and manages every user.
	PermUsersManage = "installation.users.manage"
)

// Organization is an organization as an administrator sees it: never its content.
type Organization struct {
	ID        uuid.UUID
	Slug      string
	Name      string
	CreatedAt time.Time
	// Members counts its members.
	Members int
}

// User is a user as an administrator sees them.
type User struct {
	ID          uuid.UUID
	DisplayName string
	Emails      []auth.Email
	// Issuers are the identity providers' issuers the user signs in with.
	Issuers []string
	// Roles names the roles the user holds at the installation.
	Roles []string
	// Organizations counts the organizations the user belongs to.
	Organizations int
	Suspended     bool
	CreatedAt     time.Time
	// LastSignInAt is when the user last signed in at a provider; zero when never.
	LastSignInAt time.Time
}

// Page asks for one page of a list, newest first: those matching Query, after the item whose ID is After (uuid.Nil
// for the first page), at most Limit.
type Page struct {
	Query string
	After uuid.UUID
	Limit int
}

// The limits of a page.
const (
	DefaultLimit = 50
	MaxLimit     = 100
)

// normalize trims the query and bounds the limit.
func (p Page) normalize() Page {
	p.Query = strings.TrimSpace(p.Query)
	if p.Limit <= 0 {
		p.Limit = DefaultLimit
	}
	p.Limit = min(p.Limit, MaxLimit)
	return p
}

// List is one page of a list: Items, and Next, the cursor of the page after it, or uuid.Nil on the last page.
type List[T any] struct {
	Items []T
	Next  uuid.UUID
}

// page cuts items, of which the store returned up to one more than limit, to limit, and names the next page's cursor
// when there was one more.
func page[T any](items []T, limit int, id func(T) uuid.UUID) List[T] {
	if len(items) <= limit {
		return List[T]{Items: items}
	}
	items = items[:limit]
	return List[T]{Items: items, Next: id(items[limit-1])}
}

// MaxReason is the most characters an entry's reason holds.
const MaxReason = 500

// validateReason refuses an entry with no reason, or one longer than MaxReason.
func validateReason(reason string) error {
	if reason == "" || utf8.RuneCountInString(reason) > MaxReason {
		return &orgs.ValidationError{Field: "reason", Code: "reason.invalid", Message: "say why you enter, in 1 to 500 characters"}
	}
	return nil
}
