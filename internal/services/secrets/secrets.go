// Package secrets keeps the outside credentials an organization's settings hold for its repositories, and a
// repository's for its own, and hands their values to the job runs and agent tasks whose grants allow
// (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/jobs.md, "Secrets"). Each value is sealed
// with the installation's secrets key for its own row, and nothing reads one back but Receive.
package secrets

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

// Secret is a secret as anyone may see it: everything but its value.
type Secret struct {
	ID    uuid.UUID
	OrgID uuid.UUID
	// RepoID is the repository whose settings hold the secret; uuid.Nil for the organization's own.
	RepoID uuid.UUID
	Name   string
	// Version counts the secret's changes: its ETag.
	Version   int64
	CreatedBy uuid.UUID
	CreatedAt time.Time
	UpdatedBy uuid.UUID
	UpdatedAt time.Time
}

// Principal is one of the organization's principals that may receive a secret: a member, an API key, a job or an
// agent.
type Principal struct {
	ID          uuid.UUID
	Kind        auth.Kind
	DisplayName string
}

// Input is what a manager writes: a name, unique where the secret lives, and its value.
type Input struct {
	Name  string
	Value string
}

// The limits of a secret's name and value.
const (
	MaxNameLength  = 100
	MaxValueLength = 64 << 10
)

// AnyVersion replaces or deletes whichever version of a secret is there (If-Match: *).
const AnyVersion int64 = -1

// The permissions this package checks.
const (
	// PermManage adds, replaces and deletes secrets, and lists them and who receives each.
	PermManage = "secrets.manage"
	// PermRead receives a secret's value, in a job run or an agent task.
	PermRead = "secrets.read"
)

// The errors the domain answers; an adapter maps each to its status.
var (
	// ErrNotFound is a secret, a repository or a principal that isn't there, or that the viewer may not see.
	ErrNotFound = errors.New("not found")
	// ErrNameTaken is a name another secret of the same organization or repository has.
	ErrNameTaken = errors.New("another secret here has this name")
	// ErrChanged is a change that names a version of the secret it no longer is.
	ErrChanged = errors.New("the secret changed since it was read")
	// ErrGranted is a principal that already receives the secret through a grant on it.
	ErrGranted = errors.New("the principal already receives this secret")
	// ErrNoSecretsKey is a value to seal or open on an installation that has no secrets key.
	ErrNoSecretsKey = errors.New("the installation has no secrets key")
)

// RefusedError is a secret Receive won't hand over: one the principal holds no secrets.read on, or one that isn't
// there.
type RefusedError struct {
	ID      uuid.UUID
	Missing bool
}

func (e *RefusedError) Error() string {
	if e.Missing {
		return fmt.Sprintf("secret %s isn't there", e.ID)
	}
	return fmt.Sprintf("may not read secret %s", e.ID)
}

// About is what a secret's value is sealed for: its row alone, so a sealed value copied to another row doesn't open.
func About(id uuid.UUID) string {
	return "secrets/" + id.String() + "/value"
}

// Validate checks what a manager writes and returns it normalized, the name trimmed. A replacement may leave the
// value empty to keep the one there.
func Validate(in Input, replacing bool) (Input, error) {
	in.Name = strings.TrimSpace(in.Name)
	switch {
	case in.Name == "" || utf8.RuneCountInString(in.Name) > MaxNameLength || strings.IndexFunc(in.Name, unicode.IsControl) >= 0:
		return in, &orgs.ValidationError{Field: "name", Code: "secret.name_invalid", Message: fmt.Sprintf("a secret's name is 1 to %d characters, none of them control characters", MaxNameLength)}
	case in.Value == "" && !replacing:
		return in, &orgs.ValidationError{Field: "value", Code: "secret.value_invalid", Message: "a secret has a value"}
	case len(in.Value) > MaxValueLength:
		return in, &orgs.ValidationError{Field: "value", Code: "secret.value_invalid", Message: fmt.Sprintf("a secret's value is at most %d bytes", MaxValueLength)}
	}
	return in, nil
}

// Store is what the database keeps for secrets. Each write is one transaction, which acts for actor, so the audit log
// records who changed each row.
type Store interface {
	// List lists the secrets of the organization (repoID uuid.Nil) or of one of its repositories, by name.
	List(ctx context.Context, orgID, repoID uuid.UUID) ([]Secret, error)
	// Get finds a secret, or ErrNotFound.
	Get(ctx context.Context, id uuid.UUID) (Secret, error)
	// Create writes the secret with its sealed value and records the event: ErrNotFound when its repository isn't
	// the organization's, ErrNameTaken when the name is.
	Create(ctx context.Context, actor uuid.UUID, s Secret, sealed []byte) (Secret, error)
	// Replace renames the secret, and replaces its value unless sealed is nil, if it is still at version (any with
	// AnyVersion), and records the event: ErrChanged otherwise, ErrNotFound when it's gone, ErrNameTaken.
	Replace(ctx context.Context, actor uuid.UUID, id uuid.UUID, version int64, name string, sealed []byte) (Secret, error)
	// Delete deletes the secret and the grants on it if it is still at version (any with AnyVersion), and records
	// the event: ErrChanged otherwise, ErrNotFound when it's gone.
	Delete(ctx context.Context, actor uuid.UUID, id uuid.UUID, version int64) error
	// Sealed reads the sealed values of those of ids that are there.
	Sealed(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID][]byte, error)
	// Access lists the principals holding a grant on the secret, by display name.
	Access(ctx context.Context, id uuid.UUID) ([]Principal, error)
	// Jobs lists the jobs of the organization's repository, by name.
	Jobs(ctx context.Context, orgID, repoID uuid.UUID) ([]Principal, error)
	// Principal finds one of the organization's members, API keys, jobs or agents, or ErrNotFound.
	Principal(ctx context.Context, orgID, principalID uuid.UUID) (Principal, error)
	// Grant gives the principal Secret reader on the secret and records it, or ErrGranted when it holds it.
	Grant(ctx context.Context, actor uuid.UUID, s Secret, principalID uuid.UUID) error
	// Revoke takes Secret reader from the principal on the secret and records it, or ErrNotFound when it doesn't
	// hold it.
	Revoke(ctx context.Context, actor uuid.UUID, s Secret, principalID uuid.UUID) error
}
