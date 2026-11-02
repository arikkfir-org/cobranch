package seeds

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"cobranch.dev/db/seeds/queries"
)

// demoIssue is an issue a demo organization starts with, numbered in the order demoIssues lists its organization's:
// who created it, and the changes someone made to it since, which its activity shows.
type demoIssue struct {
	id                 uuid.UUID
	org                demoOrganization
	title, description string
	state              string
	priority           int16 // 0 for none, then urgent (1) to low (4)
	assignee, repo     uuid.UUID
	creator            uuid.UUID
	changes            []demoChange
}

// demoChange is a change to a demo issue, as its activity shows it: who made it, and the fields it changed.
type demoChange struct {
	by     uuid.UUID
	fields []map[string]string
}

// demoIssues are Acme's issues in every state, priority and repository, assigned to Olivia, to Max and to nobody,
// some of them changed since, and one of Globex's, so the Issues page shows its groups, filters and activity at once.
// Gwen, a Guest, reads none of them.
var demoIssues = []demoIssue{
	{id: uuid.MustParse("00000000-0000-7000-8000-00000000d301"), org: demoOrganizations[0], title: "Publish the pricing page", state: "done", priority: 3,
		assignee: demoUsers[1].id, repo: demoRepositories[0].id, creator: demoUsers[0].id,
		changes: []demoChange{{by: demoUsers[1].id, fields: []map[string]string{{"field": "state", "from": "in_progress", "to": "done"}}}}},
	{id: uuid.MustParse("00000000-0000-7000-8000-00000000d302"), org: demoOrganizations[0], title: "Show the latest pushes on the home page",
		description: "Visitors should see what changed lately:\n\n- the five latest pushes to `main`\n- each with its author and time\n\n" +
			"It follows [the pricing page](ACME-1).",
		state: "in_progress", priority: 2, assignee: demoUsers[1].id, repo: demoRepositories[0].id, creator: demoUsers[0].id,
		changes: []demoChange{{by: demoUsers[0].id, fields: []map[string]string{{"field": "assignee", "to": demoUsers[1].name, "toId": demoUsers[1].id.String()}}},
			{by: demoUsers[1].id, fields: []map[string]string{{"field": "state", "from": "todo", "to": "in_progress"}}}}},
	{id: uuid.MustParse("00000000-0000-7000-8000-00000000d303"), org: demoOrganizations[0], title: "Fix the footer's links on phones",
		description: "The footer's links overlap below 400 pixels.", state: "todo", priority: 1, repo: demoRepositories[0].id, creator: demoUsers[1].id},
	{id: uuid.MustParse("00000000-0000-7000-8000-00000000d304"), org: demoOrganizations[0], title: "Write the onboarding guide",
		description: "A newcomer's first week, step by step.", state: "todo", priority: 3, assignee: demoUsers[0].id, repo: demoRepositories[1].id, creator: demoUsers[0].id},
	{id: uuid.MustParse("00000000-0000-7000-8000-00000000d305"), org: demoOrganizations[0], title: "Review the security policy", state: "in_review", priority: 2,
		assignee: demoUsers[0].id, repo: demoRepositories[1].id, creator: demoUsers[0].id},
	{id: uuid.MustParse("00000000-0000-7000-8000-00000000d306"), org: demoOrganizations[0], title: "Plan next quarter's launches", state: "backlog", creator: demoUsers[1].id},
	{id: uuid.MustParse("00000000-0000-7000-8000-00000000d307"), org: demoOrganizations[0], title: "Move the blog to its own host", state: "canceled", priority: 4,
		repo: demoRepositories[0].id, creator: demoUsers[0].id,
		changes: []demoChange{{by: demoUsers[0].id, fields: []map[string]string{{"field": "state", "from": "backlog", "to": "canceled"}}}}},
	{id: uuid.MustParse("00000000-0000-7000-8000-00000000d308"), org: demoOrganizations[1], title: "Close October's books", state: "todo", priority: 2,
		assignee: demoUsers[1].id, repo: demoRepositories[2].id, creator: demoUsers[1].id},
}

// issues seeds the demo issues, numbered in their organizations from 1, with their organizations' counters and the
// events their activity shows.
func issues(ctx context.Context, tx *sql.Tx) error {
	q := queries.New(tx)
	numbers := map[uuid.UUID]int32{}
	for _, i := range demoIssues {
		numbers[i.org.id]++
		if err := q.InsertSeedIssue(ctx, queries.InsertSeedIssueParams{ID: i.id, OrgID: i.org.id, Number: numbers[i.org.id], Title: i.title,
			Description: i.description, State: i.state, Priority: i.priority, AssigneeID: nullable(i.assignee), RepoID: nullable(i.repo),
			CreatedBy: uuid.NullUUID{UUID: i.creator, Valid: true}}); err != nil {
			return fmt.Errorf("seeding issue %q of %s: %w", i.title, i.org.slug, err)
		}
		if err := event(ctx, q, i, i.creator, "issue.created", map[string]any{"number": numbers[i.org.id], "title": i.title}); err != nil {
			return err
		}
		for _, c := range i.changes {
			if err := event(ctx, q, i, c.by, "issue.changed", map[string]any{"changes": c.fields}); err != nil {
				return err
			}
		}
	}
	for org, last := range numbers {
		if err := q.InsertSeedIssueNumber(ctx, org, last); err != nil {
			return fmt.Errorf("seeding %s's issue counter: %w", org, err)
		}
	}
	return nil
}

// event records one of i's events, by whom, with its details.
func event(ctx context.Context, q *queries.Queries, i demoIssue, by uuid.UUID, kind string, details map[string]any) error {
	data, err := json.Marshal(details)
	if err != nil {
		return fmt.Errorf("encoding %s of issue %q: %w", kind, i.title, err)
	}
	if err := q.InsertSeedEvent(ctx, queries.InsertSeedEventParams{ActorID: by, Kind: kind, SubjectID: uuid.NullUUID{UUID: i.id, Valid: true},
		Details: data, OrgID: uuid.NullUUID{UUID: i.org.id, Valid: true}}); err != nil {
		return fmt.Errorf("seeding %s of issue %q: %w", kind, i.title, err)
	}
	return nil
}

// nullable is id, or null for uuid.Nil.
func nullable(id uuid.UUID) uuid.NullUUID {
	return uuid.NullUUID{UUID: id, Valid: id != uuid.Nil}
}
