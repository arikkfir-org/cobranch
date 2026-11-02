package rest

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/chats"
)

// chatStep is one request of a conversation on feature/login's chat, against one fixture: path is relative to the
// chat's, and may name {thread} and {message}, the last ones started and posted.
type chatStep struct {
	name    string
	viewer  func(f *orgFixture) *auth.Authenticated
	method  string
	path    string
	body    string
	ifMatch string
	// move is the commit feature/login moves to before the request; empty leaves it.
	move string
	// rename is the slug website takes, as the chat sees it, before the request; empty leaves it.
	rename string
	// ifNoneMatch is the ETag the caller holds; ifMatch and it may name {tag}, the last ETag answered.
	ifNoneMatch string
	wantStatus  int
	wantCode    string
	want        []string
	wantHeader  map[string]string
	// wantTag is how the ETag answered starts: its version, quoted, and a dash.
	wantTag string
	// newTag is whether the ETag answered differs from the last one.
	newTag bool
}

// maxAs is Max, holding role on the website.
func maxAs(role uuid.UUID) func(*orgFixture) *auth.Authenticated {
	return func(f *orgFixture) *auth.Authenticated {
		f.store.Grant(f.max.User.ID, role, auth.ScopeRepository, f.website.ID)
		return &f.max
	}
}

func TestChats(t *testing.T) {
	const chat = "/v1/orgs/acme/repos/website/branches/feature%2Flogin/chat"
	anchor := `"anchor":{"sha":"` + shaOf("c") + `","path":"README.md","startLine":1,"endLine":2}`
	steps := []chatStep{
		{name: "an Owner starts an anchored thread", viewer: olivia, method: "POST", path: "/threads", body: `{"body":"Why *this*?",` + anchor + `}`,
			wantStatus: 201, want: []string{`"outdated":false`, `"html":"\u003cp\u003eWhy \u003cem\u003ethis\u003c/em\u003e?`, `"startedBy":{"displayName":"Olivia"`},
			wantTag: `"1-`, wantHeader: map[string]string{"Location": chat + "/threads/{thread}"}},
		{name: "a Reader reads the chat", viewer: maxAs(auth.RoleReader), method: "GET", path: "", wantStatus: 200,
			want: []string{`"after":"1"`, `"id":"{thread}"`, `"path":"README.md"`}},
		{name: "a Reader may not post", viewer: maxAs(auth.RoleReader), method: "POST", path: "/threads/{thread}/messages", body: `{"body":"Hi"}`,
			wantStatus: 403, wantCode: "auth.forbidden"},
		{name: "a Developer replies", viewer: maxAs(auth.RoleDeveloper), method: "POST", path: "/threads/{thread}/messages", body: `{"body":"Because"}`,
			wantStatus: 201, want: []string{`"body":"Because"`, `"author":{"displayName":"Max"`},
			wantTag: `"1-`, wantHeader: map[string]string{"Location": chat + "/threads/{thread}/messages/{message}"}},
		{name: "the reply, read", viewer: olivia, method: "GET", path: "/threads/{thread}/messages/{message}", wantStatus: 200,
			wantTag: `"1-`},
		{name: "the reply, still the one held", viewer: olivia, method: "GET", path: "/threads/{thread}/messages/{message}", ifNoneMatch: "{tag}",
			wantStatus: 304, wantTag: `"1-`},
		{name: "the reply bumped the thread's version", viewer: olivia, method: "GET", path: "/threads/{thread}", ifNoneMatch: "{tag}",
			wantStatus: 200, want: []string{`"version":2`}, wantTag: `"2-`},
		{name: "the thread, still the one held", viewer: olivia, method: "GET", path: "/threads/{thread}", ifNoneMatch: "{tag}",
			wantStatus: 304, wantTag: `"2-`},
		{name: "another's ID", viewer: olivia, method: "POST", path: "/threads/{thread}/messages", body: `{"id":"{message}","body":"Mine"}`,
			wantStatus: 409, wantCode: "chat.id_taken"},
		{name: "an edit names the ETag", viewer: maxAs(auth.RoleDeveloper), method: "PATCH", path: "/threads/{thread}/messages/{message}",
			body: `{"body":"Because, really"}`, wantStatus: 428, wantCode: "precondition.required"},
		{name: "its author edits it", viewer: maxAs(auth.RoleDeveloper), method: "PATCH", path: "/threads/{thread}/messages/{message}",
			body: `{"body":"Because, really"}`, ifMatch: `"1"`, wantStatus: 200, want: []string{`"body":"Because, really"`, `"editedAt":`},
			wantTag: `"2-`},
		{name: "a stale edit", viewer: maxAs(auth.RoleDeveloper), method: "PATCH", path: "/threads/{thread}/messages/{message}",
			body: `{"body":"Stale"}`, ifMatch: `"1"`, wantStatus: 412, wantCode: "precondition.failed"},
		{name: "another may not edit it", viewer: olivia, method: "PATCH", path: "/threads/{thread}/messages/{message}",
			body: `{"body":"Mine now"}`, ifMatch: "*", wantStatus: 403, wantCode: "auth.forbidden"},
		{name: "a resolution from before the edit", viewer: olivia, method: "PATCH", path: "/threads/{thread}", body: `{"resolved":true}`, ifMatch: `"2"`,
			wantStatus: 412, wantCode: "precondition.failed"},
		{name: "its starter resolves the thread", viewer: olivia, method: "PATCH", path: "/threads/{thread}", body: `{"resolved":true}`, ifMatch: `"3"`,
			wantStatus: 200, want: []string{`"resolvedBy":{"displayName":"Olivia"`, `"version":4`}, wantTag: `"4-`},
		{name: "the updates, in order", viewer: olivia, method: "GET", path: "/updates?after=0", wantStatus: 200,
			want: []string{`"after":"4"`, `"kind":"thread.started"`, `"kind":"message.posted"`, `"kind":"message.edited"`, `"kind":"thread.resolved"`}},
		{name: "none after the last", viewer: olivia, method: "GET", path: "/updates?after=4&wait=0", wantStatus: 200,
			want: []string{`{"after":"4","updates":[]}`}},
		{name: "an Owner moderates a reply away", viewer: olivia, method: "DELETE", path: "/threads/{thread}/messages/{message}", ifMatch: "*",
			wantStatus: 204},
		{name: "the deleted reply keeps its place", viewer: olivia, method: "GET", path: "/threads/{thread}", wantStatus: 200,
			want: []string{`"body":""`, `"deletedBy":{"displayName":"Olivia"`}},
		{name: "a deleted reply takes no edit", viewer: maxAs(auth.RoleDeveloper), method: "PATCH", path: "/threads/{thread}/messages/{message}",
			body: `{"body":"Back"}`, ifMatch: "*", wantStatus: 409, wantCode: "message.deleted"},
		{name: "a blank thread", viewer: olivia, method: "POST", path: "/threads", body: `{"body":" "}`, wantStatus: 400, wantCode: "body.empty"},
		{name: "a commit the repository lacks", viewer: olivia, method: "POST", path: "/threads",
			body: `{"body":"Hm","anchor":{"sha":"` + shaOf("f") + `","path":"a","startLine":1,"endLine":1}}`, wantStatus: 400, wantCode: "anchor.commit_not_found"},
		{name: "a cursor of no page", viewer: olivia, method: "GET", path: "?cursor=nope", wantStatus: 400, wantCode: "cursor.invalid"},
		{name: "after names no update", viewer: olivia, method: "GET", path: "/updates?after=nope", wantStatus: 400, wantCode: "cursor.invalid"},
		{name: "a wait past 15 seconds", viewer: olivia, method: "GET", path: "/updates?after=0&wait=60", wantStatus: 400, wantCode: "wait.invalid"},
		{name: "no such thread", viewer: olivia, method: "GET", path: "/threads/" + uuid.NewString(), wantStatus: 404, wantCode: "thread.not_found"},
		{name: "an outsider finds no repository", viewer: nia, method: "GET", path: "", wantStatus: 404, wantCode: "repository.not_found"},
	}
	f := newOrgFixture(t)
	var ids chatIDs
	for _, s := range steps {
		s.run(t, f, chat, &ids)
	}
}

func TestChatTagsFollowHowItReads(t *testing.T) {
	const chat = "/v1/orgs/acme/repos/website/branches/feature%2Flogin/chat"
	anchor := `"anchor":{"sha":"` + shaOf("c") + `","path":"README.md","startLine":1,"endLine":2}`
	steps := []chatStep{
		{name: "an Owner starts an anchored thread", viewer: olivia, method: "POST", path: "/threads", body: `{"body":"Why?",` + anchor + `}`,
			wantStatus: 201, want: []string{`"outdated":false`}, wantTag: `"1-`},
		{name: "she replies with an image", viewer: olivia, method: "POST", path: "/threads/{thread}/messages", body: `{"body":"![logo](logo.png)"}`,
			wantStatus: 201, want: []string{shaOf("c"), "/website/"}, wantTag: `"1-`},
		{name: "the reply, still the one held", viewer: olivia, method: "GET", path: "/threads/{thread}/messages/{message}", ifNoneMatch: "{tag}",
			wantStatus: 304, wantTag: `"1-`},
		{name: "a push changes how the reply reads", move: "d", viewer: olivia, method: "GET", path: "/threads/{thread}/messages/{message}", ifNoneMatch: "{tag}",
			wantStatus: 200, want: []string{shaOf("d")}, wantTag: `"1-`, newTag: true},
		{name: "a rename changes how the reply reads", rename: "site", viewer: olivia, method: "GET", path: "/threads/{thread}/messages/{message}", ifNoneMatch: "{tag}",
			wantStatus: 200, want: []string{"/site/"}, wantTag: `"1-`, newTag: true},
		{name: "the thread, outdated", viewer: olivia, method: "GET", path: "/threads/{thread}", wantStatus: 200, want: []string{`"outdated":true`}, wantTag: `"2-`},
		{name: "the thread, still the one held", viewer: olivia, method: "GET", path: "/threads/{thread}", ifNoneMatch: "{tag}", wantStatus: 304, wantTag: `"2-`},
		{name: "a change from before a push applies", move: "b", viewer: olivia, method: "PATCH", path: "/threads/{thread}", body: `{"resolved":true}`,
			ifMatch: "{tag}", wantStatus: 200, want: []string{`"version":3`}, wantTag: `"3-`, newTag: true},
	}
	f := newOrgFixture(t)
	var ids chatIDs
	for _, s := range steps {
		s.run(t, f, chat, &ids)
	}
}

// chatIDs are the last thread started and message posted, and the last ETag answered.
type chatIDs struct{ thread, message, tag string }

func (ids *chatIDs) expand(s string) string {
	return strings.NewReplacer("{thread}", ids.thread, "{message}", ids.message, "{tag}", ids.tag).Replace(s)
}

// run takes the step on chat, the path of feature/login's chat, keeping in ids the thread it starts or the message it
// posts.
func (s chatStep) run(t *testing.T, f *orgFixture, chat string, ids *chatIDs) {
	t.Helper()
	s.change(t, f)
	header := http.Header{}
	if s.ifMatch != "" {
		header.Set("If-Match", ids.expand(s.ifMatch))
	}
	if s.ifNoneMatch != "" {
		header.Set("If-None-Match", ids.expand(s.ifNoneMatch))
	}
	if s.method == "PATCH" {
		header.Set("Content-Type", "application/merge-patch+json")
	}
	rec := f.callWith(s.viewer(f), s.method, ids.expand(chat+s.path), ids.expand(s.body), header)
	if rec.Code != s.wantStatus {
		t.Fatalf("%s: status = %d, want %d: %s", s.name, rec.Code, s.wantStatus, rec.Body)
	}
	if s.wantCode != "" {
		checkBody(t, rec, "", s.wantCode)
	}
	if loc := rec.Header().Get("Location"); strings.Contains(loc, "/messages/") {
		ids.message = loc[strings.LastIndex(loc, "/")+1:]
	} else if loc != "" {
		ids.thread = loc[strings.LastIndex(loc, "/")+1:]
	}
	s.checkTag(t, rec.Header().Get("ETag"), ids)
	for k, v := range s.wantHeader {
		if got := rec.Header().Get(k); got != ids.expand(v) {
			t.Errorf("%s: %s = %q, want %q", s.name, k, got, ids.expand(v))
		}
	}
	for _, w := range s.want {
		if !strings.Contains(rec.Body.String(), ids.expand(w)) {
			t.Errorf("%s: body = %s, want %s in it", s.name, rec.Body, ids.expand(w))
		}
	}
}

// change moves feature/login, or renames website as its chat sees it, as the step says.
func (s chatStep) change(t *testing.T, f *orgFixture) {
	t.Helper()
	if s.move == "" && s.rename == "" {
		return
	}
	subj, err := f.chatStore.Subject(t.Context(), f.website.ID, "feature/login")
	if err != nil {
		t.Fatalf("%s: %v", s.name, err)
	}
	if s.move != "" {
		f.chatStore.Move(subj.BranchID, shaOf(s.move))
	}
	if s.rename != "" {
		subj.Repo = s.rename
		f.chatStore.SetBranch(subj)
	}
}

// checkTag checks the ETag answered against the step's, and keeps it in ids as the last one.
func (s chatStep) checkTag(t *testing.T, tag string, ids *chatIDs) {
	t.Helper()
	if tag == "" {
		if s.wantTag != "" {
			t.Errorf("%s: no ETag, want one starting %s", s.name, s.wantTag)
		}
		return
	}
	if !strings.HasPrefix(tag, s.wantTag) {
		t.Errorf("%s: ETag = %s, want it to start %s", s.name, tag, s.wantTag)
	}
	if s.newTag && tag == ids.tag {
		t.Errorf("%s: ETag = %s, the one before, want another", s.name, tag)
	}
	ids.tag = tag
}

func TestChatThreadNamesItsReview(t *testing.T) {
	review := uuid.New()
	tests := []struct {
		name   string
		review *uuid.UUID
		want   string
	}{
		{name: "a review's comment", review: &review, want: `"reviewId":"` + review.String() + `"`},
		{name: "a thread started in the chat"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, err := json.Marshal(chatThread(chats.Thread{ID: uuid.New(), ReviewID: tt.review}))
			if err != nil {
				t.Fatal(err)
			}
			if got := string(body); tt.want != "" && !strings.Contains(got, tt.want) || tt.want == "" && strings.Contains(got, "reviewId") {
				t.Errorf("chatThread() = %s, want %q", got, tt.want)
			}
		})
	}
}
