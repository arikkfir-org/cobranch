package rest

import (
	"net/http"
	"slices"
	"testing"

	"cobranch.dev/internal/services/agents"
)

// withProvider has Olivia add anthropic to Acme, its key in providerKey, and its a-model to Acme's models, at $3 and $15
// a million tokens and a cap of $8 a task.
func (f *orgFixture) withProvider(t *testing.T) {
	t.Helper()
	if _, _, err := f.agents.SetModelProvider(t.Context(), f.olivia.User, f.acme.ID, "anthropic", f.providerKey, agents.NoVersion); err != nil {
		t.Fatal(err)
	}
	in := agents.ModelInput{InputPrice: 3_000_000, OutputPrice: 15_000_000, Cap: 8_000_000}
	if _, _, err := f.agents.PutModel(t.Context(), f.olivia.User, f.acme.ID, "anthropic", "a-model", in, agents.NoVersion); err != nil {
		t.Fatal(err)
	}
}

// onProvider is a path with anthropic added first.
func onProvider(t *testing.T, path func(*orgFixture) string) func(*orgFixture) string {
	return func(f *orgFixture) string {
		f.withProvider(t)
		return path(f)
	}
}

// onModel is a path with anthropic added first, and Scout on one of its models.
func onModel(t *testing.T, path func(*orgFixture) string) func(*orgFixture) string {
	return func(f *orgFixture) string {
		f.withProvider(t)
		if _, err := f.agents.SetModel(t.Context(), f.olivia.User, f.acme.ID, f.scout.ID, "anthropic", "a-model", agents.AnyVersion); err != nil {
			t.Fatal(err)
		}
		return path(f)
	}
}

// wantProvider checks that the answer is anthropic at version, with providerKey's name shown or not.
func wantProvider(version int64, named bool) func(*testing.T, *orgFixture, []byte) {
	return func(t *testing.T, f *orgFixture, body []byte) {
		p := decode[ModelProvider](t, body)
		if p.Name != "anthropic" || p.Version != version || p.Broken || p.SecretId == nil || *p.SecretId != f.providerKey ||
			(p.SecretName != nil) != named || p.CreatedBy == nil || p.CreatedBy.Id != f.olivia.User.ID {
			t.Errorf("provider = %s", body)
		}
	}
}

func TestModelProviders(t *testing.T) {
	const providers = "/v1/orgs/acme/model-providers"
	anthropic := fixed(providers + "/anthropic")
	key := func(f *orgFixture) string { return `{"secretId":"` + f.providerKey.String() + `"}` }
	runOrgCases(t, []orgCase{
		{name: "list", viewer: maxim, method: "GET", path: onProvider(t, fixed(providers)), wantStatus: 200,
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				got := decode[ModelProviderList](t, body)
				if !slices.Equal(got.Available, []string{"anthropic", "openai"}) || len(got.ModelProviders) != 1 || got.ModelProviders[0].SecretName != nil {
					t.Errorf("providers = %s", body)
				}
			}},
		{name: "list none", viewer: maxim, method: "GET", path: fixed(providers), wantStatus: 200,
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				if got := decode[ModelProviderList](t, body); got.ModelProviders == nil || len(got.ModelProviders) != 0 {
					t.Errorf("providers = %s", body)
				}
			}},
		{name: "list as an outsider", viewer: nia, method: "GET", path: fixed(providers), wantStatus: 404, wantCode: "organization.not_found"},
		{name: "get as an Owner", viewer: olivia, method: "GET", path: onProvider(t, anthropic), wantStatus: 200, wantTag: `"1-`, check: wantProvider(1, true)},
		{name: "get as a Member", viewer: maxim, method: "GET", path: onProvider(t, anthropic), wantStatus: 200, check: wantProvider(1, false)},
		{name: "get what the caller holds", viewer: maxim, method: "GET", path: anthropic, wantStatus: 304,
			headerFor: func(f *orgFixture) http.Header { f.withProvider(t); return held(maxim, anthropic)(f) }},
		{name: "get one not added", viewer: maxim, method: "GET", path: fixed(providers + "/openai"), wantStatus: 404, wantCode: "model_provider.not_found"},
		{name: "add", viewer: olivia, method: "PUT", path: anthropic, header: http.Header{"If-None-Match": {"*"}}, bodyOf: key, wantStatus: 201,
			wantTag: `"1-`, wantHeader: http.Header{"Location": {providers + "/anthropic"}}, check: wantProvider(1, true)},
		{name: "add one added", viewer: olivia, method: "PUT", path: onProvider(t, anthropic), header: http.Header{"If-None-Match": {"*"}}, bodyOf: key,
			wantStatus: 412, wantCode: "precondition.failed"},
		{name: "add without a precondition", viewer: olivia, method: "PUT", path: anthropic, bodyOf: key, wantStatus: 428, wantCode: "precondition.required"},
		{name: "add under both preconditions", viewer: olivia, method: "PUT", path: anthropic, header: http.Header{"If-None-Match": {"*"}, "If-Match": {"*"}},
			bodyOf: key, wantStatus: 400, wantCode: "precondition.invalid"},
		{name: "add as a Member", viewer: maxim, method: "PUT", path: anthropic, header: http.Header{"If-None-Match": {"*"}}, bodyOf: key,
			wantStatus: 403, wantCode: "auth.forbidden"},
		{name: "add one the installation doesn't know", viewer: olivia, method: "PUT", path: fixed(providers + "/acme-ai"), header: http.Header{"If-None-Match": {"*"}},
			bodyOf: key, wantStatus: 404, wantCode: "model_provider.not_found"},
		{name: "add with a repository's secret", viewer: olivia, method: "PUT", path: anthropic, header: http.Header{"If-None-Match": {"*"}},
			bodyOf: func(f *orgFixture) string { return `{"secretId":"` + f.deployKey.String() + `"}` }, wantStatus: 400, wantCode: "model_provider.secret_invalid"},
		{name: "change at its version", viewer: olivia, method: "PUT", path: onProvider(t, anthropic), header: matchHeader(`"1"`), bodyOf: key, wantStatus: 200,
			wantTag: `"2-`, check: wantProvider(2, true)},
		{name: "change from a stale version", viewer: olivia, method: "PUT", path: onProvider(t, anthropic), header: matchHeader(`"2"`), bodyOf: key,
			wantStatus: 412, wantCode: "precondition.failed"},
		{name: "remove", viewer: olivia, method: "DELETE", path: onProvider(t, anthropic), header: matchHeader(`"1"`), wantStatus: 204},
		{name: "remove without If-Match", viewer: olivia, method: "DELETE", path: onProvider(t, anthropic), wantStatus: 428, wantCode: "precondition.required"},
		{name: "remove one an agent runs on", viewer: olivia, method: "DELETE", path: onModel(t, anthropic), header: matchHeader("*"), wantStatus: 409,
			wantCode: "model_provider.in_use"},
		{name: "remove as a Member", viewer: maxim, method: "DELETE", path: onProvider(t, anthropic), header: matchHeader("*"), wantStatus: 403, wantCode: "auth.forbidden"},
	})
}

func TestAgentModel(t *testing.T) {
	model := func(f *orgFixture) string { return scoutPath(f) + "/model" }
	runOrgCases(t, []orgCase{
		{name: "pick", viewer: olivia, method: "PUT", path: onProvider(t, model), header: matchHeader(`"1"`), body: `{"provider":"anthropic","model":"a-model"}`,
			wantStatus: 200, wantTag: `"2-`,
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				if a := decode[Agent](t, body); a.Provider == nil || *a.Provider != "anthropic" || a.Model == nil || *a.Model != "a-model" || a.Version != 2 {
					t.Errorf("agent = %s", body)
				}
			}},
		{name: "pick none", viewer: olivia, method: "PUT", path: onModel(t, model), header: matchHeader("*"), body: `{}`, wantStatus: 200,
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				if a := decode[Agent](t, body); a.Provider != nil || a.Model != nil {
					t.Errorf("agent = %s", body)
				}
			}},
		{name: "pick a provider not added", viewer: olivia, method: "PUT", path: model, header: matchHeader("*"), body: `{"provider":"openai","model":"a-model"}`,
			wantStatus: 400, wantCode: "agent.provider_invalid"},
		{name: "pick a model without a provider", viewer: olivia, method: "PUT", path: model, header: matchHeader("*"), body: `{"model":"a-model"}`,
			wantStatus: 400, wantCode: "agent.model_invalid"},
		{name: "pick without If-Match", viewer: olivia, method: "PUT", path: onProvider(t, model), body: `{"provider":"anthropic","model":"a-model"}`,
			wantStatus: 428, wantCode: "precondition.required"},
		{name: "pick from a stale version", viewer: olivia, method: "PUT", path: onProvider(t, model), header: matchHeader(`"2"`),
			body: `{"provider":"anthropic","model":"a-model"}`, wantStatus: 412, wantCode: "precondition.failed"},
		{name: "pick as a Member", viewer: maxim, method: "PUT", path: onProvider(t, model), header: matchHeader("*"), body: `{"provider":"anthropic","model":"a-model"}`,
			wantStatus: 403, wantCode: "auth.forbidden"},
	})
}

func TestAgentSecrets(t *testing.T) {
	secretsOf := func(f *orgFixture) string { return scoutPath(f) + "/secrets" }
	runOrgCases(t, []orgCase{
		{name: "name", viewer: olivia, method: "PUT", path: secretsOf, header: matchHeader(`"1"`), wantStatus: 200, wantTag: `"2-`,
			bodyOf: func(f *orgFixture) string {
				return `{"secrets":{"NPM_TOKEN":"` + f.providerKey.String() + `","DEPLOY_KEY":"` + f.deployKey.String() + `"}}`
			},
			check: func(t *testing.T, f *orgFixture, body []byte) {
				if a := decode[Agent](t, body); len(a.Secrets) != 2 || a.Secrets["NPM_TOKEN"] != f.providerKey || a.Secrets["DEPLOY_KEY"] != f.deployKey {
					t.Errorf("agent = %s", body)
				}
			}},
		{name: "name none", viewer: olivia, method: "PUT", path: secretsOf, header: matchHeader("*"), body: `{"secrets":{}}`, wantStatus: 200,
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				if a := decode[Agent](t, body); a.Secrets == nil || len(a.Secrets) != 0 {
					t.Errorf("agent = %s", body)
				}
			}},
		{name: "name a variable the harness sets", viewer: olivia, method: "PUT", path: secretsOf, header: matchHeader("*"), wantStatus: 400, wantCode: "agent.secrets_invalid",
			bodyOf: func(f *orgFixture) string { return `{"secrets":{"PATH":"` + f.providerKey.String() + `"}}` }},
		{name: "name a secret not there", viewer: olivia, method: "PUT", path: secretsOf, header: matchHeader("*"), wantStatus: 400, wantCode: "agent.secrets_invalid",
			body: `{"secrets":{"TOKEN":"00000000-0000-4000-8000-000000000000"}}`},
		{name: "name without If-Match", viewer: olivia, method: "PUT", path: secretsOf, body: `{"secrets":{}}`, wantStatus: 428, wantCode: "precondition.required"},
		{name: "name as a Member", viewer: maxim, method: "PUT", path: secretsOf, header: matchHeader("*"), body: `{"secrets":{}}`, wantStatus: 403, wantCode: "auth.forbidden"},
	})
}
