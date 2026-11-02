package rest

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"testing"

	"cobranch.dev/internal/services/agents"
	"cobranch.dev/internal/services/auth"
)

// working is a viewer under a task token of Scout's for Acme's task number, Scout a Member of Acme.
func working(number int64) func(f *orgFixture) *auth.Authenticated {
	return func(f *orgFixture) *auth.Authenticated {
		f.store.AddMember(f.acme.ID, f.scout.ID, auth.RoleMember)
		return token(f, number)
	}
}

// workingInRepo is working with Scout holding only Developer on website, and so no org.read.
func workingInRepo(number int64) func(f *orgFixture) *auth.Authenticated {
	return func(f *orgFixture) *auth.Authenticated {
		f.store.Grant(f.scout.ID, auth.RoleDeveloper, auth.ScopeRepository, f.website.ID)
		return token(f, number)
	}
}

// token is a viewer under a task token of Scout's for Acme's task number.
func token(f *orgFixture, number int64) *auth.Authenticated {
	task, err := f.agentStore.Task(context.Background(), f.acme.ID, number)
	if err != nil {
		panic(err)
	}
	return &auth.Authenticated{User: auth.User{Principal: auth.Principal{ID: f.scout.ID, Kind: auth.KindAgent, DisplayName: "Scout"}},
		Credential: auth.Credential{Kind: auth.CredentialTaskToken, PrincipalID: f.scout.ID,
			Task: auth.TaskGrant{ID: task.ID, OrgID: f.acme.ID, AssignerID: task.AssignedBy.ID}}}
}

// appended has task 1's token append its first n entries, then gives body.
func appended(n int, body string) func(f *orgFixture) string {
	return func(f *orgFixture) string {
		v := token(f, 1)
		ctx := auth.WithViewer(context.Background(), *v)
		for seq := 1; seq <= n; seq++ {
			if _, _, err := f.agents.Append(ctx, v.User, f.acme.ID, 1, agents.Entry{Seq: seq, Kind: agents.EntryTurn, Body: json.RawMessage(`{"text":"Hm"}`)}); err != nil {
				panic(err)
			}
		}
		return body
	}
}

func wantEntries(seqs ...int) func(t *testing.T, f *orgFixture, body []byte) {
	return func(t *testing.T, _ *orgFixture, body []byte) {
		t.Helper()
		page := decode[struct{ Entries []TranscriptEntry }](t, body)
		got := []int{}
		for _, e := range page.Entries {
			got = append(got, e.Seq)
		}
		if !slices.Equal(got, seqs) {
			t.Errorf("entries = %v, want %v", got, seqs)
		}
	}
}

func TestMyTask(t *testing.T) {
	runOrgCases(t, []orgCase{
		{name: "a task token's", viewer: working(2), method: "GET", path: fixed("/v1/me/task"), wantStatus: 200,
			check: func(t *testing.T, f *orgFixture, body []byte) {
				mine := decode[MyTask](t, body)
				if mine.Organization != "acme" || mine.Task.Number != 2 || mine.Task.Agent.Id != f.scout.ID || mine.AgentInstructions != "Be brief." {
					t.Errorf("my task = %s", body)
				}
			}},
		{name: "a person's", viewer: maxim, method: "GET", path: fixed("/v1/me/task"), wantStatus: 403, wantCode: "auth.forbidden"},
	})
}

func TestTranscriptAPI(t *testing.T) {
	path := fixed("/v1/orgs/acme/tasks/1/transcript")
	runOrgCases(t, []orgCase{
		{name: "append", viewer: working(1), method: "POST", path: path, body: `{"seq":1,"kind":"input","body":{"text":"Tidy up","n":12345678901234567890}}`,
			wantStatus: 201, wantTag: `"1-`, wantHeader: http.Header{"Location": {"/v1/orgs/acme/tasks/1/transcript/1"}},
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				e := decode[TranscriptEntry](t, body)
				if e.Seq != 1 || e.Kind != "input" || string(e.Body) != `{"text":"Tidy up","n":12345678901234567890}` {
					t.Errorf("entry = %s", body)
				}
			}},
		{name: "append again", viewer: working(1), method: "POST", path: path, bodyOf: appended(1, `{"seq":1,"kind":"result","body":{}}`),
			wantStatus: 200, check: func(t *testing.T, _ *orgFixture, body []byte) {
				if e := decode[TranscriptEntry](t, body); e.Kind != "turn" {
					t.Errorf("entry = %s, want the one there", body)
				}
			}},
		{name: "append past the next", viewer: working(1), method: "POST", path: path, body: `{"seq":3,"kind":"turn","body":{}}`,
			wantStatus: 400, wantCode: "transcript.seq_invalid"},
		{name: "append a body that isn't an object", viewer: working(1), method: "POST", path: path, body: `{"seq":1,"kind":"turn","body":[]}`,
			wantStatus: 400},
		{name: "append a move the task can't make", viewer: working(1), method: "POST", path: path, body: `{"seq":1,"kind":"state","body":{"state":"waiting"}}`,
			wantStatus: 409, wantCode: "task.transition_invalid"},
		{name: "append to a task that ended", viewer: working(3), method: "POST", path: fixed("/v1/orgs/acme/tasks/3/transcript"),
			body: `{"seq":1,"kind":"turn","body":{}}`, wantStatus: 409, wantCode: "task.ended"},
		{name: "append to another task", viewer: working(1), method: "POST", path: fixed("/v1/orgs/acme/tasks/3/transcript"),
			body: `{"seq":1,"kind":"turn","body":{}}`, wantStatus: 403, wantCode: "auth.forbidden"},
		{name: "append as an agent holding only a repository role", viewer: workingInRepo(1), method: "POST", path: path,
			body: `{"seq":1,"kind":"input","body":{"text":"Tidy up"}}`, wantStatus: 201},
		{name: "list as an agent holding only a repository role", viewer: workingInRepo(1), method: "GET", path: path,
			headerFor: func(f *orgFixture) http.Header { appended(2, "")(f); return nil }, wantStatus: 200, check: wantEntries(1, 2)},
		{name: "get an entry as an agent holding only a repository role", viewer: workingInRepo(1), method: "GET",
			path: fixed("/v1/orgs/acme/tasks/1/transcript/1"), headerFor: func(f *orgFixture) http.Header { appended(1, "")(f); return nil },
			wantStatus: 200, wantTag: `"1-`},
		{name: "list another organization's as a task token", viewer: workingInRepo(1), method: "GET", path: fixed("/v1/orgs/globex/tasks/1/transcript"),
			wantStatus: 404, wantCode: "organization.not_found"},
		{name: "append as a person", viewer: maxim, method: "POST", path: path, body: `{"seq":1,"kind":"turn","body":{}}`,
			wantStatus: 403, wantCode: "auth.forbidden"},
		{name: "list as the assigner", viewer: maxim, method: "GET", path: path, headerFor: func(f *orgFixture) http.Header { appended(3, "")(f); return nil },
			wantStatus: 200, check: wantEntries(1, 2, 3)},
		{name: "list a page", viewer: maxim, method: "GET", path: fixed("/v1/orgs/acme/tasks/1/transcript?after=1&limit=1"),
			headerFor: func(f *orgFixture) http.Header { appended(3, "")(f); return nil }, wantStatus: 200, check: wantEntries(2),
			wantHeader: http.Header{"Link": {`<?after=2&limit=1>; rel="next"`}}},
		{name: "list as an owner", viewer: olivia, method: "GET", path: path, headerFor: func(f *orgFixture) http.Header { appended(1, "")(f); return nil },
			wantStatus: 200, check: wantEntries(1)},
		{name: "list as an outsider", viewer: nia, method: "GET", path: path, wantStatus: 404, wantCode: "organization.not_found"},
		{name: "get an entry", viewer: maxim, method: "GET", path: fixed("/v1/orgs/acme/tasks/1/transcript/2"),
			headerFor: func(f *orgFixture) http.Header { appended(2, "")(f); return nil }, wantStatus: 200, wantTag: `"1-`},
		{name: "get an entry the caller holds", viewer: maxim, method: "GET", path: fixed("/v1/orgs/acme/tasks/1/transcript/1"),
			headerFor: func(f *orgFixture) http.Header {
				appended(1, "")(f)
				return held(maxim, fixed("/v1/orgs/acme/tasks/1/transcript/1"))(f)
			}, wantStatus: 304},
		{name: "get an entry not there", viewer: maxim, method: "GET", path: fixed("/v1/orgs/acme/tasks/1/transcript/9"), wantStatus: 404,
			wantCode: "transcript_entry.not_found"},
	})
}
