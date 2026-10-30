package seeds

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"

	"cobranch.dev/db/seeds/queries"
	"cobranch.dev/internal/services/auth"
)

// A demo user: the sign-in page offers each one while seeding is on, and signs in as them with no provider. Their
// organizations and grants come with the organizations seed; until then each is a user with an email.
type demoUser struct {
	id                   uuid.UUID
	subject, name, email string
}

// demoUsers are the roles a reviewer tries: an organization's owner, a member, a guest, and someone with no
// organization. Their IDs are fixed, so later seeds refer to them.
var demoUsers = []demoUser{
	{id: uuid.MustParse("00000000-0000-7000-8000-00000000d001"), subject: "owner", name: "Olivia Owner", email: "olivia@cobranch.example"},
	{id: uuid.MustParse("00000000-0000-7000-8000-00000000d002"), subject: "member", name: "Max Member", email: "max@cobranch.example"},
	{id: uuid.MustParse("00000000-0000-7000-8000-00000000d003"), subject: "guest", name: "Gwen Guest", email: "gwen@cobranch.example"},
	{id: uuid.MustParse("00000000-0000-7000-8000-00000000d004"), subject: "newcomer", name: "Nia Newcomer", email: "nia@cobranch.example"},
}

// users seeds the demo users, each with a verified email and an identity of auth.SeedIssuer, which no provider has.
func users(ctx context.Context, tx *sql.Tx) error {
	q := queries.New(tx)
	for _, u := range demoUsers {
		if err := q.InsertUser(ctx, u.id, u.name); err != nil {
			return fmt.Errorf("seeding user %s: %w", u.subject, err)
		}
		if err := q.InsertVerifiedEmail(ctx, u.email, u.id); err != nil {
			return fmt.Errorf("seeding %s's email: %w", u.subject, err)
		}
		if err := q.InsertSeedIdentity(ctx, queries.InsertSeedIdentityParams{
			Issuer: auth.SeedIssuer, Subject: u.subject, PrincipalID: u.id, Email: u.email, Name: u.name,
		}); err != nil {
			return fmt.Errorf("seeding %s's identity: %w", u.subject, err)
		}
	}
	return nil
}
