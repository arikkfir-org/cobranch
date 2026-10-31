package seeds

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"

	"cobranch.dev/db/seeds/queries"
	"cobranch.dev/internal/services/auth"
)

// demoRepository is a repository a demo organization starts with: who created it, and so holds Maintainer there,
// and the repository roles others hold on it.
type demoRepository struct {
	id                      uuid.UUID
	org                     demoOrganization
	slug, name, description string
	creator                 uuid.UUID
	grants                  map[uuid.UUID]uuid.UUID // principal → repository role
}

// demoRepositories are empty repositories a reviewer pushes to, and whose access they change. At Acme, Max is a
// Developer of website and Gwen, a Guest, its Reader; Release manager, which Max and the Deploy script key hold
// there, reads every repository of Acme, so both show as Internal. Globex's ledger is Max's alone, and Private.
var demoRepositories = []demoRepository{
	{id: uuid.MustParse("00000000-0000-7000-8000-00000000d201"), org: demoOrganizations[0], slug: "website", name: "Website",
		description: "Acme's public site.", creator: demoUsers[0].id,
		grants: map[uuid.UUID]uuid.UUID{demoUsers[1].id: auth.RoleDeveloper, demoUsers[2].id: auth.RoleReader}},
	{id: uuid.MustParse("00000000-0000-7000-8000-00000000d202"), org: demoOrganizations[0], slug: "handbook", name: "Handbook",
		description: "How Acme works.", creator: demoUsers[0].id},
	{id: uuid.MustParse("00000000-0000-7000-8000-00000000d203"), org: demoOrganizations[1], slug: "ledger", name: "Ledger",
		description: "Globex's books.", creator: demoUsers[1].id},
}

// repositories seeds the demo repositories, their slugs, their creators' Maintainer and the other grants on them.
func repositories(ctx context.Context, tx *sql.Tx) error {
	q := queries.New(tx)
	for _, r := range demoRepositories {
		if err := q.InsertSeedRepository(ctx, queries.InsertSeedRepositoryParams{
			ID: r.id, OrgID: r.org.id, Slug: r.slug, Name: r.name, Description: r.description, CreatedBy: uuid.NullUUID{UUID: r.creator, Valid: true},
		}); err != nil {
			return fmt.Errorf("seeding repository %s/%s: %w", r.org.slug, r.slug, err)
		}
		if err := q.InsertSeedRepositorySlug(ctx, r.org.id, r.slug, r.id); err != nil {
			return fmt.Errorf("seeding %s/%s's slug: %w", r.org.slug, r.slug, err)
		}
		grants := map[uuid.UUID]uuid.UUID{r.creator: auth.RoleMaintainer}
		for principal, role := range r.grants {
			grants[principal] = role
		}
		for principal, role := range grants {
			at := uuid.NullUUID{UUID: r.id, Valid: true}
			if err := q.InsertSeedGrant(ctx, queries.InsertSeedGrantParams{PrincipalID: principal, RoleID: role, Scope: "repository", ScopeID: at}); err != nil {
				return fmt.Errorf("granting %s a role on %s/%s: %w", principal, r.org.slug, r.slug, err)
			}
		}
	}
	return nil
}
