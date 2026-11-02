package chats_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/chats"
	"cobranch.dev/internal/services/chats/chatstest"
	"cobranch.dev/internal/services/orgs/orgstest"
	"cobranch.dev/internal/services/repos"
)

// issueFixture is an organization's issue ACME-7, whose chat holds its comments, and a viewer per built-in
// organization role: Oz owns the organization, Mem is a member, Gus a guest; Nia holds nothing there.
type issueFixture struct {
	store             *chatstest.Store
	notifier          *chatstest.Notifier
	grants            *grants
	service           *chats.Service
	org               uuid.UUID
	issue             chats.Subject
	oz, mem, gus, nia chats.Viewer
}

func newIssueFixture(t *testing.T) *issueFixture {
	t.Helper()
	o := orgstest.NewStore()
	viewer := func(name string) chats.Viewer {
		return chats.Viewer{Principal: auth.Principal{ID: uuid.New(), Kind: auth.KindUser, DisplayName: name}}
	}
	f := &issueFixture{store: chatstest.NewStore(), notifier: &chatstest.Notifier{}, grants: &grants{o: o}, org: uuid.New(),
		oz: viewer("Oz"), mem: viewer("Mem"), gus: viewer("Gus"), nia: viewer("Nia")}
	o.Grant(f.oz.ID, auth.RoleOwner, auth.ScopeOrganization, f.org)
	o.Grant(f.mem.ID, auth.RoleMember, auth.ScopeOrganization, f.org)
	o.Grant(f.gus.ID, auth.RoleGuest, auth.ScopeOrganization, f.org)
	for _, v := range []chats.Viewer{f.oz, f.mem, f.gus, f.nia} {
		f.store.Names[v.ID] = v.DisplayName
	}
	f.store.Now = now
	f.issue = f.store.SetIssue(chats.Subject{OrgID: f.org, Number: 7, Issue: "ACME-7", Org: "acme"})
	f.service = chats.NewService(f.store, f.notifier, renderer{}, auth.NewAuthorizer(f.grants))
	return f
}

// start has v start a thread on the issue saying body.
func (f *issueFixture) start(t *testing.T, v chats.Viewer, body string) chats.Thread {
	t.Helper()
	th, err := f.service.StartThread(context.Background(), v, f.issue, chats.ThreadInput{Body: body})
	if err != nil {
		t.Fatal(err)
	}
	return th
}

func TestIssue(t *testing.T) {
	tests := []struct {
		name    string
		viewer  func(*issueFixture) chats.Viewer
		number  int
		wantErr error
	}{
		{name: "a member's", viewer: func(f *issueFixture) chats.Viewer { return f.mem }, number: 7},
		{name: "no such issue", viewer: func(f *issueFixture) chats.Viewer { return f.mem }, number: 8, wantErr: repos.ErrNotFound},
		{name: "a guest's", viewer: func(f *issueFixture) chats.Viewer { return f.gus }, number: 7, wantErr: auth.ErrForbidden},
		{name: "an outsider's", viewer: func(f *issueFixture) chats.Viewer { return f.nia }, number: 7, wantErr: repos.ErrNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newIssueFixture(t)
			subj, err := f.service.Issue(context.Background(), tt.viewer(f), f.org, tt.number)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Issue() = %v, want %v", err, tt.wantErr)
			}
			if err == nil && (subj != f.issue || !subj.OfIssue() || subj.ID() != f.issue.IssueID) {
				t.Errorf("Issue() = %+v, want %+v", subj, f.issue)
			}
		})
	}
}

func TestIssueStartThread(t *testing.T) {
	tests := []struct {
		name    string
		viewer  func(*issueFixture) chats.Viewer
		in      chats.ThreadInput
		wantErr error
	}{
		{name: "a member's", viewer: func(f *issueFixture) chats.Viewer { return f.mem }, in: chats.ThreadInput{Body: "Seen in Safari too"}},
		{name: "an owner's", viewer: func(f *issueFixture) chats.Viewer { return f.oz }, in: chats.ThreadInput{Body: "On it"}},
		{name: "anchored", viewer: func(f *issueFixture) chats.Viewer { return f.mem },
			in: chats.ThreadInput{Anchor: &chats.Anchor{SHA: sha(1), Path: "a", StartLine: 1, EndLine: 1}, Body: "Here"}, wantErr: invalid("anchor.not_allowed")},
		{name: "a guest's", viewer: func(f *issueFixture) chats.Viewer { return f.gus }, in: chats.ThreadInput{Body: "Hi"}, wantErr: auth.ErrForbidden},
		{name: "an outsider's", viewer: func(f *issueFixture) chats.Viewer { return f.nia }, in: chats.ThreadInput{Body: "Hi"}, wantErr: repos.ErrNotFound},
		{name: "blank", viewer: func(f *issueFixture) chats.Viewer { return f.mem }, in: chats.ThreadInput{Body: " "}, wantErr: invalid("body.empty")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newIssueFixture(t)
			v := tt.viewer(f)
			th, err := f.service.StartThread(context.Background(), v, f.issue, tt.in)
			if !is(err, tt.wantErr) {
				t.Fatalf("StartThread() = %v, want %v", err, tt.wantErr)
			}
			if err == nil && (th.StartedBy.ID != v.ID || th.Anchor != nil || th.Outdated || len(th.Messages) != 1 ||
				th.Messages[0].HTML != "<p>"+tt.in.Body+"</p><!-- acme/issues/ACME-7 -->") {
				t.Errorf("StartThread() = %+v; want its message rendered beside the issue", th)
			}
		})
	}
}

// issueOp is a change to a member's thread on the issue, or to its first message, by a viewer of the issue fixture's.
type issueOp struct {
	name    string
	viewer  func(*issueFixture) chats.Viewer
	do      func(ctx context.Context, f *issueFixture, v chats.Viewer, th chats.Thread) error
	wantErr error
}

func TestIssueChanges(t *testing.T) {
	post := func(ctx context.Context, f *issueFixture, v chats.Viewer, th chats.Thread) error {
		_, err := f.service.Post(ctx, v, f.issue, th.ID, chats.MessageInput{Body: "Agreed"})
		return err //nolint:wrapcheck // the error under test
	}
	edit := func(ctx context.Context, f *issueFixture, v chats.Viewer, th chats.Thread) error {
		_, err := f.service.Edit(ctx, v, f.issue, th.ID, th.Messages[0].ID, "Fixed", chats.AnyVersion)
		return err //nolint:wrapcheck // the error under test
	}
	remove := func(ctx context.Context, f *issueFixture, v chats.Viewer, th chats.Thread) error {
		return f.service.Delete(ctx, v, f.issue, th.ID, th.Messages[0].ID, chats.AnyVersion) //nolint:wrapcheck // the error under test
	}
	resolve := func(ctx context.Context, f *issueFixture, v chats.Viewer, th chats.Thread) error {
		_, err := f.service.Resolve(ctx, v, f.issue, th.ID, true, chats.AnyVersion)
		return err //nolint:wrapcheck // the error under test
	}
	mem := func(f *issueFixture) chats.Viewer { return f.mem }
	oz := func(f *issueFixture) chats.Viewer { return f.oz }
	gus := func(f *issueFixture) chats.Viewer { return f.gus }
	tests := []issueOp{
		{name: "an owner replies", viewer: oz, do: post},
		{name: "a guest replies", viewer: gus, do: post, wantErr: auth.ErrForbidden},
		{name: "its author edits", viewer: mem, do: edit},
		{name: "an owner edits", viewer: oz, do: edit, wantErr: auth.ErrForbidden},
		{name: "its author deletes", viewer: mem, do: remove},
		{name: "an owner deletes", viewer: oz, do: remove},
		{name: "its starter resolves", viewer: mem, do: resolve},
		{name: "an owner resolves", viewer: oz, do: resolve},
		{name: "an outsider resolves", viewer: func(f *issueFixture) chats.Viewer { return f.nia }, do: resolve, wantErr: repos.ErrNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newIssueFixture(t)
			th := f.start(t, f.mem, "Typo")
			if err := tt.do(context.Background(), f, tt.viewer(f), th); !is(err, tt.wantErr) {
				t.Errorf("%s = %v, want %v", tt.name, err, tt.wantErr)
			}
		})
	}
	t.Run("another member", func(t *testing.T) {
		f := newIssueFixture(t)
		th := f.start(t, f.oz, "Owner's")
		for name, do := range map[string]func(context.Context, *issueFixture, chats.Viewer, chats.Thread) error{"deletes": remove, "resolves": resolve} {
			if err := do(context.Background(), f, f.mem, th); !errors.Is(err, auth.ErrForbidden) {
				t.Errorf("a member %s the owner's = %v, want %v", name, err, auth.ErrForbidden)
			}
		}
	})
}

func TestIssueChatApart(t *testing.T) {
	f := newIssueFixture(t)
	ctx := context.Background()
	th := f.start(t, f.mem, "On the issue")
	other := f.store.SetIssue(chats.Subject{OrgID: f.org, Number: 8, Issue: "ACME-8", Org: "acme"})
	if _, err := f.service.Thread(ctx, f.mem, other, th.ID); !errors.Is(err, repos.ErrNotFound) {
		t.Errorf("Thread() through another issue = %v, want %v", err, repos.ErrNotFound)
	}
	page, err := f.service.Threads(ctx, f.mem, other, uuid.Nil, 10)
	if err != nil || len(page.Threads) != 0 {
		t.Errorf("Threads() of another issue = %+v, %v; want none", page, err)
	}
	if page, err := f.service.Threads(ctx, f.mem, f.issue, uuid.Nil, 10); err != nil || len(page.Threads) != 1 || page.Threads[0].ID != th.ID {
		t.Errorf("Threads() = %+v, %v; want its thread", page, err)
	}
}

func TestIssueFollow(t *testing.T) {
	f := newIssueFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	th := f.start(t, f.mem, "Hi")
	updates, err := f.service.Follow(ctx, f.oz, f.issue, 0)
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
	if u, ok := next("the thread started"); !ok || u.Kind != chats.ThreadStarted || u.Thread.ID != th.ID || u.Message.HTML == "" {
		t.Fatalf("first update = %+v, %v", u, ok)
	}
	if f.notifier.Watching(f.issue.IssueID) != 1 {
		t.Errorf("Watching(the issue) = %d, want 1", f.notifier.Watching(f.issue.IssueID))
	}
	f.store.DeleteIssue(f.org, 7)
	f.notifier.Wake(f.issue.IssueID)
	if u, ok := next("the follow ending"); ok {
		t.Errorf("an update after the issue was deleted: %+v", u)
	}
	if _, err := f.service.Updates(ctx, f.oz, f.issue, 0, 0); !errors.Is(err, repos.ErrNotFound) {
		t.Errorf("Updates() of a deleted issue = %v, want %v", err, repos.ErrNotFound)
	}
}
