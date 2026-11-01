// Package code is the domain of a repository's code as people browse it: its branches, a branch's history and its
// commits, with the principals behind them and the pushes that brought them, all as pushes recorded them. It knows
// nothing of HTTP, Git's protocol or PostgreSQL: adapters bring those through its port, Store, and every permission
// check goes through sign-in's authorizer
// (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/git-hosting.md, "The Console").
package code

import (
	"regexp"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/git"
	"cobranch.dev/internal/services/orgs"
)

// Principal is who is behind a commit's author or committer, or a push: a user, perhaps one with no login whom a
// push made for an unknown email.
type Principal struct {
	ID          uuid.UUID
	DisplayName string
}

// Person is a commit's author or committer as the commit writes them, with the zone their time was written in, such
// as +0200, and the principal behind them once the push worker has mapped their email; nil until then.
type Person struct {
	Name      string
	Email     string
	At        time.Time
	Zone      string
	Principal *Principal
}

// Commit is a commit a push brought, as the push recorded it: its tree, its parents (the first of which its branch's
// history follows), its message and signature as written, its author and committer, and the push that first brought
// it.
type Commit struct {
	SHA       string
	Tree      string
	Parents   []string
	Message   string
	Author    Person
	Committer Person
	Signature string
	FirstPush uuid.UUID
}

// Push is a push that brought commits: who pushed, over which transport, and when Cobranch recorded it.
type Push struct {
	ID uuid.UUID
	// Pusher is who pushed; nil once they are gone.
	Pusher    *Principal
	Transport git.Transport
	At        time.Time
}

// Branch is one of a repository's live branches and the commit it is at: Commit is nil while its head names an
// object no push recorded as a commit.
type Branch struct {
	Name      string
	Head      string
	CreatedAt time.Time
	UpdatedAt time.Time
	Commit    *Commit
}

// Page is a page of a branch's first-parent history, nearest first; Next is where the next page starts, empty after
// the last.
type Page struct {
	Commits []Commit
	Next    string
}

// The sizes of a history's pages.
const (
	// DefaultPageSize is a page's size when the caller asks for none.
	DefaultPageSize = 30
	// MaxPageSize is the largest page anyone gets.
	MaxPageSize = 100
)

// shaPattern is an object name: 40 hexadecimal digits for SHA-1, or 64 for SHA-256 once repositories take it.
var shaPattern = regexp.MustCompile(`^([0-9a-f]{40}|[0-9a-f]{64})$`)

// ValidateSHA reports whether sha names an object as Git writes it in full, in lowercase hexadecimal; field names
// what the caller gave it in.
func ValidateSHA(field, sha string) error {
	if !shaPattern.MatchString(sha) {
		return &orgs.ValidationError{Field: field, Code: field + ".invalid", Message: "an object's full name: 40 or 64 lowercase hexadecimal digits"}
	}
	return nil
}

// pageSize is the size of the page a caller asked for: DefaultPageSize for 0, at most MaxPageSize.
func pageSize(asked int) (int, error) {
	switch {
	case asked == 0:
		return DefaultPageSize, nil
	case asked < 0 || asked > MaxPageSize:
		return 0, &orgs.ValidationError{Field: "limit", Code: "limit.invalid", Message: "a page holds 1 to 100 commits"}
	}
	return asked, nil
}
