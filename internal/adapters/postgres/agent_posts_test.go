package postgres

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"cobranch.dev/db/migrations"
	"cobranch.dev/internal/services/agents"
	"cobranch.dev/internal/services/chats"
)

// emailsOf lists the emails principal holds, verified or not.
func emailsOf(t *testing.T, f *agentFixture, principal uuid.UUID) []string {
	t.Helper()
	rows, err := f.pool.Query(context.Background(), "SELECT email || CASE WHEN verified THEN '' ELSE ' (unverified)' END FROM principal_emails WHERE principal_id = $1", principal)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var e string
		if err := rows.Scan(&e); err != nil {
			t.Fatal(err)
		}
		out = append(out, e)
	}
	return out
}

func TestAgentEmail(t *testing.T) {
	t.Parallel()
	f := newAgentFixture(t)
	ctx := context.Background()
	if got := emailsOf(t, f, f.scout.ID); len(got) != 1 || got[0] != agents.Email(f.scout.ID) {
		t.Errorf("Scout holds %v, want %s, verified", got, agents.Email(f.scout.ID))
	}
	// Down past the migration forgets the agents' emails; up again gives every agent there its email back.
	schema := newSchema(t, f.pool, migrations.FS, migrations.Go...)
	if _, err := schema.provider.DownTo(ctx, 41); err != nil {
		t.Fatal(err)
	}
	if got := emailsOf(t, f, f.scout.ID); len(got) != 0 {
		t.Errorf("Scout holds %v after rolling back, want none", got)
	}
	// A push's login-less user takes the address meanwhile, as a commit naming it makes one; the migration takes it back.
	pusher := loginlessHolding(t, f, agents.Email(f.scout.ID))
	if _, err := schema.provider.Up(ctx); err != nil {
		t.Fatal(err)
	}
	if got := emailsOf(t, f, f.scout.ID); len(got) != 1 || got[0] != agents.Email(f.scout.ID) {
		t.Errorf("Scout holds %v after the migration ran again", got)
	}
	if got := emailsOf(t, f, pusher); len(got) != 0 {
		t.Errorf("the login-less user holds %v, want none", got)
	}
	// So does a new agent whose address a login-less user took before the agent was made.
	id := v7()
	pusher = loginlessHolding(t, f, agents.Email(id))
	if _, err := f.pool.Exec(ctx, "INSERT INTO principals (id, kind, display_name, org_id) VALUES ($1, 'agent', 'Finder', $2)", id, f.acme.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, "INSERT INTO agents (principal_id, org_id, name) VALUES ($1, $2, 'Finder')", id, f.acme.ID); err != nil {
		t.Fatal(err)
	}
	if got := emailsOf(t, f, id); len(got) != 1 || got[0] != agents.Email(id) {
		t.Errorf("a new agent holds %v", got)
	}
	if got := emailsOf(t, f, pusher); len(got) != 0 {
		t.Errorf("the login-less user holds %v once the agent is made, want none", got)
	}
}

// loginlessHolding makes a login-less user holding email unverified, as a push whose commit names it does, and
// answers its ID.
func loginlessHolding(t *testing.T, f *agentFixture, email string) uuid.UUID {
	t.Helper()
	id := v7()
	if _, err := f.pool.Exec(context.Background(), "INSERT INTO principals (id, kind, display_name) VALUES ($1, 'user', 'someone')", id); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(context.Background(), "INSERT INTO principal_emails (email, principal_id, verified) VALUES ($1, $2, false)", email, id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestAgentTaskPosts(t *testing.T) {
	t.Parallel()
	f := newAgentFixture(t)
	ctx := context.Background()
	task := f.assigned(t, "Look around", true, uuid.Nil)
	scout := chats.Poster{ID: f.scout.ID, Task: task.ID}
	want := chats.TaskRef{ID: task.ID, Number: task.Number}
	first, _, err := f.s.StartThread(ctx, scout, f.feature, v7(), v7(), nil, chats.Said{Body: "Starting."})
	if err != nil || first.Messages[0].Task == nil || *first.Messages[0].Task != want || first.Messages[0].Author.ID != f.scout.ID {
		t.Fatalf("StartThread() = %+v, %v", first, err)
	}
	if got, err := f.a.Task(ctx, f.acme.ID, task.Number); err != nil || got.ThreadID != first.ID {
		t.Errorf("the task talks in %v, %v; want the thread it started", got.ThreadID, err)
	}
	second, _, err := f.s.StartThread(ctx, scout, f.feature, v7(), v7(), nil, chats.Said{Body: "And another."})
	if err != nil {
		t.Fatal(err)
	}
	main, err := f.s.Subject(ctx, f.website.ID, "main")
	if err != nil {
		t.Fatal(err)
	}
	other := f.assigned(t, "Look elsewhere", true, uuid.Nil)
	if _, _, err := f.s.StartThread(ctx, chats.Poster{ID: f.scout.ID, Task: other.ID}, main, v7(), v7(), nil, chats.Said{Body: "Over here."}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name string
		task agents.Task
		want uuid.UUID
	}{{"a second thread", task, first.ID}, {"a thread of another branch", other, uuid.Nil}} {
		if got, err := f.a.Task(ctx, f.acme.ID, c.task.Number); err != nil || got.ThreadID != c.want {
			t.Errorf("after %s, the task talks in %v, %v; want %v", c.name, got.ThreadID, err, c.want)
		}
	}
	posted, _, err := f.s.Post(ctx, scout, f.feature, second.ID, v7(), chats.Said{Body: "Done."})
	if err != nil || posted.Task == nil || *posted.Task != want {
		t.Errorf("Post() = %+v, %v", posted, err)
	}
	theirs, _, err := f.s.Post(ctx, chats.Poster{ID: f.max}, f.feature, second.ID, v7(), chats.Said{Body: "Thanks."})
	if err != nil || theirs.Task != nil {
		t.Errorf("a person's Post() = %+v, %v", theirs, err)
	}
	if _, err := f.pool.Exec(ctx, "DELETE FROM agent_tasks WHERE id = $1", task.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := f.s.Thread(ctx, f.feature, second.ID); err != nil || len(got.Messages) != 3 || got.Messages[1].Task != nil || got.Messages[1].Author == nil {
		t.Errorf("once the task is deleted, Thread() = %+v, %v", got, err)
	}
}
