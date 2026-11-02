package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/chats"
	"cobranch.dev/internal/services/repos"
)

// chatFixture is codeFixture with the chats' store, and the chat of website's feature branch.
type chatFixture struct {
	*codeFixture
	s       *ChatStore
	feature chats.Subject
}

func newChatFixture(t *testing.T) *chatFixture {
	t.Helper()
	f := &chatFixture{codeFixture: newCodeFixture(t)}
	f.s = NewChatStore(f.pool)
	var err error
	if f.feature, err = f.s.Subject(context.Background(), f.website.ID, "feature"); err != nil {
		t.Fatal(err)
	}
	return f
}

func v7() uuid.UUID { return uuid.Must(uuid.NewV7()) }

func TestChatStoreSubject(t *testing.T) {
	f := newChatFixture(t)
	ctx := context.Background()
	if s := f.feature; s.RepoID != f.website.ID || s.OrgID != f.acme.ID || s.Branch != "feature" || s.Head != shaC || s.Org != f.acme.Slug ||
		s.Repo != "website" || s.BranchID == uuid.Nil {
		t.Errorf("Subject(feature) = %+v", s)
	}
	for _, name := range []string{"old", "nothing"} {
		if _, err := f.s.Subject(ctx, f.website.ID, name); !errors.Is(err, repos.ErrNotFound) {
			t.Errorf("Subject(%s) = %v, want %v", name, err, repos.ErrNotFound)
		}
	}
	if head, err := f.s.Head(ctx, f.feature.BranchID); err != nil || head != shaC {
		t.Errorf("Head() = %q, %v; want %s", head, err, shaC)
	}
	if held, err := f.s.HasCommit(ctx, f.website.ID, shaB); err != nil || !held {
		t.Errorf("HasCommit(b) = %v, %v", held, err)
	}
	if held, err := f.s.HasCommit(ctx, f.secret.ID, shaB); err != nil || held {
		t.Errorf("HasCommit(another repository's) = %v, %v", held, err)
	}
}

// started starts a thread of by's on feature, and returns it.
func (f *chatFixture) started(t *testing.T, by uuid.UUID, body string) chats.Thread {
	t.Helper()
	thread, _, err := f.s.StartThread(context.Background(), by, f.feature, v7(), v7(), nil, body)
	if err != nil {
		t.Fatal(err)
	}
	return thread
}

// changed starts a thread of Max's on feature, then edits its message, resolves the thread, opens it again and deletes
// the message, and returns the thread and the message's ID.
func (f *chatFixture) changed(t *testing.T) (chats.Thread, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	thread := f.started(t, f.max, "Hello")
	id := thread.Messages[0].ID
	if _, err := f.s.Edit(ctx, f.max, f.feature, id, 1, "Hello, world"); err != nil {
		t.Fatal(err)
	}
	for i, resolved := range []bool{true, false} {
		if _, err := f.s.Resolve(ctx, f.nia, f.feature, thread.ID, int64(i+1), resolved); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.s.Delete(ctx, f.nia, f.feature, id, 2); err != nil {
		t.Fatal(err)
	}
	return thread, id
}

func TestChatStoreStartThread(t *testing.T) {
	f := newChatFixture(t)
	ctx := context.Background()
	anchor := &chats.Anchor{SHA: shaC, Path: "src/main.go", StartLine: 3, EndLine: 5}
	first, created, err := f.s.StartThread(ctx, f.max, f.feature, v7(), v7(), anchor, "Why *this*?")
	if err != nil || !created {
		t.Fatalf("StartThread() = %v, %v", created, err)
	}
	if first.StartedBy == nil || first.StartedBy.ID != f.max || first.Anchor == nil || *first.Anchor != *anchor || first.Version != 1 ||
		len(first.Messages) != 1 || first.Messages[0].Body != "Why *this*?" || first.Messages[0].Author.ID != f.max {
		t.Fatalf("StartThread() = %+v", first)
	}
	again, created, err := f.s.StartThread(ctx, f.nia, f.feature, first.ID, v7(), nil, "Again")
	if err != nil || created || again.ID != first.ID || len(again.Messages) != 1 {
		t.Errorf("StartThread(its ID again) = %+v, %v, %v; want the thread, not started again", again, created, err)
	}
	main, err := f.s.Subject(ctx, f.website.ID, "main")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.s.StartThread(ctx, f.max, main, first.ID, v7(), nil, "Elsewhere"); !errors.Is(err, chats.ErrIDTaken) {
		t.Errorf("StartThread(another chat's ID) = %v, want %v", err, chats.ErrIDTaken)
	}
}

func TestChatStorePost(t *testing.T) {
	f := newChatFixture(t)
	ctx := context.Background()
	first, second, reply := f.started(t, f.max, "Why?"), f.started(t, f.nia, "Ship it"), v7()
	if m, created, err := f.s.Post(ctx, f.nia, f.feature, first.ID, reply, "Because"); err != nil || !created || m.ThreadID != first.ID ||
		m.Author.ID != f.nia {
		t.Fatalf("Post() = %+v, %v, %v", m, created, err)
	}
	if _, created, err := f.s.Post(ctx, f.nia, f.feature, first.ID, reply, "Because"); err != nil || created {
		t.Errorf("Post(its ID again) = %v, %v; want it found", created, err)
	}
	if _, _, err := f.s.Post(ctx, f.nia, f.feature, second.ID, reply, "Because"); !errors.Is(err, chats.ErrIDTaken) {
		t.Errorf("Post(another thread's message's ID) = %v, want %v", err, chats.ErrIDTaken)
	}
	main, err := f.s.Subject(ctx, f.website.ID, "main")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.s.Post(ctx, f.nia, main, first.ID, v7(), "Lost"); !errors.Is(err, repos.ErrNotFound) {
		t.Errorf("Post() to another chat's thread = %v, want %v", err, repos.ErrNotFound)
	}
	if n := count(t, f.pool, "SELECT count(*) FROM audit_log WHERE table_name IN ('chat_threads', 'chat_messages') AND principal_id = $1", f.nia); n != 3 {
		t.Errorf("Nia's audit rows = %d, want 3: a thread and two messages", n)
	}
}

func TestChatStoreThreads(t *testing.T) {
	f := newChatFixture(t)
	ctx := context.Background()
	first, second, reply := f.started(t, f.max, "Why?"), f.started(t, f.nia, "Ship it"), v7()
	if _, _, err := f.s.Post(ctx, f.nia, f.feature, first.ID, reply, "Because"); err != nil {
		t.Fatal(err)
	}
	page, after, err := f.s.Threads(ctx, f.feature, uuid.Nil, 10)
	if err != nil || len(page) != 2 || page[0].ID != second.ID || page[1].ID != first.ID || len(page[1].Messages) != 2 ||
		page[1].Messages[1].ID != reply || after == 0 {
		t.Fatalf("Threads() = %+v, %d, %v; want second, then first with its reply", page, after, err)
	}
	older, _, err := f.s.Threads(ctx, f.feature, second.ID, 10)
	if err != nil || len(older) != 1 || older[0].ID != first.ID {
		t.Errorf("Threads(before second) = %+v, %v; want first", older, err)
	}
	main, err := f.s.Subject(ctx, f.website.ID, "main")
	if err != nil {
		t.Fatal(err)
	}
	if none, _, err := f.s.Threads(ctx, main, uuid.Nil, 10); err != nil || len(none) != 0 {
		t.Errorf("Threads(main) = %+v, %v; want none", none, err)
	}
	if _, err := f.s.Thread(ctx, main, first.ID); !errors.Is(err, repos.ErrNotFound) {
		t.Errorf("Thread(another chat's) = %v, want %v", err, repos.ErrNotFound)
	}
	if _, err := f.s.Message(ctx, main, reply); !errors.Is(err, repos.ErrNotFound) {
		t.Errorf("Message(another chat's) = %v, want %v", err, repos.ErrNotFound)
	}
}

func TestChatStoreEditResolve(t *testing.T) {
	f := newChatFixture(t)
	ctx := context.Background()
	thread := f.started(t, f.max, "Hello")
	id := thread.Messages[0].ID
	edited, err := f.s.Edit(ctx, f.max, f.feature, id, 1, "Hello, world")
	if err != nil || edited.Body != "Hello, world" || edited.EditedAt == nil || edited.Version != 2 {
		t.Fatalf("Edit() = %+v, %v", edited, err)
	}
	if _, err := f.s.Edit(ctx, f.max, f.feature, id, 1, "Stale"); !errors.Is(err, chats.ErrChanged) {
		t.Errorf("Edit(from version 1) = %v, want %v", err, chats.ErrChanged)
	}
	resolved, err := f.s.Resolve(ctx, f.nia, f.feature, thread.ID, chats.AnyVersion, true)
	if err != nil || resolved.ResolvedAt == nil || resolved.ResolvedBy == nil || resolved.ResolvedBy.ID != f.nia || resolved.Version != 2 ||
		len(resolved.Messages) != 1 {
		t.Fatalf("Resolve() = %+v, %v", resolved, err)
	}
	if _, err := f.s.Resolve(ctx, f.max, f.feature, thread.ID, 1, false); !errors.Is(err, chats.ErrChanged) {
		t.Errorf("Resolve(from version 1) = %v, want %v", err, chats.ErrChanged)
	}
	reopened, err := f.s.Resolve(ctx, f.max, f.feature, thread.ID, 2, false)
	if err != nil || reopened.ResolvedAt != nil || reopened.ResolvedBy != nil || reopened.Version != 3 {
		t.Errorf("Resolve(open) = %+v, %v", reopened, err)
	}
}

func TestChatStoreDelete(t *testing.T) {
	f := newChatFixture(t)
	ctx := context.Background()
	id := f.started(t, f.max, "Hello").Messages[0].ID
	if err := f.s.Delete(ctx, f.nia, f.feature, id, 1); err != nil {
		t.Fatalf("Delete() = %v", err)
	}
	gone, err := f.s.Message(ctx, f.feature, id)
	if err != nil || gone.Body != "" || gone.DeletedAt == nil || gone.DeletedBy == nil || gone.DeletedBy.ID != f.nia {
		t.Errorf("Message() after Delete() = %+v, %v", gone, err)
	}
	if err := f.s.Delete(ctx, f.nia, f.feature, id, chats.AnyVersion); !errors.Is(err, chats.ErrChanged) {
		t.Errorf("Delete() again = %v, want %v", err, chats.ErrChanged)
	}
	if _, err := f.s.Edit(ctx, f.max, f.feature, id, chats.AnyVersion, "Back"); !errors.Is(err, chats.ErrChanged) {
		t.Errorf("Edit() of a deleted message = %v, want %v", err, chats.ErrChanged)
	}
}

func TestChatStoreUpdates(t *testing.T) {
	f := newChatFixture(t)
	ctx := context.Background()
	thread, id := f.changed(t)
	updates, err := f.s.Updates(ctx, f.feature, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	want := []chats.UpdateKind{chats.ThreadStarted, chats.MessageEdited, chats.ThreadResolved, chats.ThreadReopened, chats.MessageDeleted}
	if len(updates) != len(want) {
		t.Fatalf("Updates() = %+v, want %v", updates, want)
	}
	for i, u := range updates {
		if u.Kind != want[i] || u.Thread.ID != thread.ID || (u.Message == nil) != (u.Kind == chats.ThreadResolved || u.Kind == chats.ThreadReopened) ||
			i > 0 && u.Cursor <= updates[i-1].Cursor {
			t.Errorf("update %d = %+v, want %s on %s in order", i, u, want[i], thread.ID)
		}
	}
	if u := updates[0]; u.Message == nil || u.Message.ID != id || u.Message.DeletedAt == nil || u.Thread.Version != 3 {
		t.Errorf("first update = %+v; want the message and thread as they are now", u)
	}
	if later, err := f.s.Updates(ctx, f.feature, updates[2].Cursor, 100); err != nil || len(later) != 2 || later[0].Kind != chats.ThreadReopened {
		t.Errorf("Updates(after the third) = %+v, %v; want the last two", later, err)
	}
	if _, after, err := f.s.Threads(ctx, f.feature, uuid.Nil, 10); err != nil || after != updates[len(updates)-1].Cursor {
		t.Errorf("Threads()' cursor = %d, %v; want %d", after, err, updates[len(updates)-1].Cursor)
	}
	if n := count(t, f.pool, "SELECT count(*) FROM events WHERE kind LIKE 'chat.%' AND subject_id = $1 AND org_id = $2", f.feature.BranchID, f.acme.ID); n != 5 {
		t.Errorf("chat events = %d, want 5", n)
	}
}

func TestChatStoreDeletedBranch(t *testing.T) {
	f := newChatFixture(t)
	ctx := context.Background()
	thread := f.started(t, f.max, "Hello")
	if _, err := f.pool.Exec(ctx, "UPDATE branches SET deleted_at = now() WHERE id = $1", f.feature.BranchID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.s.Post(ctx, f.max, f.feature, thread.ID, v7(), "Late"); !errors.Is(err, chats.ErrBranchDeleted) {
		t.Errorf("Post() to a deleted branch's chat = %v, want %v", err, chats.ErrBranchDeleted)
	}
	if kept, err := f.s.Thread(ctx, f.feature, thread.ID); err != nil || len(kept.Messages) != 1 {
		t.Errorf("Thread() of a deleted branch's chat = %+v, %v; want it as it was", kept, err)
	}
}

func TestChatHub(t *testing.T) {
	f := newChatFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hub := ListenChats(ctx, f.pool)
	wake, stop := hub.Watch(f.feature.BranchID)
	defer stop()
	other, stopOther := hub.Watch(uuid.New())
	defer stopOther()
	// The first wake comes when the hub starts to listen; the next with the change.
	for _, what := range []string{"listening", "a change"} {
		if what == "a change" {
			drain(other)
			if _, _, err := f.s.StartThread(ctx, f.max, f.feature, v7(), v7(), nil, "Hi"); err != nil {
				t.Fatal(err)
			}
		}
		select {
		case <-wake:
		case <-time.After(10 * time.Second):
			t.Fatalf("no wake on %s", what)
		}
	}
	select {
	case <-other:
		t.Error("a change to one chat woke another's watcher")
	case <-time.After(200 * time.Millisecond):
	}
}

// drain takes a wake from ch, if one waits.
func drain(ch <-chan struct{}) {
	select {
	case <-ch:
	case <-time.After(time.Second):
	}
}
