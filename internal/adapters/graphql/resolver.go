package graphql

import (
	"context"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
)

// Sessions is what the API needs of sign-in's service: the viewer's sessions, and revoking one.
type Sessions interface {
	Sessions(ctx context.Context, userID uuid.UUID) ([]auth.Session, error)
	Revoke(ctx context.Context, userID, id uuid.UUID) error
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
}
