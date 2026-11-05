package graphql

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/agents"
)

// spent is a document with Scout on a-model, capped at $8, and its first two tasks running: 1, anywhere, has spent
// $0.30 and 2, on website, $3.00.
func spent(document func(*orgFixture) string) func(*orgFixture) string {
	return onProvider(true, func(f *orgFixture) string {
		ctx := context.Background()
		for number, input := range map[int64]int64{1: 100_000, 2: 1_000_000} {
			f.agentStore.SetState(f.acme.ID, number, agents.TaskRunning)
			task, err := f.agentStore.Task(ctx, f.acme.ID, number)
			if err != nil {
				panic(err)
			}
			if _, err := f.agentStore.RecordSpend(ctx, task.ID, agents.Usage{ID: uuid.New(), Model: "a-model", Input: input}); err != nil {
				panic(err)
			}
		}
		return document(f)
	})
}

func TestSpendQueries(t *testing.T) {
	spend := func(args string) func(*orgFixture) string {
		return ofAcme(`spend` + args + ` { month zone since until agents { agent { displayName } provider model cap tasks failedOnCap active spent ` +
			`costliest { number title spent } } models { provider model tasks inputTokens outputTokens spent } }`)
	}
	scout := `{"agent":{"displayName":"Scout"},"provider":"anthropic","model":"a-model","cap":"8.00","tasks":3,"failedOnCap":0,"active":2,"spent":"3.30",`
	reviewer := `{"agent":{"displayName":"Reviewer"},"provider":null,"model":null,"cap":null,"tasks":0,"failedOnCap":0,"active":0,"spent":"0.00","costliest":null}`
	models := `"models":[{"provider":"anthropic","model":"a-model","tasks":2,"inputTokens":1100000,"outputTokens":0,"spent":"3.30"}]`
	runOrgCases(t, []orgCase{
		{name: "an Owner's names the costliest task", viewer: olivia, document: spent(spend(`(month: "2026-10")`)),
			want: []string{`"month":"2026-10","zone":"UTC","since":"2026-10-01T00:00:00Z","until":"2026-11-01T00:00:00Z"`,
				`"agents":[` + reviewer + `,` + scout + `"costliest":{"number":2,"title":"Fix the login","spent":"3.00"}}]`, models}},
		{name: "a Member's names only one they see", viewer: maxim, document: spent(spend(`(month: "2026-10", zone: "Asia/Jerusalem")`)),
			want: []string{`"month":"2026-10","zone":"Asia/Jerusalem","since":"2026-09-30T21:00:00Z"`,
				scout + `"costliest":{"number":1,"title":"Tidy up","spent":"0.30"}}`, models}},
		{name: "a month that isn't one", viewer: maxim, document: spend(`(month: "October")`), wantCode: "BAD_USER_INPUT", wantReason: "spend.month_invalid"},
		{name: "a zone that isn't one", viewer: maxim, document: spend(`(zone: "Mars/Olympus")`), wantCode: "BAD_USER_INPUT", wantReason: "spend.zone_invalid"},
		{name: "as an outsider", viewer: nia, document: spend(``), want: []string{`"organization":null`}},
	})
}
