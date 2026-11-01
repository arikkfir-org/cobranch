package graphql

import (
	"context"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
)

// Sessions is what the API needs of sign-in's service: the viewer's sessions, personal tokens and SSH keys, and
// revoking one.
type Sessions interface {
	Sessions(ctx context.Context, userID uuid.UUID) ([]auth.Session, error)
	Revoke(ctx context.Context, userID, id uuid.UUID) error
	CreateToken(ctx context.Context, a auth.Authenticated, in auth.TokenInput) (auth.Credential, string, error)
	Tokens(ctx context.Context, userID uuid.UUID) ([]auth.Credential, error)
	RevokeToken(ctx context.Context, userID, id uuid.UUID) error
	AddSSHKey(ctx context.Context, a auth.Authenticated, in auth.SSHKeyInput) (auth.Credential, error)
	SSHKeys(ctx context.Context, userID uuid.UUID) ([]auth.Credential, error)
	DeleteSSHKey(ctx context.Context, a auth.Authenticated, id uuid.UUID) error
}

// Resolver resolves api/graphql/'s schema. gqlgen generates its methods' stubs into <schema>.resolvers.go and keeps
// their bodies when it regenerates.
type Resolver struct {
	// Version is the version the server was built as.
	Version string
	// Sessions is sign-in's service.
	Sessions Sessions
	// Orgs is the organizations' service.
	Orgs Organizations
	// Repos is the repositories' service.
	Repos Repositories
	// Code is the service of the repositories' code.
	Code Code
}
