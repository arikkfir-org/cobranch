package chats_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/chats"
	"cobranch.dev/internal/services/chats/chatstest"
	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/orgs"
	"cobranch.dev/internal/services/orgs/orgstest"
	"cobranch.dev/internal/services/repos"
)

var now = time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)

func sha(n int) string { return strings.Repeat(string(rune('0'+n)), 40) }

// grants is the organizations' fake behind a lock, so a test revokes a grant while a follower reads.
type grants struct {
	mu sync.Mutex
	o  *orgstest.Store
}

func (g *grants) Permissions(ctx context.Context, principal uuid.UUID, scope auth.Scope, scopeID uuid.UUID) ([]string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.o.Permissions(ctx, principal, scope, scopeID) //nolint:wrapcheck // the fake's own error, as the authorizer reads it
}

func (g *grants) Repositories(ctx context.Context, principal, orgID uuid.UUID, permission string) ([]uuid.UUID, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.o.Repositories(ctx, principal, orgID, permission) //nolint:wrapcheck // the fake's own error, as the authorizer reads it
}

// revoke takes every grant of principal's away.
func (g *grants) revoke(principal uuid.UUID) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.o.Grants = slices.DeleteFunc(g.o.Grants, func(x orgstest.Grant) bool { return x.Principal == principal })
}

// renderer renders Markdown as the body inside <p>, naming the branch and commit it rendered at.
type renderer struct{}

func (renderer) Render(_ context.Context, src []byte, doc code.Document) (string, error) {
	return "<p>" + string(src) + "</p><!-- " + doc.Org + "/" + doc.Repo + "@" + doc.Ref + ":" + doc.Commit + " -->", nil
}

// fixture is a repository whose feature branch is at commit 2, and a viewer per built-in repository role: Rae reads it,
// Dev develops, Mia maintains it, and the CI's API key automates; Nia holds nothing there.
type fixture struct {
	store                  *chatstest.Store
	notifier               *chatstest.Notifier
	grants                 *grants
	service                *chats.Service
	repo                   uuid.UUID
	feature                chats.Subject
	rae, dev, mia, ci, nia chats.Viewer
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	o := orgstest.NewStore()
	viewer := func(name string) chats.Viewer {
		return chats.Viewer{Principal: auth.Principal{ID: uuid.New(), Kind: auth.KindUser, DisplayName: name}}
	}
	f := &fixture{store: chatstest.NewStore(), notifier: &chatstest.Notifier{}, grants: &grants{o: o}, repo: uuid.New(),
		rae: viewer("Rae"), dev: viewer("Dev"), mia: viewer("Mia"), ci: viewer("CI"), nia: viewer("Nia")}
	f.ci.Kind = auth.KindAPIKey
	o.Repos[f.repo] = uuid.New()
	o.Grant(f.rae.ID, auth.RoleReader, auth.ScopeRepository, f.repo)
	o.Grant(f.dev.ID, auth.RoleDeveloper, auth.ScopeRepository, f.repo)
	o.Grant(f.mia.ID, auth.RoleMaintainer, auth.ScopeRepository, f.repo)
	o.Grant(f.ci.ID, auth.RoleAutomation, auth.ScopeRepository, f.repo)
	for _, v := range []chats.Viewer{f.rae, f.dev, f.mia, f.ci, f.nia} {
		f.store.Names[v.ID] = v.DisplayName
	}
	f.store.Now = now
	f.store.AddCommit(f.repo, sha(1))
	f.feature = f.store.SetBranch(chats.Subject{OrgID: o.Repos[f.repo], RepoID: f.repo, Branch: "feature", Head: sha(2), Org: "acme", Repo: "website"})
	f.service = chats.NewService(f.store, f.notifier, renderer{}, auth.NewAuthorizer(f.grants))
	return f
}

// start has v start a thread on feature saying body.
func (f *fixture) start(t *testing.T, v chats.Viewer, body string) chats.Thread {
	t.Helper()
	th, err := f.service.StartThread(context.Background(), v, f.feature, chats.ThreadInput{Body: body})
	if err != nil {
		t.Fatal(err)
	}
	return th
}

func invalid(code string) error { return &orgs.ValidationError{Code: code} }

// is reports whether err is want: a validation error of want's code, or matches it.
func is(err, want error) bool {
	var verr, wantV *orgs.ValidationError
	if errors.As(want, &wantV) {
		return errors.As(err, &verr) && verr.Code == wantV.Code
	}
	return errors.Is(err, want)
}

func TestBranch(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if subj, err := f.service.Branch(ctx, f.rae, f.repo, "feature"); err != nil || subj != f.feature {
		t.Errorf("Branch(feature) = %+v, %v; want %+v", subj, err, f.feature)
	}
	for name, v := range map[string]chats.Viewer{"no branch": f.rae, "no access": f.nia} {
		branch := "feature"
		if name == "no branch" {
			branch = "nothing"
		}
		if _, err := f.service.Branch(ctx, v, f.repo, branch); !errors.Is(err, repos.ErrNotFound) {
			t.Errorf("%s: Branch() = %v, want %v", name, err, repos.ErrNotFound)
		}
	}
}

func TestStartThread(t *testing.T) {
	anchor := &chats.Anchor{SHA: sha(1), Path: "src/main.go", StartLine: 3, EndLine: 5}
	mine := uuid.Must(uuid.NewV7())
	tests := []struct {
		name    string
		viewer  func(*fixture) chats.Viewer
		in      chats.ThreadInput
		wantErr error
	}{
		{name: "a developer's", in: chats.ThreadInput{Body: "Why?"}},
		{name: "an API key's, anchored", viewer: func(f *fixture) chats.Viewer { return f.ci }, in: chats.ThreadInput{Anchor: anchor, Body: "Why?"}},
		{name: "a reader's", viewer: func(f *fixture) chats.Viewer { return f.rae }, in: chats.ThreadInput{Body: "Why?"}, wantErr: auth.ErrForbidden},
		{name: "an outsider's", viewer: func(f *fixture) chats.Viewer { return f.nia }, in: chats.ThreadInput{Body: "Why?"}, wantErr: repos.ErrNotFound},
		{name: "blank", in: chats.ThreadInput{Body: " \n"}, wantErr: invalid("body.empty")},
		{name: "too long", in: chats.ThreadInput{Body: strings.Repeat("é", chats.MaxBody+1)}, wantErr: invalid("body.too_long")},
		{name: "the longest", in: chats.ThreadInput{Body: strings.Repeat("é", chats.MaxBody)}},
		{name: "a UUIDv4", in: chats.ThreadInput{ID: uuid.New(), Body: "Why?"}, wantErr: invalid("id.invalid")},
		{name: "its own ID", in: chats.ThreadInput{ID: mine, Body: "Why?"}},
		{name: "a short commit", in: chats.ThreadInput{Anchor: &chats.Anchor{SHA: "1111111", Path: "a", StartLine: 1, EndLine: 1}, Body: "Why?"},
			wantErr: invalid("anchor.invalid")},
		{name: "a commit the repository lacks", in: chats.ThreadInput{Anchor: &chats.Anchor{SHA: sha(9), Path: "a", StartLine: 1, EndLine: 1}, Body: "Why?"},
			wantErr: invalid("anchor.commit_not_found")},
		{name: "a path out of the root", in: chats.ThreadInput{Anchor: &chats.Anchor{SHA: sha(1), Path: "../a", StartLine: 1, EndLine: 1}, Body: "Why?"},
			wantErr: invalid("anchor.invalid")},
		{name: "a path from /", in: chats.ThreadInput{Anchor: &chats.Anchor{SHA: sha(1), Path: "/a", StartLine: 1, EndLine: 1}, Body: "Why?"},
			wantErr: invalid("anchor.invalid")},
		{name: "line 0", in: chats.ThreadInput{Anchor: &chats.Anchor{SHA: sha(1), Path: "a", StartLine: 0, EndLine: 1}, Body: "Why?"},
			wantErr: invalid("anchor.invalid")},
		{name: "lines backwards", in: chats.ThreadInput{Anchor: &chats.Anchor{SHA: sha(1), Path: "a", StartLine: 5, EndLine: 4}, Body: "Why?"},
			wantErr: invalid("anchor.invalid")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			v := f.dev
			if tt.viewer != nil {
				v = tt.viewer(f)
			}
			th, err := f.service.StartThread(context.Background(), v, f.feature, tt.in)
			if !is(err, tt.wantErr) {
				t.Fatalf("StartThread() = %v, want %v", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			if th.StartedBy == nil || th.StartedBy.ID != v.ID || len(th.Messages) != 1 || th.Messages[0].Body != tt.in.Body ||
				th.Messages[0].HTML != "<p>"+tt.in.Body+"</p><!-- acme/website@feature:"+sha(2)+" -->" || th.ID.Version() != 7 ||
				tt.in.ID != uuid.Nil && th.ID != tt.in.ID {
				t.Errorf("StartThread() = %+v", th)
			}
			if th.Outdated != (tt.in.Anchor != nil) {
				t.Errorf("Outdated = %v; an anchor at commit 1 on a branch at 2 is outdated", th.Outdated)
			}
		})
	}
}

func TestStartThreadAgain(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	id := uuid.Must(uuid.NewV7())
	first, err := f.service.StartThread(ctx, f.dev, f.feature, chats.ThreadInput{ID: id, Body: "Once"})
	if err != nil {
		t.Fatal(err)
	}
	again, err := f.service.StartThread(ctx, f.dev, f.feature, chats.ThreadInput{ID: id, Body: "Once"})
	if err != nil || again.ID != first.ID || len(again.Messages) != 1 {
		t.Errorf("StartThread() again = %+v, %v; want the same thread, once", again, err)
	}
	if _, err := f.service.StartThread(ctx, f.mia, f.feature, chats.ThreadInput{ID: id, Body: "Mine"}); !errors.Is(err, chats.ErrIDTaken) {
		t.Errorf("another's StartThread() of its ID = %v, want %v", err, chats.ErrIDTaken)
	}
}

func TestPost(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	th := f.start(t, f.dev, "Why?")
	id := uuid.Must(uuid.NewV7())
	m, err := f.service.Post(ctx, f.ci, f.feature, th.ID, chats.MessageInput{ID: id, Body: "Because"})
	if err != nil || m.ID != id || m.Author.ID != f.ci.ID || m.HTML == "" {
		t.Fatalf("Post() = %+v, %v", m, err)
	}
	if again, err := f.service.Post(ctx, f.ci, f.feature, th.ID, chats.MessageInput{ID: id, Body: "Because"}); err != nil || again.ID != id {
		t.Errorf("Post() again = %+v, %v; want the message", again, err)
	}
	other := f.start(t, f.dev, "Else")
	tests := []struct {
		name    string
		viewer  chats.Viewer
		thread  uuid.UUID
		in      chats.MessageInput
		wantErr error
	}{
		{name: "another's ID", viewer: f.dev, thread: th.ID, in: chats.MessageInput{ID: id, Body: "Mine"}, wantErr: chats.ErrIDTaken},
		{name: "its ID in another thread", viewer: f.ci, thread: other.ID, in: chats.MessageInput{ID: id, Body: "Because"}, wantErr: chats.ErrIDTaken},
		{name: "no thread", viewer: f.dev, thread: uuid.New(), in: chats.MessageInput{Body: "Hi"}, wantErr: repos.ErrNotFound},
		{name: "a reader's", viewer: f.rae, thread: th.ID, in: chats.MessageInput{Body: "Hi"}, wantErr: auth.ErrForbidden},
		{name: "an outsider's", viewer: f.nia, thread: th.ID, in: chats.MessageInput{Body: "Hi"}, wantErr: repos.ErrNotFound},
		{name: "blank", viewer: f.dev, thread: th.ID, in: chats.MessageInput{Body: ""}, wantErr: invalid("body.empty")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := f.service.Post(ctx, tt.viewer, f.feature, tt.thread, tt.in); !is(err, tt.wantErr) {
				t.Errorf("Post() = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

// op is an edit, or else a deletion, of a message.
type op struct {
	edit    bool
	viewer  func(*fixture) chats.Viewer
	version int64
	wantErr error
}

// run edits or deletes the message id of thread as o says, and returns the error it met.
func (o op) run(t *testing.T, f *fixture, thread, id uuid.UUID) error {
	t.Helper()
	ctx := context.Background()
	if !o.edit {
		return f.service.Delete(ctx, o.viewer(f), f.feature, thread, id, o.version) //nolint:wrapcheck // the error under test
	}
	m, err := f.service.Edit(ctx, o.viewer(f), f.feature, thread, id, "Fixed", o.version)
	if err == nil && (m.Body != "Fixed" || m.EditedAt == nil) {
		t.Errorf("Edit() = %+v", m)
	}
	return err //nolint:wrapcheck // the error under test
}

func TestEditDelete(t *testing.T) {
	dev := func(f *fixture) chats.Viewer { return f.dev }
	tests := []struct {
		name string
		ops  []op
	}{
		{name: "its author edits", ops: []op{{edit: true, viewer: dev, version: 1}}},
		{name: "its author edits any version", ops: []op{{edit: true, viewer: dev, version: chats.AnyVersion}}},
		{name: "a stale edit", ops: []op{{edit: true, viewer: dev, version: 1}, {edit: true, viewer: dev, version: 1, wantErr: chats.ErrChanged}}},
		{name: "a moderator edits", ops: []op{{edit: true, viewer: func(f *fixture) chats.Viewer { return f.mia }, version: 1, wantErr: auth.ErrForbidden}}},
		{name: "its author deletes", ops: []op{{viewer: dev, version: 1}, {viewer: dev, version: 2}}},
		{name: "a moderator deletes", ops: []op{{viewer: func(f *fixture) chats.Viewer { return f.mia }, version: 1}}},
		{name: "another developer deletes", ops: []op{{viewer: func(f *fixture) chats.Viewer { return f.ci }, version: 1, wantErr: auth.ErrForbidden}}},
		{name: "a stale deletion", ops: []op{{edit: true, viewer: dev, version: 1}, {viewer: dev, version: 1, wantErr: chats.ErrChanged}}},
		{name: "an edit of a deleted one", ops: []op{{viewer: dev, version: 1}, {edit: true, viewer: dev, version: 2, wantErr: chats.ErrDeleted}}},
		{name: "an outsider deletes", ops: []op{{viewer: func(f *fixture) chats.Viewer { return f.nia }, version: 1, wantErr: repos.ErrNotFound}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			th := f.start(t, f.dev, "Typo")
			for i, o := range tt.ops {
				if err := o.run(t, f, th.ID, th.Messages[0].ID); !is(err, o.wantErr) {
					t.Fatalf("op %d = %v, want %v", i, err, o.wantErr)
				}
			}
		})
	}
	t.Run("in another thread", func(t *testing.T) {
		f := newFixture(t)
		th, other := f.start(t, f.dev, "One"), f.start(t, f.dev, "Two")
		if _, err := f.service.Edit(context.Background(), f.dev, f.feature, other.ID, th.Messages[0].ID, "X", 1); !errors.Is(err, repos.ErrNotFound) {
			t.Errorf("Edit() through another thread = %v, want %v", err, repos.ErrNotFound)
		}
	})
}

func TestResolve(t *testing.T) {
	tests := []struct {
		name     string
		viewer   func(*fixture) chats.Viewer
		resolved bool
		version  int64
		wantErr  error
		want     int64
	}{
		{name: "its starter resolves", viewer: func(f *fixture) chats.Viewer { return f.dev }, resolved: true, version: 1, want: 2},
		{name: "a moderator resolves", viewer: func(f *fixture) chats.Viewer { return f.mia }, resolved: true, version: chats.AnyVersion, want: 2},
		{name: "another developer resolves", viewer: func(f *fixture) chats.Viewer { return f.ci }, resolved: true, version: 1, wantErr: auth.ErrForbidden},
		{name: "a stale resolution", viewer: func(f *fixture) chats.Viewer { return f.dev }, resolved: true, version: 7, wantErr: chats.ErrChanged},
		{name: "opening an open one", viewer: func(f *fixture) chats.Viewer { return f.dev }, resolved: false, version: 7, want: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			th := f.start(t, f.dev, "Done?")
			got, err := f.service.Resolve(context.Background(), tt.viewer(f), f.feature, th.ID, tt.resolved, tt.version)
			if !is(err, tt.wantErr) {
				t.Fatalf("Resolve() = %v, want %v", err, tt.wantErr)
			}
			if err == nil && (got.Version != tt.want || (got.ResolvedAt != nil) != tt.resolved || len(got.Messages) != 1) {
				t.Errorf("Resolve() = %+v, want version %d", got, tt.want)
			}
		})
	}
}

func TestThreads(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	var started []uuid.UUID
	for _, body := range []string{"One", "Two", "Three"} {
		started = append(started, f.start(t, f.dev, body).ID)
	}
	page, err := f.service.Threads(ctx, f.rae, f.feature, uuid.Nil, 2)
	if err != nil || len(page.Threads) != 2 || page.Threads[0].ID != started[2] || page.Next != started[1] || page.After != 3 ||
		page.Threads[0].Messages[0].HTML == "" {
		t.Fatalf("Threads(2) = %+v, %v; want Three and Two, then more, after update 3", page, err)
	}
	rest, err := f.service.Threads(ctx, f.rae, f.feature, page.Next, 2)
	if err != nil || len(rest.Threads) != 1 || rest.Threads[0].ID != started[0] || rest.Next != uuid.Nil {
		t.Errorf("Threads(after) = %+v, %v; want One alone", rest, err)
	}
	for _, limit := range []int{0, chats.MaxPage + 1} {
		if _, err := f.service.Threads(ctx, f.rae, f.feature, uuid.Nil, limit); !is(err, invalid("limit.invalid")) {
			t.Errorf("Threads(%d) = %v, want limit.invalid", limit, err)
		}
	}
	if _, err := f.service.Threads(ctx, f.nia, f.feature, uuid.Nil, 10); !errors.Is(err, repos.ErrNotFound) {
		t.Errorf("an outsider's Threads() = %v, want %v", err, repos.ErrNotFound)
	}
	if th, err := f.service.Thread(ctx, f.rae, f.feature, started[0]); err != nil || th.Messages[0].Body != "One" {
		t.Errorf("Thread() = %+v, %v", th, err)
	}
}

func TestUpdates(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	th := f.start(t, f.dev, "Hi")
	list, err := f.service.Updates(ctx, f.rae, f.feature, 0, 0)
	if err != nil || len(list) != 1 || list[0].Kind != chats.ThreadStarted || list[0].Message == nil || list[0].Message.HTML == "" {
		t.Fatalf("Updates(0) = %+v, %v; want the thread started", list, err)
	}
	if none, err := f.service.Updates(ctx, f.rae, f.feature, list[0].Cursor, 0); err != nil || len(none) != 0 {
		t.Errorf("Updates(after it) = %+v, %v; want none", none, err)
	}
	if none, err := f.service.Updates(ctx, f.rae, f.feature, list[0].Cursor, 50*time.Millisecond); err != nil || len(none) != 0 {
		t.Errorf("Updates(after it, waiting) = %+v, %v; want none once the wait ends", none, err)
	}
	if _, err := f.service.Updates(ctx, f.nia, f.feature, 0, 0); !errors.Is(err, repos.ErrNotFound) {
		t.Errorf("an outsider's Updates() = %v, want %v", err, repos.ErrNotFound)
	}

	done := make(chan []chats.Update)
	go func() {
		list, err := f.service.Updates(ctx, f.rae, f.feature, 1, 10*time.Second)
		if err != nil {
			t.Error(err)
		}
		done <- list
	}()
	for f.notifier.Watching(f.feature.BranchID) == 0 {
		time.Sleep(time.Millisecond)
	}
	if _, err := f.service.Post(ctx, f.dev, f.feature, th.ID, chats.MessageInput{Body: "Still there?"}); err != nil {
		t.Fatal(err)
	}
	f.notifier.Wake(f.feature.BranchID)
	select {
	case got := <-done:
		if len(got) != 1 || got[0].Kind != chats.MessagePosted || got[0].Message.Body != "Still there?" {
			t.Errorf("Updates(1, waiting) = %+v; want the message", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Updates() waited past the wake")
	}
}

func TestFollow(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	th := f.start(t, f.dev, "Hi")
	if _, err := f.service.Follow(ctx, f.nia, f.feature, 0); !errors.Is(err, repos.ErrNotFound) {
		t.Errorf("an outsider's Follow() = %v, want %v", err, repos.ErrNotFound)
	}
	updates, err := f.service.Follow(ctx, f.rae, f.feature, 0)
	if err != nil {
		t.Fatal(err)
	}
	next := func(what string) (chats.Update, bool) {
		t.Helper()
		select {
		case u, ok := <-updates:
			return u, ok
		case <-time.After(5 * time.Second):
			t.Fatalf("no update: %s", what)
			return chats.Update{}, false
		}
	}
	if u, ok := next("the thread started"); !ok || u.Kind != chats.ThreadStarted || u.Thread.ID != th.ID {
		t.Fatalf("first update = %+v, %v", u, ok)
	}
	if _, err := f.service.Resolve(ctx, f.dev, f.feature, th.ID, true, 1); err != nil {
		t.Fatal(err)
	}
	f.notifier.Wake(f.feature.BranchID)
	if u, ok := next("the thread resolved"); !ok || u.Kind != chats.ThreadResolved || u.Thread.ResolvedAt == nil || u.Message != nil {
		t.Fatalf("second update = %+v, %v", u, ok)
	}
	f.grants.revoke(f.rae.ID)
	if _, err := f.service.Post(ctx, f.dev, f.feature, th.ID, chats.MessageInput{Body: "Secret"}); err != nil {
		t.Fatal(err)
	}
	f.notifier.Wake(f.feature.BranchID)
	if u, ok := next("the follow ending"); ok {
		t.Errorf("an update after the viewer lost access: %+v", u)
	}
}
