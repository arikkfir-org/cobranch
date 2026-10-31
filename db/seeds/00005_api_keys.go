package seeds

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"

	"cobranch.dev/db/seeds/queries"
	"cobranch.dev/internal/services/auth"
)

// deployKey is Acme's API key, holding Release manager, so a reviewer sees a key with its roles and a secret on the
// keys page and calls the APIs with it. Its secret is fixed, so a reviewer can use it: a demo installation alone
// seeds, where every seeded credential is public by design.
var deployKey = struct {
	id, secretID uuid.UUID
	name, secret string
}{
	id: uuid.MustParse("00000000-0000-7000-8000-00000000d1e1"), secretID: uuid.MustParse("00000000-0000-7000-8000-00000000d1e2"),
	name: "Deploy script", secret: "cbk_rmj3kyPYS3Stleno6ieY6J5uyEuDgkBJPj4w0C7inos2PndWL",
}

// apiKeys seeds Acme's API key with its grant and its secret, which never expires.
func apiKeys(ctx context.Context, tx *sql.Tx) error {
	q := queries.New(tx)
	acme, olivia := demoOrganizations[0], demoUsers[0].id
	if err := q.InsertSeedAPIKey(ctx, queries.InsertSeedAPIKeyParams{
		ID: deployKey.id, Name: deployKey.name, OrgID: uuid.NullUUID{UUID: acme.id, Valid: true}, CreatedBy: uuid.NullUUID{UUID: olivia, Valid: true},
	}); err != nil {
		return fmt.Errorf("seeding %s's API key: %w", acme.slug, err)
	}
	if err := q.InsertSeedGrant(ctx, queries.InsertSeedGrantParams{
		PrincipalID: deployKey.id, RoleID: releaseManager.id, Scope: "organization", ScopeID: uuid.NullUUID{UUID: acme.id, Valid: true},
	}); err != nil {
		return fmt.Errorf("granting the API key %s at %s: %w", releaseManager.name, acme.slug, err)
	}
	if err := q.InsertSeedCredential(ctx, queries.InsertSeedCredentialParams{
		ID: deployKey.secretID, TokenHash: auth.Hash(deployKey.secret), PrincipalID: deployKey.id, Hint: auth.Hint(deployKey.secret),
		CreatedBy: uuid.NullUUID{UUID: olivia, Valid: true},
	}); err != nil {
		return fmt.Errorf("seeding the API key's secret: %w", err)
	}
	return nil
}
