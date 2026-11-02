package seeds

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"

	"cobranch.dev/db/seeds/queries"
	"cobranch.dev/internal/services/agents"
	"cobranch.dev/internal/services/auth"
)

// demoReviewers are the demo organizations' AI reviewers, by organization. The schema's migration 40 makes one for
// every organization already there, and runs before the seeds, so a fresh demo installation gets its own here; one
// upgraded in place got them from the migration, and keeps those.
var demoReviewers = map[uuid.UUID]uuid.UUID{
	demoOrganizations[0].id: uuid.MustParse("00000000-0000-7000-8000-00000000d701"),
	demoOrganizations[1].id: uuid.MustParse("00000000-0000-7000-8000-00000000d702"),
}

// reviewers seeds each demo organization's AI reviewer that it lacks, holding Automation there, with no model yet.
func reviewers(ctx context.Context, tx *sql.Tx) error {
	q := queries.New(tx)
	for _, o := range demoOrganizations {
		has, err := q.HasReviewer(ctx, o.id)
		if err != nil {
			return fmt.Errorf("finding %s's AI reviewer: %w", o.slug, err)
		}
		if has {
			continue
		}
		r := agents.Reviewer(demoReviewers[o.id], o.id)
		if err := q.InsertSeedAgentPrincipal(ctx, r.ID, r.Name, uuid.NullUUID{UUID: o.id, Valid: true}); err != nil {
			return fmt.Errorf("seeding %s's AI reviewer: %w", o.slug, err)
		}
		if err := q.InsertSeedReviewer(ctx, queries.InsertSeedReviewerParams{ID: r.ID, OrgID: o.id, Name: r.Name, Description: r.Description}); err != nil {
			return fmt.Errorf("seeding %s's AI reviewer: %w", o.slug, err)
		}
		at := uuid.NullUUID{UUID: o.id, Valid: true}
		if err := q.InsertSeedGrant(ctx, queries.InsertSeedGrantParams{PrincipalID: r.ID, RoleID: auth.RoleOrgAutomation, Scope: "organization", ScopeID: at}); err != nil {
			return fmt.Errorf("granting %s's AI reviewer Automation: %w", o.slug, err)
		}
	}
	return nil
}
