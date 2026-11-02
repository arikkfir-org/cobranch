package graphql

import (
	"context"
	"strings"
	"testing"

	"cobranch.dev/internal/services/agents"
)

// withKeys is a document that names, besides what withScout replaces, providerKey where it says @KEY@ and deployKey
// where it says @DEPLOY@.
func withKeys(document string) func(*orgFixture) string {
	return func(f *orgFixture) string {
		return withScout(strings.NewReplacer("@KEY@", f.providerKey.String(), "@DEPLOY@", f.deployKey.String()).Replace(document))(f)
	}
}

// onProvider is a document with anthropic added to Acme first, its key in providerKey, and Scout on one of its models
// when onModel.
func onProvider(onModel bool, document func(*orgFixture) string) func(*orgFixture) string {
	return func(f *orgFixture) string {
		ctx := context.Background()
		if _, _, err := f.agents.SetModelProvider(ctx, f.olivia.User, f.acme.ID, "anthropic", f.providerKey, agents.NoVersion); err != nil {
			panic(err)
		}
		if onModel {
			if _, err := f.agents.SetModel(ctx, f.olivia.User, f.acme.ID, f.scout.ID, "anthropic", "a-model", agents.AnyVersion); err != nil {
				panic(err)
			}
		}
		return document(f)
	}
}

func TestModelProviderQueries(t *testing.T) {
	providers := ofAcme(`availableModelProviders modelProviders { name secretName broken createdBy { displayName } version }`)
	runOrgCases(t, []orgCase{
		{name: "as an Owner", viewer: olivia, document: onProvider(false, providers),
			want: []string{`"availableModelProviders":["anthropic","openai"]`,
				`"modelProviders":[{"name":"anthropic","secretName":"anthropic-key","broken":false,"createdBy":{"displayName":"Olivia"},"version":1}]`}},
		{name: "as a Member", viewer: maxim, document: onProvider(false, providers), want: []string{`"secretName":null`}},
		{name: "none", viewer: maxim, document: providers, want: []string{`"modelProviders":[]`}},
		{name: "an agent's model and secrets", viewer: maxim, document: onProvider(true, withScout(`{ organization(slug: "acme") { agent(id: "SCOUT") { provider model secrets { name } } } }`)),
			want: []string{`"agent":{"provider":"anthropic","model":"a-model","secrets":[]}`}},
	})
}

func TestModelProviderMutations(t *testing.T) {
	set := `mutation { setModelProvider(organizationId: "ACME", provider: "anthropic", secretId: "@KEY@"%s) { name secretName version } }`
	runOrgCases(t, []orgCase{
		{name: "add", viewer: olivia, document: withKeys(strings.Replace(set, "%s", ", expectedVersion: 0", 1)),
			want: []string{`"setModelProvider":{"name":"anthropic","secretName":"anthropic-key","version":1}`}},
		{name: "add one added", viewer: olivia, document: onProvider(false, withKeys(strings.Replace(set, "%s", ", expectedVersion: 0", 1))),
			wantCode: "CONFLICT", wantReason: "precondition.failed"},
		{name: "change", viewer: olivia, document: onProvider(false, withKeys(strings.Replace(set, "%s", ", expectedVersion: 1", 1))),
			want: []string{`"version":2`}},
		{name: "change at any version", viewer: olivia, document: onProvider(false, withKeys(strings.Replace(set, "%s", "", 1))), want: []string{`"version":2`}},
		{name: "change one not added", viewer: olivia, document: withKeys(strings.Replace(set, "%s", "", 1)), wantCode: "CONFLICT", wantReason: "precondition.failed"},
		{name: "add one the installation doesn't know", viewer: olivia,
			document: withKeys(`mutation { setModelProvider(organizationId: "ACME", provider: "acme-ai", secretId: "@KEY@", expectedVersion: 0) { name } }`),
			wantCode: "NOT_FOUND", wantReason: "model_provider.not_found"},
		{name: "add with a repository's secret", viewer: olivia,
			document: withKeys(`mutation { setModelProvider(organizationId: "ACME", provider: "anthropic", secretId: "@DEPLOY@", expectedVersion: 0) { name } }`),
			wantCode: "BAD_USER_INPUT", wantReason: "model_provider.secret_invalid"},
		{name: "add with a secret of no ID", viewer: olivia,
			document: withKeys(`mutation { setModelProvider(organizationId: "ACME", provider: "anthropic", secretId: "x", expectedVersion: 0) { name } }`),
			wantCode: "BAD_USER_INPUT", wantReason: "model_provider.secret_invalid"},
		{name: "add as a Member", viewer: maxim, document: withKeys(strings.Replace(set, "%s", ", expectedVersion: 0", 1)), wantCode: "FORBIDDEN"},
		{name: "remove", viewer: olivia, document: onProvider(false, withScout(`mutation { removeModelProvider(organizationId: "ACME", provider: "anthropic", expectedVersion: 1) }`)),
			want: []string{`"removeModelProvider":true`}},
		{name: "remove one an agent runs on", viewer: olivia, document: onProvider(true, withScout(`mutation { removeModelProvider(organizationId: "ACME", provider: "anthropic") }`)),
			wantCode: "CONFLICT", wantReason: "model_provider.in_use"},
		{name: "remove one not added", viewer: olivia, document: withScout(`mutation { removeModelProvider(organizationId: "ACME", provider: "anthropic") }`),
			wantCode: "NOT_FOUND", wantReason: "model_provider.not_found"},
	})
}

func TestAgentModelMutations(t *testing.T) {
	runOrgCases(t, []orgCase{
		{name: "pick", viewer: olivia,
			document: onProvider(false, withScout(`mutation { setAgentModel(organizationId: "ACME", id: "SCOUT", provider: "anthropic", model: "a-model", expectedVersion: 1) { provider model version } }`)),
			want:     []string{`"setAgentModel":{"provider":"anthropic","model":"a-model","version":2}`}},
		{name: "pick none", viewer: olivia, document: onProvider(true, withScout(`mutation { setAgentModel(organizationId: "ACME", id: "SCOUT") { provider model } }`)),
			want: []string{`"setAgentModel":{"provider":null,"model":null}`}},
		{name: "pick a provider not added", viewer: olivia, document: withScout(`mutation { setAgentModel(organizationId: "ACME", id: "SCOUT", provider: "openai", model: "a-model") { model } }`),
			wantCode: "BAD_USER_INPUT", wantReason: "agent.provider_invalid"},
		{name: "pick as a Member", viewer: maxim, document: onProvider(false, withScout(`mutation { setAgentModel(organizationId: "ACME", id: "SCOUT", provider: "anthropic", model: "a-model") { model } }`)),
			wantCode: "FORBIDDEN"},
		{name: "pick for an agent not there", viewer: olivia, document: withScout(`mutation { setAgentModel(organizationId: "ACME", id: "x") { model } }`),
			wantCode: "NOT_FOUND", wantReason: "agent.not_found"},
		{name: "name secrets", viewer: olivia,
			document: withKeys(`mutation { setAgentSecrets(organizationId: "ACME", id: "SCOUT", secrets: [{name: "NPM_TOKEN", secretId: "@KEY@"}, {name: "DEPLOY_KEY", secretId: "@DEPLOY@"}], expectedVersion: 1) { secrets { name secretId } version } }`),
			check: func(t *testing.T, f *orgFixture, body string) {
				want := `"secrets":[{"name":"DEPLOY_KEY","secretId":"` + f.deployKey.String() + `"},{"name":"NPM_TOKEN","secretId":"` + f.providerKey.String() + `"}],"version":2`
				if !strings.Contains(body, want) {
					t.Errorf("body = %s, want %s", body, want)
				}
			}},
		{name: "name a variable twice", viewer: olivia,
			document: withKeys(`mutation { setAgentSecrets(organizationId: "ACME", id: "SCOUT", secrets: [{name: "TOKEN", secretId: "@KEY@"}, {name: "TOKEN", secretId: "@DEPLOY@"}]) { version } }`),
			wantCode: "BAD_USER_INPUT", wantReason: "agent.secrets_invalid"},
		{name: "name a variable the harness sets", viewer: olivia,
			document: withKeys(`mutation { setAgentSecrets(organizationId: "ACME", id: "SCOUT", secrets: [{name: "HOME", secretId: "@KEY@"}]) { version } }`),
			wantCode: "BAD_USER_INPUT", wantReason: "agent.secrets_invalid"},
		{name: "name secrets from a stale version", viewer: olivia,
			document: withKeys(`mutation { setAgentSecrets(organizationId: "ACME", id: "SCOUT", secrets: [], expectedVersion: 2) { version } }`),
			wantCode: "CONFLICT", wantReason: "precondition.failed"},
		{name: "name secrets as a Member", viewer: maxim, document: withKeys(`mutation { setAgentSecrets(organizationId: "ACME", id: "SCOUT", secrets: []) { version } }`),
			wantCode: "FORBIDDEN"},
	})
}
