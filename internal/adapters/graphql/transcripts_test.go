package graphql

import (
	"context"
	"encoding/json"
	"testing"

	"cobranch.dev/internal/services/agents"
	"cobranch.dev/internal/services/auth"
)

// working is a viewer under a task token of Scout's for Acme's task number, Scout a Member of Acme.
func working(number int64) func(f *orgFixture) *auth.Authenticated {
	return func(f *orgFixture) *auth.Authenticated {
		f.store.AddMember(f.acme.ID, f.scout.ID, auth.RoleMember)
		task, err := f.agentStore.Task(context.Background(), f.acme.ID, number)
		if err != nil {
			panic(err)
		}
		return &auth.Authenticated{User: auth.User{Principal: auth.Principal{ID: f.scout.ID, Kind: auth.KindAgent, DisplayName: "Scout"}},
			Credential: auth.Credential{Kind: auth.CredentialTaskToken, PrincipalID: f.scout.ID,
				Task: auth.TaskGrant{ID: task.ID, OrgID: f.acme.ID, AssignerID: task.AssignedBy.ID}}}
	}
}

// appended is a document with task 1's first n entries appended by its token first.
func appended(n int, document func(*orgFixture) string) func(*orgFixture) string {
	return func(f *orgFixture) string {
		v := working(1)(f)
		ctx := auth.WithViewer(context.Background(), *v)
		for seq := 1; seq <= n; seq++ {
			if _, _, err := f.agents.Append(ctx, v.User, f.acme.ID, 1, agents.Entry{Seq: seq, Kind: agents.EntryTurn,
				Body: json.RawMessage(`{"text":"Hm","n":12345678901234567890}`)}); err != nil {
				panic(err)
			}
		}
		return document(f)
	}
}

func TestTranscriptQueries(t *testing.T) {
	transcript := ofAcme(`task(number: 1) { organizationId transcript(after: 1, first: 1) { seq kind body } }`)
	runOrgCases(t, []orgCase{
		{name: "my task", viewer: working(2), document: fixed(`{ myTask { organization task { number title } agentInstructions } }`),
			want: []string{`"myTask":{"organization":"acme","task":{"number":2,"title":"Fix the login"},"agentInstructions":"Be brief."}`}},
		{name: "my task as a person", viewer: maxim, document: fixed(`{ myTask { organization } }`), wantCode: "FORBIDDEN"},
		{name: "a page of the transcript as its assigner", viewer: maxim, document: appended(3, transcript),
			want: []string{`"transcript":[{"seq":2,"kind":"TURN","body":{"n":12345678901234567890,"text":"Hm"}}]`}},
		{name: "the transcript as another member", viewer: maxim, document: appended(1, ofAcme(`task(number: 2) { transcript { seq } }`)),
			want: []string{`"task":null`}},
		{name: "a page too large", viewer: maxim, document: ofAcme(`task(number: 1) { transcript(first: 501) { seq } }`),
			wantCode: "BAD_USER_INPUT", wantReason: "limit.invalid"},
	})
}

func TestTranscriptMutations(t *testing.T) {
	appendOne := withScout(`mutation { appendTranscriptEntry(organizationId: "ACME", number: 1, input: {seq: 1, kind: INPUT, body: {text: "Tidy up"}}) { seq kind body } }`)
	runOrgCases(t, []orgCase{
		{name: "append", viewer: working(1), document: appendOne, want: []string{`"appendTranscriptEntry":{"seq":1,"kind":"INPUT","body":{"text":"Tidy up"}}`}},
		{name: "append again", viewer: working(1), document: appended(1, appendOne), want: []string{`"kind":"TURN"`}},
		{name: "append as a person", viewer: maxim, document: appendOne, wantCode: "FORBIDDEN"},
		{name: "append a move the task can't make", viewer: working(1),
			document: withScout(`mutation { appendTranscriptEntry(organizationId: "ACME", number: 1, input: {seq: 1, kind: STATE, body: {state: "waiting"}}) { seq } }`),
			wantCode: "CONFLICT", wantReason: "task.transition_invalid"},
		{name: "append past the next", viewer: working(1),
			document: withScout(`mutation { appendTranscriptEntry(organizationId: "ACME", number: 1, input: {seq: 2, kind: TURN, body: {}}) { seq } }`),
			wantCode: "BAD_USER_INPUT", wantReason: "transcript.seq_invalid"},
	})
}
