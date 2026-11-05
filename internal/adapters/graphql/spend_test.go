package graphql

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/agents"
	"cobranch.dev/internal/services/auth"
)

func TestModelQueries(t *testing.T) {
	models := ofAcme(`models { provider name inputPrice outputPrice cap default agents { displayName } createdBy { displayName } version }`)
	runOrgCases(t, []orgCase{
		{name: "as a Member", viewer: maxim, document: onProvider(true, models),
			want: []string{`"models":[{"provider":"anthropic","name":"a-model","inputPrice":"3.00","outputPrice":"15.00","cap":"8.00","default":false,` +
				`"agents":[{"displayName":"Scout"}],"createdBy":{"displayName":"Olivia"},"version":1}]`}},
		{name: "none", viewer: maxim, document: models, want: []string{`"models":[]`}},
		{name: "an agent's caps and a task's spend", viewer: maxim,
			document: onProvider(true, withScout(`{ organization(slug: "acme") { agent(id: "SCOUT") { cap modelCap effectiveCap } task(number: 1) { spent inputTokens outputTokens cap } } }`)),
			want:     []string{`"agent":{"cap":null,"modelCap":"8.00","effectiveCap":"8.00"}`, `"task":{"spent":"0.00","inputTokens":0,"outputTokens":0,"cap":"8.00"}`}},
	})
}

func TestModelMutations(t *testing.T) {
	set := `mutation { setModel(organizationId: "ACME", provider: "anthropic", model: "%m", input: {%i}%v) { name inputPrice outputPrice cap default version } }`
	doc := func(model, input, version string) func(*orgFixture) string {
		return withScout(strings.NewReplacer("%m", model, "%i", input, "%v", version).Replace(set))
	}
	priced := `inputPrice: "0.80", outputPrice: "4", cap: "0.000150", default: true`
	runOrgCases(t, []orgCase{
		{name: "add", viewer: olivia, document: onProvider(false, doc("b-model", priced, ", expectedVersion: 0")),
			want: []string{`"setModel":{"name":"b-model","inputPrice":"0.80","outputPrice":"4.00","cap":"0.00015","default":true,"version":1}`}},
		{name: "add one added", viewer: olivia, document: onProvider(false, doc("a-model", priced, ", expectedVersion: 0")),
			wantCode: "CONFLICT", wantReason: "precondition.failed"},
		{name: "change", viewer: olivia, document: onProvider(false, doc("a-model", `inputPrice: "1", outputPrice: "2"`, ", expectedVersion: 1")),
			want: []string{`"setModel":{"name":"a-model","inputPrice":"1.00","outputPrice":"2.00","cap":null,"default":false,"version":2}`}},
		{name: "a provider not added", viewer: olivia, document: doc("a-model", priced, ", expectedVersion: 0"),
			wantCode: "BAD_USER_INPUT", wantReason: "model.provider_invalid"},
		{name: "a price that isn't an amount", viewer: olivia, document: onProvider(false, doc("b-model", `inputPrice: "$$", outputPrice: "4"`, "")),
			wantCode: "BAD_USER_INPUT", wantReason: "model.price_invalid"},
		{name: "a cap that isn't an amount", viewer: olivia, document: onProvider(false, doc("b-model", `inputPrice: "1", outputPrice: "4", cap: "x"`, "")),
			wantCode: "BAD_USER_INPUT", wantReason: "model.cap_invalid"},
		{name: "add as a Member", viewer: maxim, document: onProvider(false, doc("b-model", priced, ", expectedVersion: 0")), wantCode: "FORBIDDEN"},
		{name: "remove", viewer: olivia, document: onProvider(false, withScout(`mutation { removeModel(organizationId: "ACME", provider: "anthropic", model: "a-model", expectedVersion: 1) }`)),
			want: []string{`"removeModel":true`}},
		{name: "remove one an agent runs on", viewer: olivia, document: onProvider(true, withScout(`mutation { removeModel(organizationId: "ACME", provider: "anthropic", model: "a-model") }`)),
			wantCode: "CONFLICT", wantReason: "model.in_use"},
		{name: "remove one not added", viewer: olivia, document: withScout(`mutation { removeModel(organizationId: "ACME", provider: "anthropic", model: "a-model") }`),
			wantCode: "NOT_FOUND", wantReason: "model.not_found"},
	})
}

func TestAgentCapMutations(t *testing.T) {
	update := func(input string) func(*orgFixture) string {
		return onProvider(true, withScout(`mutation { updateAgent(organizationId: "ACME", id: "SCOUT", input: {`+input+`}) { cap modelCap effectiveCap } }`))
	}
	runOrgCases(t, []orgCase{
		{name: "set a lower one", viewer: olivia, document: update(`cap: "2"`), want: []string{`"updateAgent":{"cap":"2.00","modelCap":"8.00","effectiveCap":"2.00"}`}},
		{name: "clear it", viewer: olivia, document: update(`cap: null`), want: []string{`"updateAgent":{"cap":null,"modelCap":"8.00","effectiveCap":"8.00"}`}},
		{name: "leave it", viewer: olivia, document: update(`name: "Scout"`), want: []string{`"updateAgent":{"cap":null,`}},
		{name: "one that isn't an amount", viewer: olivia, document: update(`cap: "two"`), wantCode: "BAD_USER_INPUT", wantReason: "agent.cap_invalid"},
	})
}

// spending is working on Acme's task number, running on a-model.
func spending(number int64) func(f *orgFixture) *auth.Authenticated {
	return func(f *orgFixture) *auth.Authenticated {
		f.agentStore.SetState(f.acme.ID, number, agents.TaskRunning)
		return working(number)(f)
	}
}

func TestRecordTaskSpend(t *testing.T) {
	record := func(id string, input, output string) func(*orgFixture) string {
		return onProvider(true, func(*orgFixture) string {
			return `mutation { recordTaskSpend(input: {id: "` + id + `", model: "a-model", inputTokens: ` + input + `, outputTokens: ` + output + `}) { spent inputTokens outputTokens cap ended } }`
		})
	}
	runOrgCases(t, []orgCase{
		{name: "a call", viewer: spending(1), document: record(uuid.NewString(), "100000", "10000"),
			want: []string{`"recordTaskSpend":{"spent":"0.45","inputTokens":100000,"outputTokens":10000,"cap":"8.00","ended":false}`}},
		{name: "the call that reaches the cap", viewer: spending(1), document: record(uuid.NewString(), "0", "600000"),
			want: []string{`"spent":"9.00"`, `"ended":true`}},
		{name: "an ID that isn't one", viewer: spending(1), document: record("x", "1", "1"), wantCode: "BAD_USER_INPUT", wantReason: "spend.id_invalid"},
		{name: "a task that ended", viewer: working(3), document: record(uuid.NewString(), "1", "1"), wantCode: "CONFLICT", wantReason: "task.ended"},
		{name: "a person", viewer: maxim, document: record(uuid.NewString(), "1", "1"), wantCode: "FORBIDDEN"},
	})
}
