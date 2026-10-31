package seeds

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/db/seeds/queries"
	"cobranch.dev/internal/services/auth"
)

// deployKey is Acme's API key, holding Release manager, so a reviewer sees a key with its roles and a secret on the
// keys page. Its one secret is revoked, so the key holds no credential anyone knows: a reviewer rotates it from the
// keys page and gets a secret of their own, shown once, to call the APIs with.
var deployKey = struct {
	id, secretID uuid.UUID
	name         string
}{
	id: uuid.MustParse("00000000-0000-7000-8000-00000000d1e1"), secretID: uuid.MustParse("00000000-0000-7000-8000-00000000d1e2"),
	name: "Deploy script",
}

// apiKeys seeds Acme's API key with its grant and a revoked secret, whose value nobody keeps.
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
	gone := auth.NewSecret(auth.PrefixAPIKey) // forgotten once hashed: the row shows a revoked secret, not a way in
	if err := q.InsertSeedCredential(ctx, queries.InsertSeedCredentialParams{
		ID: deployKey.secretID, TokenHash: auth.Hash(gone), PrincipalID: deployKey.id, Hint: auth.Hint(gone),
		CreatedBy: uuid.NullUUID{UUID: olivia, Valid: true}, RevokedAt: sql.NullTime{Time: time.Now(), Valid: true},
	}); err != nil {
		return fmt.Errorf("seeding the API key's secret: %w", err)
	}
	return nil
}
