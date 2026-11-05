package rest

import (
	"net/http"
	"strconv"
	"testing"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/agents"
	"cobranch.dev/internal/services/auth"
)

// wantModel checks that the answer is anthropic's a-model at version, as withProvider added it, with the agents named.
func wantModel(version int64, on ...string) func(*testing.T, *orgFixture, []byte) {
	return func(t *testing.T, f *orgFixture, body []byte) {
		t.Helper()
		m := decode[Model](t, body)
		if m.Provider != "anthropic" || m.Name != "a-model" || m.InputPrice != "3.00" || m.OutputPrice != "15.00" ||
			m.Version != version || len(m.Agents) != len(on) || m.CreatedBy == nil || m.CreatedBy.Id != f.olivia.User.ID {
			t.Errorf("model = %s", body)
		}
		for i, name := range on {
			if m.Agents[i].DisplayName != name {
				t.Errorf("model = %s, want %v on it", body, on)
			}
		}
	}
}

func TestModels(t *testing.T) {
	const models = "/v1/orgs/acme/models"
	aModel := fixed(models + "/anthropic/a-model")
	priced := `{"inputPrice":"3.00","outputPrice":"15"}`
	runOrgCases(t, []orgCase{
		{name: "list", viewer: maxim, method: "GET", path: onModel(t, fixed(models)), wantStatus: 200,
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				if got := decode[ModelList](t, body); len(got.Models) != 1 || got.Models[0].Name != "a-model" || len(got.Models[0].Agents) != 1 {
					t.Errorf("models = %s", body)
				}
			}},
		{name: "list none", viewer: maxim, method: "GET", path: fixed(models), wantStatus: 200,
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				if got := decode[ModelList](t, body); got.Models == nil || len(got.Models) != 0 {
					t.Errorf("models = %s", body)
				}
			}},
		{name: "list as an outsider", viewer: nia, method: "GET", path: fixed(models), wantStatus: 404, wantCode: "organization.not_found"},
		{name: "get", viewer: maxim, method: "GET", path: onModel(t, aModel), wantStatus: 200, wantTag: `"1-`, check: wantModel(1, "Scout")},
		{name: "get what the caller holds", viewer: maxim, method: "GET", path: aModel, wantStatus: 304,
			headerFor: func(f *orgFixture) http.Header { f.withProvider(t); return held(maxim, aModel)(f) }},
		{name: "get one not added", viewer: maxim, method: "GET", path: fixed(models + "/anthropic/b-model"), wantStatus: 404, wantCode: "model.not_found"},
		{name: "add", viewer: olivia, method: "PUT", path: onProvider(t, fixed(models+"/anthropic/b-model")), header: http.Header{"If-None-Match": {"*"}},
			body: `{"inputPrice":"0.80","outputPrice":"4.000150","default":true}`, wantStatus: 201, wantTag: `"1-`,
			wantHeader: http.Header{"Location": {models + "/anthropic/b-model"}},
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				if m := decode[Model](t, body); m.Name != "b-model" || m.InputPrice != "0.80" || m.OutputPrice != "4.00015" ||
					!m.Default || m.Version != 1 {
					t.Errorf("model = %s", body)
				}
			}},
		{name: "add one added", viewer: olivia, method: "PUT", path: onProvider(t, aModel), header: http.Header{"If-None-Match": {"*"}}, body: priced,
			wantStatus: 412, wantCode: "precondition.failed"},
		{name: "add without a precondition", viewer: olivia, method: "PUT", path: aModel, body: priced, wantStatus: 428, wantCode: "precondition.required"},
		{name: "add of a provider not added", viewer: olivia, method: "PUT", path: fixed(models + "/openai/a-model"), header: http.Header{"If-None-Match": {"*"}},
			body: priced, wantStatus: 400, wantCode: "model.provider_invalid"},
		{name: "add with a price that isn't an amount", viewer: olivia, method: "PUT", path: onProvider(t, fixed(models+"/anthropic/b-model")),
			header: http.Header{"If-None-Match": {"*"}}, body: `{"inputPrice":"3,00","outputPrice":"15"}`, wantStatus: 400, wantCode: "model.price_invalid"},
		{name: "add with a price past the most", viewer: olivia, method: "PUT", path: onProvider(t, fixed(models+"/anthropic/b-model")),
			header: http.Header{"If-None-Match": {"*"}}, body: `{"inputPrice":"3","outputPrice":"10000.01"}`, wantStatus: 400, wantCode: "model.price_invalid"},
		{name: "add as a Member", viewer: maxim, method: "PUT", path: onProvider(t, fixed(models+"/anthropic/b-model")), header: http.Header{"If-None-Match": {"*"}},
			body: priced, wantStatus: 403, wantCode: "auth.forbidden"},
		{name: "change at its version", viewer: olivia, method: "PUT", path: onProvider(t, aModel), header: matchHeader(`"1"`), body: priced,
			wantStatus: 200, wantTag: `"2-`, check: wantModel(2)},
		{name: "change from a stale version", viewer: olivia, method: "PUT", path: onProvider(t, aModel), header: matchHeader(`"2"`), body: priced,
			wantStatus: 412, wantCode: "precondition.failed"},
		{name: "remove", viewer: olivia, method: "DELETE", path: onProvider(t, aModel), header: matchHeader(`"1"`), wantStatus: 204},
		{name: "remove one an agent runs on", viewer: olivia, method: "DELETE", path: onModel(t, aModel), header: matchHeader("*"), wantStatus: 409,
			wantCode: "model.in_use"},
		{name: "remove without If-Match", viewer: olivia, method: "DELETE", path: onProvider(t, aModel), wantStatus: 428, wantCode: "precondition.required"},
		{name: "remove as a Member", viewer: maxim, method: "DELETE", path: onProvider(t, aModel), header: matchHeader("*"), wantStatus: 403, wantCode: "auth.forbidden"},
	})
}

// wantCap checks that the answer is an agent with the cap, nil for none.
func wantCap(want *string) func(*testing.T, *orgFixture, []byte) {
	return func(t *testing.T, _ *orgFixture, body []byte) {
		t.Helper()
		if a := decode[Agent](t, body); (a.Cap == nil) != (want == nil) || (a.Cap != nil && *a.Cap != *want) {
			t.Errorf("agent = %s", body)
		}
	}
}

func TestAgentCap(t *testing.T) {
	patch := http.Header{"If-Match": {"*"}, "Content-Type": {"application/merge-patch+json"}}
	two, eight := new("2.00"), new("8.00")
	runOrgCases(t, []orgCase{
		{name: "get none", viewer: maxim, method: "GET", path: scoutPath, wantStatus: 200, check: wantCap(nil)},
		{name: "get one", viewer: maxim, method: "GET", path: onModel(t, scoutPath), wantStatus: 200, check: wantCap(eight)},
		{name: "change it", viewer: olivia, method: "PATCH", path: onModel(t, scoutPath), header: patch, body: `{"cap":"2"}`, wantStatus: 200,
			check: wantCap(two)},
		{name: "set one without a model", viewer: olivia, method: "PATCH", path: scoutPath, header: patch, body: `{"cap":"2"}`, wantStatus: 200,
			check: wantCap(two)},
		{name: "leave it", viewer: olivia, method: "PATCH", path: onModel(t, scoutPath), header: patch, body: `{"name":"Scout"}`, wantStatus: 200,
			check: wantCap(eight)},
		{name: "clear it", viewer: olivia, method: "PATCH", path: onModel(t, scoutPath), header: patch, body: `{"cap":null}`, wantStatus: 200,
			check: wantCap(nil)},
		{name: "one that isn't an amount", viewer: olivia, method: "PATCH", path: scoutPath, header: patch, body: `{"cap":"two"}`, wantStatus: 400,
			wantCode: "agent.cap_invalid"},
		{name: "one past the most", viewer: olivia, method: "PATCH", path: scoutPath, header: patch, body: `{"cap":"1000000.000001"}`, wantStatus: 400,
			wantCode: "agent.cap_invalid"},
	})
}

// spending is a viewer under a task token of Scout's, on a-model with a cap of $8 a task, for Acme's running task
// number.
func spending(t *testing.T, number int64) func(f *orgFixture) *auth.Authenticated {
	return func(f *orgFixture) *auth.Authenticated {
		f.withProvider(t)
		f.withScoutOnModel(t)
		f.agentStore.SetState(f.acme.ID, number, agents.TaskRunning)
		return working(number)(f)
	}
}

func TestRecordMyTaskSpend(t *testing.T) {
	const path = "/v1/me/task/spend"
	call := func(input, output int) string {
		return `{"id":"` + uuid.NewString() + `","model":"a-model","inputTokens":` + strconv.Itoa(input) + `,"outputTokens":` + strconv.Itoa(output) + `}`
	}
	runOrgCases(t, []orgCase{
		{name: "a call", viewer: spending(t, 1), method: "POST", path: fixed(path), body: call(100_000, 10_000), wantStatus: 200,
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				s := decode[TaskSpend](t, body)
				if s.Spent != "0.45" || s.InputTokens != 100_000 || s.OutputTokens != 10_000 || s.Cap == nil || *s.Cap != "8.00" || s.Ended {
					t.Errorf("spend = %s", body)
				}
			}},
		{name: "the call that reaches the cap", viewer: spending(t, 1), method: "POST", path: fixed(path), body: call(0, 600_000), wantStatus: 200,
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				if s := decode[TaskSpend](t, body); s.Spent != "9.00" || !s.Ended {
					t.Errorf("spend = %s", body)
				}
			}},
		{name: "without a model", viewer: spending(t, 1), method: "POST", path: fixed(path), body: `{"id":"` + uuid.NewString() + `","model":"","inputTokens":1,"outputTokens":0}`,
			wantStatus: 400, wantCode: "spend.model_invalid"},
		{name: "a task that ended", viewer: working(3), method: "POST", path: fixed(path), body: call(1, 1), wantStatus: 409, wantCode: "task.ended"},
		{name: "a person", viewer: maxim, method: "POST", path: fixed(path), body: call(1, 1), wantStatus: 403, wantCode: "auth.forbidden"},
	})
}
