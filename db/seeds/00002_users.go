package seeds

import (
	"context"
	"database/sql"
	"fmt"

	"cobranch.dev/internal/services/auth"
)

// A demo user: the sign-in page offers each one while seeding is on, and signs in as them with no provider. Their
// organizations and grants come with the organizations seed; until then each is a user with an email.
type demoUser struct {
	id, subject, name, email string
}

// demoUsers are the roles a reviewer tries: an organization's owner, a member, a guest, and someone with no
// organization. Their IDs are fixed, so later seeds refer to them.
var demoUsers = []demoUser{
	{id: "00000000-0000-7000-8000-00000000d001", subject: "owner", name: "Olivia Owner", email: "olivia@cobranch.example"},
	{id: "00000000-0000-7000-8000-00000000d002", subject: "member", name: "Max Member", email: "max@cobranch.example"},
	{id: "00000000-0000-7000-8000-00000000d003", subject: "guest", name: "Gwen Guest", email: "gwen@cobranch.example"},
	{id: "00000000-0000-7000-8000-00000000d004", subject: "newcomer", name: "Nia Newcomer", email: "nia@cobranch.example"},
}

// users seeds the demo users, each with a verified email and an identity of auth.SeedIssuer, which no provider has.
func users(ctx context.Context, tx *sql.Tx) error {
	for _, u := range demoUsers {
		if _, err := tx.ExecContext(ctx, "INSERT INTO principals (id, kind, display_name) VALUES ($1, $2, $3)",
			u.id, auth.KindUser, u.name); err != nil {
			return fmt.Errorf("seeding user %s: %w", u.subject, err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO principal_emails (email, principal_id, verified) VALUES ($1, $2, true)",
			u.email, u.id); err != nil {
			return fmt.Errorf("seeding %s's email: %w", u.subject, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO identities (issuer, subject, principal_id, email, email_verified, name)
			VALUES ($1, $2, $3, $4, true, $5)`, auth.SeedIssuer, u.subject, u.id, u.email, u.name); err != nil {
			return fmt.Errorf("seeding %s's identity: %w", u.subject, err)
		}
	}
	return nil
}
