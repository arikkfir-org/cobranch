package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/chats"
	"cobranch.dev/internal/services/issues"
	"cobranch.dev/internal/services/repos"
)

// issueChatFixture is chatFixture with one of Acme's issues, and its chat.
type issueChatFixture struct {
	*chatFixture
	is    *IssueStore
	issue issues.Issue
	chat  chats.Subject
}

func newIssueChatFixture(t *testing.T) *issueChatFixture {
	t.Helper()
	f := &issueChatFixture{chatFixture: newChatFixture(t)}
	f.is = NewIssueStore(f.pool)
	ctx := context.Background()
	var err error
	f.issue, err = f.is.Create(ctx, f.olivia, issues.Issue{ID: v7(), OrgID: f.acme.ID, Title: "Safari", State: issues.StateTodo,
		CreatedBy: &issues.Principal{ID: f.olivia}, CreatedAt: now, UpdatedAt: now, Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	if f.chat, err = f.s.Issue(ctx, f.acme.ID, f.issue.Number); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestChatStoreIssue(t *testing.T) {
	t.Parallel()
	f := newIssueChatFixture(t)
	ctx := context.Background()
	if s := f.chat; s.OrgID != f.acme.ID || s.IssueID != f.issue.ID || s.Number != f.issue.Number || s.Issue != f.issue.Identifier() ||
		s.Org != f.acme.Slug || s.RepoID != uuid.Nil || s.BranchID != uuid.Nil {
		t.Errorf("Issue() = %+v; want issue %s's chat", s, f.issue.Identifier())
	}
	for _, number := range []int{0, f.issue.Number + 1, 1 << 40} {
		if _, err := f.s.Issue(ctx, f.acme.ID, number); !errors.Is(err, repos.ErrNotFound) {
			t.Errorf("Issue(%d) = %v, want %v", number, err, repos.ErrNotFound)
		}
	}
}

func TestChatStoreIssueThreads(t *testing.T) {
	t.Parallel()
	f := newIssueChatFixture(t)
	ctx := context.Background()
	thread, created, err := f.s.StartThread(ctx, chats.Poster{ID: f.max}, f.chat, v7(), v7(), nil, chats.Said{Body: "Seen in Safari 18 too"})
	if err != nil || !created || thread.Anchor != nil || len(thread.Messages) != 1 {
		t.Fatalf("StartThread() = %+v, %v, %v", thread, created, err)
	}
	reply := v7()
	if _, created, err := f.s.Post(ctx, chats.Poster{ID: f.nia}, f.chat, thread.ID, reply, chats.Said{Body: "Same here"}); err != nil || !created {
		t.Fatalf("Post() = %v, %v", created, err)
	}
	if n := count(t, f.pool, "SELECT count(*) FROM chat_threads WHERE id = $1 AND issue_id = $2 AND repo_id IS NULL AND branch_id IS NULL", thread.ID,
		f.issue.ID); n != 1 {
		t.Errorf("the thread's row names its issue alone: %d rows", n)
	}
	page, after, err := f.s.Threads(ctx, f.chat, uuid.Nil, 10)
	if err != nil || len(page) != 1 || page[0].ID != thread.ID || len(page[0].Messages) != 2 || after == 0 {
		t.Errorf("Threads(the issue) = %+v, %d, %v; want its thread and reply", page, after, err)
	}
	// The issue's chat and the branch's are apart.
	if none, _, err := f.s.Threads(ctx, f.feature, uuid.Nil, 10); err != nil || len(none) != 0 {
		t.Errorf("Threads(feature) = %+v, %v; want none", none, err)
	}
	if _, err := f.s.Message(ctx, f.feature, reply); !errors.Is(err, repos.ErrNotFound) {
		t.Errorf("Message() through the branch = %v, want %v", err, repos.ErrNotFound)
	}
	if _, _, err := f.s.StartThread(ctx, chats.Poster{ID: f.max}, f.feature, thread.ID, v7(), nil, chats.Said{Body: "Elsewhere"}); !errors.Is(err, chats.ErrIDTaken) {
		t.Errorf("StartThread(the issue's thread's ID on a branch) = %v, want %v", err, chats.ErrIDTaken)
	}
	// An issue's thread points at no file, whatever asks the store.
	anchor := &chats.Anchor{SHA: shaC, Path: "src/main.go", StartLine: 1, EndLine: 1}
	if _, _, err := f.s.StartThread(ctx, chats.Poster{ID: f.max}, f.chat, v7(), v7(), anchor, chats.Said{Body: "Here"}); err == nil {
		t.Error("StartThread(anchored, on the issue) started a thread")
	}
}

func TestChatStoreIssueUpdates(t *testing.T) {
	t.Parallel()
	f := newIssueChatFixture(t)
	ctx := context.Background()
	thread, _, err := f.s.StartThread(ctx, chats.Poster{ID: f.max}, f.chat, v7(), v7(), nil, chats.Said{Body: "Hello"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Resolve(ctx, f.max, f.chat, thread.ID, 1, true); err != nil {
		t.Fatal(err)
	}
	updates, err := f.s.Updates(ctx, f.chat, 0, 100)
	if err != nil || len(updates) != 2 || updates[0].Kind != chats.ThreadStarted || updates[1].Kind != chats.ThreadResolved ||
		updates[1].Thread.ResolvedAt == nil {
		t.Fatalf("Updates() = %+v, %v; want the start and the resolution", updates, err)
	}
	if n := count(t, f.pool, "SELECT count(*) FROM events WHERE kind LIKE 'chat.%' AND subject_id = $1 AND details->>'issue' = $1::text", f.issue.ID); n != 2 {
		t.Errorf("the issue's chat events = %d, want 2, naming the issue", n)
	}
	// Its activity holds what happened to the issue alone.
	events, err := f.is.Activity(ctx, f.issue.ID)
	if err != nil || len(events) != 1 || events[0].Kind != "issue.created" {
		t.Errorf("Activity() = %+v, %v; want issue.created alone", events, err)
	}
}

func TestChatStoreDeletedIssue(t *testing.T) {
	t.Parallel()
	f := newIssueChatFixture(t)
	ctx := context.Background()
	thread, _, err := f.s.StartThread(ctx, chats.Poster{ID: f.max}, f.chat, v7(), v7(), nil, chats.Said{Body: "Hello"})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.is.Delete(ctx, f.olivia, f.issue, f.issue.Version); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.s.Post(ctx, chats.Poster{ID: f.max}, f.chat, thread.ID, v7(), chats.Said{Body: "Late"}); !errors.Is(err, repos.ErrNotFound) {
		t.Errorf("Post() to a deleted issue's chat = %v, want %v", err, repos.ErrNotFound)
	}
	for what, query := range map[string]string{
		"threads": "SELECT count(*) FROM chat_threads WHERE issue_id = $1",
		"events":  "SELECT count(*) FROM events WHERE subject_id = $1",
	} {
		if n := count(t, f.pool, query, f.issue.ID); n != 0 {
			t.Errorf("a deleted issue's chat %s = %d, want none", what, n)
		}
	}
}

func TestAgentStoreThreadBranchOfAnIssue(t *testing.T) {
	t.Parallel()
	f := newIssueChatFixture(t)
	ctx := context.Background()
	thread, _, err := f.s.StartThread(ctx, chats.Poster{ID: f.max}, f.chat, v7(), v7(), nil, chats.Said{Body: "Seen in Safari 18 too"})
	if err != nil {
		t.Fatal(err)
	}
	// A task's thread is one of its branch's chat: an issue's belongs to no branch.
	if id, err := NewAgentStore(f.pool).ThreadBranch(ctx, thread.ID); err != nil || id != uuid.Nil {
		t.Errorf("ThreadBranch(an issue's thread) = %s, %v; want none", id, err)
	}
}
