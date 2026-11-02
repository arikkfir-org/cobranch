package seeds

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"

	"cobranch.dev/db/seeds/queries"
)

// demoLabel is a label a demo organization starts with, and the demo issues that carry it, by ID.
type demoLabel struct {
	id                uuid.UUID
	org               demoOrganization
	name, color, what string
	issues            []uuid.UUID
}

// demoLabels are Acme's labels in every colour, on most of its issues, some carrying two, and one of Globex's, so the
// Issues page's chips and label filter show at once, and the Labels page what each is for.
var demoLabels = []demoLabel{
	{id: uuid.MustParse("00000000-0000-7000-8000-00000000d401"), org: demoOrganizations[0], name: "Bug", color: "red", what: "Something works wrongly",
		issues: []uuid.UUID{demoIssues[2].id}},
	{id: uuid.MustParse("00000000-0000-7000-8000-00000000d402"), org: demoOrganizations[0], name: "Feature", color: "blue", what: "Something new for visitors",
		issues: []uuid.UUID{demoIssues[0].id, demoIssues[1].id}},
	{id: uuid.MustParse("00000000-0000-7000-8000-00000000d403"), org: demoOrganizations[0], name: "Docs", color: "green", what: "Guides and policies",
		issues: []uuid.UUID{demoIssues[3].id, demoIssues[4].id}},
	{id: uuid.MustParse("00000000-0000-7000-8000-00000000d404"), org: demoOrganizations[0], name: "Design", color: "yellow", what: "How it looks",
		issues: []uuid.UUID{demoIssues[0].id, demoIssues[2].id}},
	{id: uuid.MustParse("00000000-0000-7000-8000-00000000d405"), org: demoOrganizations[0], name: "Chore", color: "gray", what: "Upkeep nobody sees",
		issues: []uuid.UUID{demoIssues[6].id}},
	{id: uuid.MustParse("00000000-0000-7000-8000-00000000d406"), org: demoOrganizations[1], name: "Finance", color: "green", what: "The books",
		issues: []uuid.UUID{demoIssues[7].id}},
}

// labels seeds the demo labels and puts them on their issues.
func labels(ctx context.Context, tx *sql.Tx) error {
	q := queries.New(tx)
	for _, l := range demoLabels {
		if err := q.InsertSeedLabel(ctx, queries.InsertSeedLabelParams{ID: l.id, OrgID: l.org.id, Name: l.name, Color: l.color, Description: l.what}); err != nil {
			return fmt.Errorf("seeding label %q of %s: %w", l.name, l.org.slug, err)
		}
		for _, issue := range l.issues {
			if err := q.InsertSeedIssueLabel(ctx, issue, l.id); err != nil {
				return fmt.Errorf("putting label %q on issue %s: %w", l.name, issue, err)
			}
		}
	}
	return nil
}
