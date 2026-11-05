package chats_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/chats"
	"cobranch.dev/internal/services/chats/chatstest"
	"cobranch.dev/internal/services/orgs/orgstest"
	"cobranch.dev/internal/services/repos"
)

// conversationFixture is organization acme with repository website, at commit 1 on its default branch, and agent
// Builder, and a viewer per kind of access: Mem is a member who develops website, Gus a guest, Rae reads website alone;
// Nia holds nothing there.
type conversationFixture struct {
	store              *chatstest.Store
	notifier           *chatstest.Notifier
	grants             *grants
	o                  *orgstest.Store
	service            *chats.Service
	org, repo, agent   uuid.UUID
	mem, gus, rae, nia chats.Viewer
}

func newConversationFixture(t *testing.T) *conversationFixture {
	t.Helper()
	o := orgstest.NewStore()
	viewer := func(name string) chats.Viewer {
		return chats.Viewer{Principal: auth.Principal{ID: uuid.New(), Kind: auth.KindUser, DisplayName: name}}
	}
	f := &conversationFixture{store: chatstest.NewStore(), notifier: &chatstest.Notifier{}, grants: &grants{o: o}, o: o, org: uuid.New(), repo: uuid.New(), agent: uuid.New(),
		mem: viewer("Mem"), gus: viewer("Gus"), rae: viewer("Rae"), nia: viewer("Nia")}
	o.Repos[f.repo] = f.org
	o.Grant(f.mem.ID, auth.RoleMember, auth.ScopeOrganization, f.org)
	o.Grant(f.mem.ID, auth.RoleDeveloper, auth.ScopeRepository, f.repo)
	o.Grant(f.gus.ID, auth.RoleGuest, auth.ScopeOrganization, f.org)
	o.Grant(f.rae.ID, auth.RoleReader, auth.ScopeRepository, f.repo)
	for _, v := range []chats.Viewer{f.mem, f.gus, f.rae, f.nia} {
		f.store.Names[v.ID] = v.DisplayName
	}
	f.store.Now = now
	f.store.SetOrganization(f.org, "acme")
	f.store.SetRepository(f.org, f.repo, "website", sha(1))
	f.store.SetAgent(f.org, f.agent, "Builder")
	f.service = chats.NewService(f.store, f.notifier, renderer{}, auth.NewAuthorizer(f.grants))
	return f
}

// find finds v's conversation of kind: about acme, about website, or with Builder.
func (f *conversationFixture) find(ctx context.Context, v chats.Viewer, kind chats.Kind) (chats.Subject, error) {
	var subj chats.Subject
	var err error
	switch kind {
	case chats.KindRepository:
		subj, err = f.service.Repository(ctx, v, f.org, f.repo)
	case chats.KindAgent:
		subj, err = f.service.Agent(ctx, v, f.org, f.agent)
	default:
		subj, err = f.service.Organization(ctx, v, f.org)
	}
	if err != nil {
		return chats.Subject{}, fmt.Errorf("finding %s's %s conversation: %w", v.DisplayName, kind, err)
	}
	return subj, nil
}

// mine finds v's conversation of kind, or fails the test.
func (f *conversationFixture) mine(t *testing.T, v chats.Viewer, kind chats.Kind) chats.Subject {
	t.Helper()
	subj, err := f.find(context.Background(), v, kind)
	if err != nil {
		t.Fatal(err)
	}
	return subj
}

func TestConversation(t *testing.T) {
	tests := []struct {
		name    string
		kind    chats.Kind
		viewer  func(*conversationFixture) chats.Viewer
		wantErr error
	}{
		{name: "a member's about the organization", kind: chats.KindOrganization, viewer: func(f *conversationFixture) chats.Viewer { return f.mem }},
		{name: "a guest's about the organization", kind: chats.KindOrganization, viewer: func(f *conversationFixture) chats.Viewer { return f.gus }},
		{name: "an outsider's about the organization", kind: chats.KindOrganization, viewer: func(f *conversationFixture) chats.Viewer { return f.nia },
			wantErr: repos.ErrNotFound},
		{name: "a member's about a repository", kind: chats.KindRepository, viewer: func(f *conversationFixture) chats.Viewer { return f.mem }},
		{name: "a reader's about a repository", kind: chats.KindRepository, viewer: func(f *conversationFixture) chats.Viewer { return f.rae }},
		{name: "a guest's about a repository they don't read", kind: chats.KindRepository, viewer: func(f *conversationFixture) chats.Viewer { return f.gus },
			wantErr: repos.ErrNotFound},
		{name: "an outsider's about a repository", kind: chats.KindRepository, viewer: func(f *conversationFixture) chats.Viewer { return f.nia },
			wantErr: repos.ErrNotFound},
		{name: "a member's with an agent", kind: chats.KindAgent, viewer: func(f *conversationFixture) chats.Viewer { return f.mem }},
		{name: "a guest's with an agent", kind: chats.KindAgent, viewer: func(f *conversationFixture) chats.Viewer { return f.gus }},
		{name: "an outsider's with an agent", kind: chats.KindAgent, viewer: func(f *conversationFixture) chats.Viewer { return f.nia },
			wantErr: repos.ErrNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newConversationFixture(t)
			v := tt.viewer(f)
			subj, err := f.find(context.Background(), v, tt.kind)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("finding the conversation = %v, want %v", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			repo, agent := uuid.Nil, uuid.Nil
			switch tt.kind {
			case chats.KindRepository:
				repo = f.repo
			case chats.KindAgent:
				agent = f.agent
			}
			if subj.Kind() != tt.kind || !subj.Private() || subj.OfIssue() || subj.OwnerID != v.ID || subj.Org != "acme" ||
				subj.ID() != chats.ConversationID(v.ID, f.org, repo, agent) {
				t.Errorf("the conversation = %+v (kind %s), want %s's own %s conversation", subj, subj.Kind(), v.DisplayName, tt.kind)
			}
		})
	}
}

func TestConversationSubjectGone(t *testing.T) {
	f := newConversationFixture(t)
	ctx := context.Background()
	if _, err := f.service.Repository(ctx, f.mem, f.org, uuid.New()); !errors.Is(err, repos.ErrNotFound) {
		t.Errorf("Repository(another organization's) = %v, want %v", err, repos.ErrNotFound)
	}
	if _, err := f.service.Agent(ctx, f.mem, f.org, uuid.New()); !errors.Is(err, repos.ErrNotFound) {
		t.Errorf("Agent(another organization's) = %v, want %v", err, repos.ErrNotFound)
	}
	subj := f.mine(t, f.mem, chats.KindRepository)
	f.store.DeleteRepository(f.org, f.repo)
	if _, err := f.service.Updates(ctx, f.mem, subj, 0, 0); !errors.Is(err, repos.ErrNotFound) {
		t.Errorf("Updates() once the repository is gone = %v, want %v", err, repos.ErrNotFound)
	}
}

func TestConversationIDs(t *testing.T) {
	owner, org, repo, agent := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	ids := map[uuid.UUID]string{}
	for name, id := range map[string]uuid.UUID{
		"organization":                chats.ConversationID(owner, org, uuid.Nil, uuid.Nil),
		"repository":                  chats.ConversationID(owner, org, repo, uuid.Nil),
		"agent":                       chats.ConversationID(owner, org, uuid.Nil, agent),
		"another's":                   chats.ConversationID(uuid.New(), org, uuid.Nil, uuid.Nil),
		"another organization's":      chats.ConversationID(owner, uuid.New(), uuid.Nil, uuid.Nil),
		"an agent named as a repo":    chats.ConversationID(owner, org, agent, uuid.Nil),
		"a repository named as agent": chats.ConversationID(owner, org, uuid.Nil, repo),
	} {
		if other, taken := ids[id]; taken {
			t.Errorf("the %s conversation's ID %s is the %s one's too", name, id, other)
		}
		ids[id] = name
	}
	if a, b := chats.ConversationID(owner, org, repo, uuid.Nil), chats.ConversationID(owner, org, repo, uuid.Nil); a != b {
		t.Errorf("ConversationID() = %s, then %s; want the same", a, b)
	}
}

// TestConversationPrivate holds that a conversation is its owner's alone: they post, edit, delete, resolve and follow
// there, and anyone else, even one who reads what it is about, finds nothing.
func TestConversationPrivate(t *testing.T) {
	for _, kind := range []chats.Kind{chats.KindOrganization, chats.KindRepository, chats.KindAgent} {
		t.Run(string(kind), func(t *testing.T) {
			f := newConversationFixture(t)
			mine := f.mine(t, f.mem, kind)
			th, m := f.converse(t, mine)
			other := f.gus
			if kind == chats.KindRepository {
				other = f.rae
			}
			f.findNothing(t, other, kind, mine, th.ID, m.ID)
		})
	}
}

// converse has Mem start a thread in mine, post to it, edit, resolve, follow and delete there, and answers the thread
// and the message.
func (f *conversationFixture) converse(t *testing.T, mine chats.Subject) (chats.Thread, chats.Message) {
	t.Helper()
	ctx := context.Background()
	th, err := f.service.StartThread(ctx, f.mem, mine, chats.ThreadInput{Body: "What changed this week?"})
	if err != nil {
		t.Fatalf("StartThread() = %v", err)
	}
	m, err := f.service.Post(ctx, f.mem, mine, th.ID, chats.MessageInput{Body: "And why?"})
	if err != nil {
		t.Fatalf("Post() = %v", err)
	}
	if _, err := f.service.Edit(ctx, f.mem, mine, th.ID, m.ID, "And who?", m.Version); err != nil {
		t.Errorf("Edit() = %v", err)
	}
	if _, err := f.service.Resolve(ctx, f.mem, mine, th.ID, true, chats.AnyVersion); err != nil {
		t.Errorf("Resolve() = %v", err)
	}
	if updates, err := f.service.Updates(ctx, f.mem, mine, 0, 0); err != nil || len(updates) != 4 {
		t.Errorf("Updates() = %d updates, %v; want 4", len(updates), err)
	}
	if err := f.service.Delete(ctx, f.mem, mine, th.ID, m.ID, chats.AnyVersion); err != nil {
		t.Errorf("Delete() = %v", err)
	}
	return th, m
}

// findNothing checks that other, who reads what Mem's conversation mine is about, finds none of it, and their own
// conversation of kind empty.
func (f *conversationFixture) findNothing(t *testing.T, other chats.Viewer, kind chats.Kind, mine chats.Subject, thread, message uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	theirs := f.mine(t, other, kind)
	if page, err := f.service.Threads(ctx, other, theirs, uuid.Nil, 10); err != nil || len(page.Threads) != 0 || page.After != 0 {
		t.Errorf("%s's Threads() = %+v, %v; want none of %s's", other.DisplayName, page, err, f.mem.DisplayName)
	}
	_, threads := f.service.Threads(ctx, other, mine, uuid.Nil, 10)
	_, read := f.service.Thread(ctx, other, mine, thread)
	_, msg := f.service.Message(ctx, other, mine, thread, message)
	_, post := f.service.Post(ctx, other, mine, thread, chats.MessageInput{Body: "Let me in"})
	_, start := f.service.StartThread(ctx, other, mine, chats.ThreadInput{Body: "Let me in"})
	_, resolve := f.service.Resolve(ctx, other, mine, thread, false, chats.AnyVersion)
	_, updates := f.service.Updates(ctx, other, mine, 0, 0)
	_, follow := f.service.Follow(ctx, other, mine, 0)
	for name, err := range map[string]error{"Threads": threads, "Thread": read, "Message": msg, "Post": post, "StartThread": start, "Resolve": resolve,
		"Delete": f.service.Delete(ctx, other, mine, thread, message, chats.AnyVersion), "Updates": updates, "Follow": follow} {
		if !errors.Is(err, repos.ErrNotFound) {
			t.Errorf("%s's %s() on %s's conversation = %v, want %v", other.DisplayName, name, f.mem.DisplayName, err, repos.ErrNotFound)
		}
	}
}

func TestConversationAnchor(t *testing.T) {
	f := newConversationFixture(t)
	subj := f.mine(t, f.mem, chats.KindRepository)
	_, err := f.service.StartThread(context.Background(), f.mem, subj, chats.ThreadInput{Body: "Here",
		Anchor: &chats.Anchor{SHA: sha(1), Path: "main.go", StartLine: 1, EndLine: 2}})
	if !is(err, invalid("anchor.not_allowed")) {
		t.Errorf("StartThread(anchored) = %v, want anchor.not_allowed", err)
	}
}

// TestConversationAccessLost holds that a conversation is its owner's only while they read what it is about.
func TestConversationAccessLost(t *testing.T) {
	f := newConversationFixture(t)
	ctx := context.Background()
	subj := f.mine(t, f.mem, chats.KindOrganization)
	if _, err := f.service.StartThread(ctx, f.mem, subj, chats.ThreadInput{Body: "Notes"}); err != nil {
		t.Fatal(err)
	}
	f.grants.revoke(f.mem.ID)
	if _, err := f.service.Threads(ctx, f.mem, subj, uuid.Nil, 10); !errors.Is(err, repos.ErrNotFound) {
		t.Errorf("Threads() once the owner left = %v, want %v", err, repos.ErrNotFound)
	}
}

func TestConversationRender(t *testing.T) {
	tests := []struct {
		kind chats.Kind
		want func(f *conversationFixture) string
	}{
		{kind: chats.KindOrganization, want: func(*conversationFixture) string { return "<p>See [the docs](docs)</p><!-- acme/@: -->" }},
		{kind: chats.KindRepository, want: func(*conversationFixture) string {
			return "<p>See [the docs](docs)</p><!-- acme/website@main:" + sha(1) + " -->"
		}},
		{kind: chats.KindAgent, want: func(f *conversationFixture) string {
			return "<p>See [the docs](docs)</p><!-- acme/agents/" + f.agent.String() + " -->"
		}},
	}
	for _, tt := range tests {
		t.Run(string(tt.kind), func(t *testing.T) {
			f := newConversationFixture(t)
			subj := f.mine(t, f.mem, tt.kind)
			th, err := f.service.StartThread(context.Background(), f.mem, subj, chats.ThreadInput{Body: "See [the docs](docs)"})
			if err != nil {
				t.Fatal(err)
			}
			if got, want := th.Messages[0].HTML, tt.want(f); got != want {
				t.Errorf("the message's HTML = %q, want %q", got, want)
			}
		})
	}
}

func TestConversationFollow(t *testing.T) {
	f := newConversationFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	mine := f.mine(t, f.mem, chats.KindAgent)
	theirs := f.mine(t, f.gus, chats.KindAgent)
	updates, err := f.service.Follow(ctx, f.mem, mine, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.StartThread(ctx, f.gus, theirs, chats.ThreadInput{Body: "Gus's own"}); err != nil {
		t.Fatal(err)
	}
	th, err := f.service.StartThread(ctx, f.mem, mine, chats.ThreadInput{Body: "Mem's own"})
	if err != nil {
		t.Fatal(err)
	}
	f.notifier.Wake(mine.ID())
	select {
	case u := <-updates:
		if u.Kind != chats.ThreadStarted || u.Thread.ID != th.ID || u.Message == nil || u.Message.Body != "Mem's own" {
			t.Errorf("the first update = %+v, want Mem's thread started", u)
		}
	case <-ctx.Done():
		t.Fatal("no update came")
	}
}
