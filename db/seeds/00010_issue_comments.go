package seeds

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"

	"cobranch.dev/db/seeds/queries"
)

// demoComment is a thread of a demo issue's comments: who started it, its messages in the order they were posted, and
// who resolved it, if anyone.
type demoComment struct {
	id         uuid.UUID
	issue      demoIssue
	messages   []demoMessage
	resolvedBy uuid.UUID
}

// demoMessage is a message of a demo comment's thread, and who posted it.
type demoMessage struct {
	id     uuid.UUID
	author uuid.UUID
	body   string
}

// demoComments are comments on two of Acme's issues: a question that Max answered on the one in progress, and a
// report that Olivia resolved on the footer's bug, so the issue page shows its comments, replies and a resolved thread
// at once.
var demoComments = []demoComment{
	{id: uuid.MustParse("00000000-0000-7000-8000-00000000d501"), issue: demoIssues[1], messages: []demoMessage{
		{id: uuid.MustParse("00000000-0000-7000-8000-00000000d511"), author: demoUsers[0].id, body: "Should it show pushes to the other branches too?"},
		{id: uuid.MustParse("00000000-0000-7000-8000-00000000d512"), author: demoUsers[1].id,
			body: "Only `main`'s: the others are work in progress, and their own pages show them."},
	}},
	{id: uuid.MustParse("00000000-0000-7000-8000-00000000d502"), issue: demoIssues[2], resolvedBy: demoUsers[0].id, messages: []demoMessage{
		{id: uuid.MustParse("00000000-0000-7000-8000-00000000d521"), author: demoUsers[1].id, body: "Seen on a 375-pixel phone too: the last two links overlap."},
		{id: uuid.MustParse("00000000-0000-7000-8000-00000000d522"), author: demoUsers[0].id, body: "Thanks, that's the case to fix first."},
	}},
}

// issueComments seeds the demo comments, each thread with its messages and the events its chat's updates count.
func issueComments(ctx context.Context, tx *sql.Tx) error {
	q := queries.New(tx)
	for _, c := range demoComments {
		org := c.issue.org.id
		if err := q.InsertSeedChatThread(ctx, queries.InsertSeedChatThreadParams{ID: c.id, OrgID: org, IssueID: uuid.NullUUID{UUID: c.issue.id, Valid: true},
			CreatedBy: uuid.NullUUID{UUID: c.messages[0].author, Valid: true}, ResolvedBy: nullable(c.resolvedBy)}); err != nil {
			return fmt.Errorf("seeding a comment on issue %q: %w", c.issue.title, err)
		}
		for i, m := range c.messages {
			if err := q.InsertSeedChatMessage(ctx, queries.InsertSeedChatMessageParams{ID: m.id, ThreadID: c.id, OrgID: org,
				AuthorID: uuid.NullUUID{UUID: m.author, Valid: true}, Body: m.body}); err != nil {
				return fmt.Errorf("seeding a message on issue %q: %w", c.issue.title, err)
			}
			kind := "chat.message.posted"
			if i == 0 {
				kind = "chat.thread.started"
			}
			if err := event(ctx, q, c.issue, m.author, kind, map[string]any{"issue": c.issue.id, "thread": c.id, "message": m.id}); err != nil {
				return err
			}
		}
		if c.resolvedBy != uuid.Nil {
			if err := event(ctx, q, c.issue, c.resolvedBy, "chat.thread.resolved", map[string]any{"issue": c.issue.id, "thread": c.id}); err != nil {
				return err
			}
		}
	}
	return nil
}
