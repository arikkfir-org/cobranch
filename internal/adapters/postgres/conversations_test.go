package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/chats"
	"cobranch.dev/internal/services/repos"
)

// conversationFixture is chatFixture with Acme's built-in reviewer agent.
type conversationFixture struct {
	*chatFixture
	agent uuid.UUID
}

func newConversationFixture(t *testing.T) *conversationFixture {
	t.Helper()
	f := &conversationFixture{chatFixture: newChatFixture(t)}
	if err := f.pool.QueryRow(context.Background(), "SELECT principal_id FROM agents WHERE org_id = $1 AND purpose = 'review'", f.acme.ID).Scan(&f.agent); err != nil {
		t.Fatal(err)
	}
	return f
}

// conversation finds owner's conversation about Acme, about website or with the reviewer, by kind.
func (f *conversationFixture) conversation(t *testing.T, owner uuid.UUID, kind chats.Kind) chats.Subject {
	t.Helper()
	repo, agent := uuid.Nil, uuid.Nil
	switch kind {
	case chats.KindRepository:
		repo = f.website.ID
	case chats.KindAgent:
		agent = f.agent
	}
	subj, err := f.s.Conversation(context.Background(), owner, f.acme.ID, repo, agent)
	if err != nil {
		t.Fatalf("Conversation(%s) = %v", kind, err)
	}
	return subj
}

func TestChatStoreConversation(t *testing.T) {
	t.Parallel()
	f := newConversationFixture(t)
	ctx := context.Background()
	var mainHead string
	if err := f.pool.QueryRow(ctx, "SELECT head FROM branches WHERE repo_id = $1 AND name = 'main' AND deleted_at IS NULL", f.website.ID).Scan(&mainHead); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		kind chats.Kind
		want func(s chats.Subject) bool
	}{
		{kind: chats.KindOrganization, want: func(s chats.Subject) bool { return s.RepoID == uuid.Nil && s.AgentID == uuid.Nil && s.Repo == "" }},
		{kind: chats.KindRepository, want: func(s chats.Subject) bool {
			return s.RepoID == f.website.ID && s.Repo == "website" && s.Branch == "main" && s.Head == mainHead && s.AgentID == uuid.Nil
		}},
		{kind: chats.KindAgent, want: func(s chats.Subject) bool { return s.AgentID == f.agent && s.Agent != "" && s.RepoID == uuid.Nil }},
	}
	for _, tt := range tests {
		t.Run(string(tt.kind), func(t *testing.T) {
			s := f.conversation(t, f.max, tt.kind)
			if s.Kind() != tt.kind || s.OrgID != f.acme.ID || s.Org != f.acme.Slug || s.OwnerID != f.max || s.BranchID != uuid.Nil || s.IssueID != uuid.Nil ||
				s.ConversationID != chats.ConversationID(f.max, f.acme.ID, s.RepoID, s.AgentID) || !tt.want(s) {
				t.Errorf("Conversation() = %+v; want Max's %s conversation", s, tt.kind)
			}
		})
	}
	for name, ids := range map[string][2]uuid.UUID{"no repository": {uuid.New(), uuid.Nil}, "no agent": {uuid.Nil, uuid.New()},
		"a person as an agent": {uuid.Nil, f.max}} {
		if _, err := f.s.Conversation(ctx, f.max, f.acme.ID, ids[0], ids[1]); !errors.Is(err, repos.ErrNotFound) {
			t.Errorf("Conversation(%s) = %v, want %v", name, err, repos.ErrNotFound)
		}
	}
	if _, err := f.s.Conversation(ctx, f.max, uuid.New(), uuid.Nil, uuid.Nil); !errors.Is(err, repos.ErrNotFound) {
		t.Errorf("Conversation(no organization) = %v, want %v", err, repos.ErrNotFound)
	}
}

func TestChatStoreConversationThreads(t *testing.T) {
	t.Parallel()
	for _, kind := range []chats.Kind{chats.KindOrganization, chats.KindRepository, chats.KindAgent} {
		t.Run(string(kind), func(t *testing.T) {
			t.Parallel()
			f := newConversationFixture(t)
			maxs, nias := f.conversation(t, f.max, kind), f.conversation(t, f.nia, kind)
			if n := count(t, f.pool, "SELECT count(*) FROM conversations"); n != 0 {
				t.Fatalf("conversations before any thread = %d, want none", n)
			}
			if page, after, err := f.s.Threads(context.Background(), maxs, uuid.Nil, 10); err != nil || len(page) != 0 || after != 0 {
				t.Errorf("Threads() before any thread = %+v, %d, %v", page, after, err)
			}
			thread, reply := f.startConversation(t, maxs)
			f.checkApart(t, maxs, nias, thread, reply)
			f.checkRecords(t, maxs)
		})
	}
}

// startConversation starts two threads in Max's conversation maxs, the first of which makes it, and replies to the
// first; it answers the first thread and the reply.
func (f *conversationFixture) startConversation(t *testing.T, maxs chats.Subject) (uuid.UUID, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	thread, created, err := f.s.StartThread(ctx, chats.Poster{ID: f.max}, maxs, v7(), v7(), nil, "Where does the push worker start?")
	if err != nil || !created || len(thread.Messages) != 1 {
		t.Fatalf("StartThread() = %+v, %v, %v", thread, created, err)
	}
	if _, _, err := f.s.StartThread(ctx, chats.Poster{ID: f.max}, maxs, v7(), v7(), nil, "And another"); err != nil {
		t.Fatal(err)
	}
	if n := count(t, f.pool, "SELECT count(*) FROM conversations WHERE id = $1 AND owner_id = $2 AND org_id = $3", maxs.ConversationID, f.max,
		f.acme.ID); n != 1 {
		t.Errorf("Max's conversation rows = %d, want 1, made by the first thread", n)
	}
	if n := count(t, f.pool, "SELECT count(*) FROM chat_threads WHERE conversation_id = $1 AND repo_id IS NULL AND branch_id IS NULL AND issue_id IS NULL",
		maxs.ConversationID); n != 2 {
		t.Errorf("the threads' rows name the conversation alone: %d rows, want 2", n)
	}
	reply := v7()
	if _, created, err := f.s.Post(ctx, chats.Poster{ID: f.max}, maxs, thread.ID, reply, "In cmd/cobranch"); err != nil || !created {
		t.Fatalf("Post() = %v, %v", created, err)
	}
	if page, after, err := f.s.Threads(ctx, maxs, uuid.Nil, 10); err != nil || len(page) != 2 || after == 0 {
		t.Errorf("Threads(Max's) = %d threads, %d, %v; want 2", len(page), after, err)
	}
	return thread.ID, reply
}

// checkApart checks that Nia's conversation about the same subject, nias, and the branch's chat, are apart from Max's.
func (f *conversationFixture) checkApart(t *testing.T, maxs, nias chats.Subject, thread, reply uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	if page, after, err := f.s.Threads(ctx, nias, uuid.Nil, 10); err != nil || len(page) != 0 || after != 0 {
		t.Errorf("Threads(Nia's) = %+v, %d, %v; want none", page, after, err)
	}
	if _, err := f.s.Message(ctx, nias, reply); !errors.Is(err, repos.ErrNotFound) {
		t.Errorf("Message() through Nia's conversation = %v, want %v", err, repos.ErrNotFound)
	}
	if _, _, err := f.s.Post(ctx, chats.Poster{ID: f.nia}, nias, thread, v7(), "Hi"); !errors.Is(err, repos.ErrNotFound) {
		t.Errorf("Post() to Max's thread through Nia's conversation = %v, want %v", err, repos.ErrNotFound)
	}
	if none, _, err := f.s.Threads(ctx, f.feature, uuid.Nil, 10); err != nil || len(none) != 0 {
		t.Errorf("Threads(feature) = %+v, %v; want none", none, err)
	}
	if _, err := f.s.Message(ctx, maxs, reply); err != nil {
		t.Errorf("Message() through Max's conversation = %v", err)
	}
}

// checkRecords checks the updates, events and audit rows of Max's conversation maxs, and that its threads point at no
// file.
func (f *conversationFixture) checkRecords(t *testing.T, maxs chats.Subject) {
	t.Helper()
	ctx := context.Background()
	updates, err := f.s.Updates(ctx, maxs, 0, 100)
	if err != nil || len(updates) != 3 || updates[0].Kind != chats.ThreadStarted || updates[2].Kind != chats.MessagePosted {
		t.Errorf("Updates() = %+v, %v; want two starts and a post", updates, err)
	}
	if n := count(t, f.pool, "SELECT count(*) FROM events WHERE kind LIKE 'chat.%' AND subject_id = $1 AND details->>'conversation' = $1::text",
		maxs.ConversationID); n != 3 {
		t.Errorf("the conversation's events = %d, want 3, naming it", n)
	}
	if n := count(t, f.pool, "SELECT count(*) FROM audit_log WHERE table_name = 'conversations' AND principal_id = $1", f.max); n != 1 {
		t.Errorf("the audit log's rows of the conversation = %d, want 1, naming Max", n)
	}
	// A conversation's thread points at no file, whatever asks the store.
	anchor := &chats.Anchor{SHA: shaC, Path: "src/main.go", StartLine: 1, EndLine: 1}
	if _, _, err := f.s.StartThread(ctx, chats.Poster{ID: f.max}, maxs, v7(), v7(), anchor, "Here"); err == nil {
		t.Error("StartThread(anchored, in a conversation) started a thread")
	}
}

func TestChatStoreConversationGone(t *testing.T) {
	t.Parallel()
	for _, kind := range []chats.Kind{chats.KindRepository, chats.KindAgent} {
		t.Run(string(kind), func(t *testing.T) {
			t.Parallel()
			f := newConversationFixture(t)
			ctx := context.Background()
			subj := f.conversation(t, f.max, kind)
			thread, _, err := f.s.StartThread(ctx, chats.Poster{ID: f.max}, subj, v7(), v7(), nil, "Before")
			if err != nil {
				t.Fatal(err)
			}
			f.deleteSubject(t, subj)
			if n := count(t, f.pool, "SELECT count(*) FROM chat_threads WHERE conversation_id = $1", subj.ConversationID); n != 0 {
				t.Errorf("the conversation's threads once its subject is gone = %d, want none", n)
			}
			if _, _, err := f.s.Post(ctx, chats.Poster{ID: f.max}, subj, thread.ID, v7(), "After"); !errors.Is(err, repos.ErrNotFound) {
				t.Errorf("Post() once its subject is gone = %v, want %v", err, repos.ErrNotFound)
			}
			if _, _, err := f.s.StartThread(ctx, chats.Poster{ID: f.max}, subj, v7(), v7(), nil, "After"); !errors.Is(err, repos.ErrNotFound) {
				t.Errorf("StartThread() once its subject is gone = %v, want %v", err, repos.ErrNotFound)
			}
		})
	}
}

// deleteSubject deletes the repository or the agent the conversation subj is about, the agent's tasks first.
func (f *conversationFixture) deleteSubject(t *testing.T, subj chats.Subject) {
	t.Helper()
	ctx := context.Background()
	statement, gone := "DELETE FROM repositories WHERE id = $1", subj.RepoID
	if subj.Kind() == chats.KindAgent {
		if _, err := f.pool.Exec(ctx, "DELETE FROM agent_tasks WHERE agent_id = $1", subj.AgentID); err != nil {
			t.Fatal(err)
		}
		statement, gone = "DELETE FROM principals WHERE id = $1", subj.AgentID
	}
	if _, err := f.pool.Exec(ctx, statement, gone); err != nil {
		t.Fatal(err)
	}
}
