package rest

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/secrets"
	"cobranch.dev/internal/services/secrets/secretstest"
)

// buildJob is the principal of build, a job of website's, and scoutAgent of Scout, an agent of Acme's.
var (
	buildJob   = uuid.MustParse("00000000-0000-7000-8000-00000000b001")
	scoutAgent = uuid.MustParse("00000000-0000-7000-8000-00000000a001")
)

// withSecrets adds Acme's secret npm and website's deploy, sealed as authtest.Sealer seals them, buildJob and
// scoutAgent, to the fixture.
func (f *orgFixture) withSecrets() (npm, deploy secrets.Secret) {
	npm = secrets.Secret{ID: uuid.New(), OrgID: f.acme.ID, Name: "npm", Version: 1, CreatedBy: f.olivia.User.ID, UpdatedBy: f.olivia.User.ID, CreatedAt: now, UpdatedAt: now}
	deploy = secrets.Secret{ID: uuid.New(), OrgID: f.acme.ID, RepoID: f.website.ID, Name: "deploy", Version: 2, CreatedAt: now, UpdatedAt: now}
	for s, value := range map[secrets.Secret]string{npm: "npm-token", deploy: "deploy-key"} {
		f.secretStore.Add(s, []byte("sealed:"+secrets.About(s.ID)+":"+value))
	}
	f.secretStore.Others[buildJob] = secretstest.Other{OrgID: f.acme.ID, RepoID: f.website.ID, Principal: secrets.Principal{ID: buildJob, Kind: auth.KindJob, DisplayName: "build"}}
	f.secretStore.Others[scoutAgent] = secretstest.Other{OrgID: f.acme.ID, Principal: secrets.Principal{ID: scoutAgent, Kind: auth.KindAgent, DisplayName: "Scout"}}
	return npm, deploy
}

// secretNamed is the fixture's secret named name.
func (f *orgFixture) secretNamed(name string) secrets.Secret {
	for _, s := range f.secretStore.Secrets {
		if s.Name == name {
			return s
		}
	}
	return secrets.Secret{}
}

// npmPath is the URL of the fixture's secret npm, with suffix after it.
func npmPath(suffix string) func(f *orgFixture) string {
	return func(f *orgFixture) string {
		npm, _ := f.withSecrets()
		return "/v1/orgs/acme/secrets/" + npm.ID.String() + suffix
	}
}

// ifMatchHeader is a header naming tag in If-Match.
func ifMatchHeader(tag string) http.Header {
	return http.Header{"If-Match": []string{tag}}
}

func TestSecretsAPI(t *testing.T) {
	runOrgCases(t, []orgCase{
		{name: "add one to the organization", viewer: olivia, method: "POST", path: fixed("/v1/orgs/acme/secrets"), body: `{"name":"npm","value":"npm-token"}`, wantStatus: 201,
			wantHeader: http.Header{"Etag": []string{`"1"`}},
			check: func(t *testing.T, f *orgFixture, body []byte) {
				got := decode[Secret](t, body)
				sealed := string(f.secretStore.Values[got.Id])
				if got.Name != "npm" || got.OrgId != f.acme.ID || got.RepoId != nil || got.Version != 1 || strings.Contains(string(body), "npm-token") ||
					sealed != "sealed:"+secrets.About(got.Id)+":npm-token" {
					t.Errorf("created %s, sealed as %q", body, sealed)
				}
			}},
		{name: "add one as a Member", viewer: maxim, method: "POST", path: fixed("/v1/orgs/acme/secrets"), body: `{"name":"npm","value":"x"}`, wantStatus: 403, wantCode: "auth.forbidden"},
		{name: "add one from outside", viewer: nia, method: "POST", path: fixed("/v1/orgs/acme/secrets"), body: `{"name":"npm","value":"x"}`, wantStatus: 404, wantCode: "organization.not_found"},
		{name: "add one under a taken name", viewer: olivia, method: "POST", path: func(f *orgFixture) string { f.withSecrets(); return "/v1/orgs/acme/secrets" },
			body: `{"name":"npm","value":"x"}`, wantStatus: 409, wantCode: "secret.name_taken"},
		{name: "add one without a value", viewer: olivia, method: "POST", path: fixed("/v1/orgs/acme/secrets"), body: `{"name":"npm","value":""}`, wantStatus: 400, wantCode: "secret.value_invalid"},
		{name: "add one without a secrets key", viewer: olivia, method: "POST", body: `{"name":"npm","value":"x"}`, wantStatus: 409, wantCode: "secrets_key.missing",
			path: func(f *orgFixture) string {
				f.secrets = secrets.NewService(f.secretStore, auth.NewAuthorizer(f.store), nil)
				return "/v1/orgs/acme/secrets"
			}},
		{name: "add one to a repository", viewer: olivia, method: "POST", path: fixed("/v1/orgs/acme/repos/website/secrets"), body: `{"name":"deploy","value":"k"}`, wantStatus: 201,
			check: func(t *testing.T, f *orgFixture, body []byte) {
				if got := decode[Secret](t, body); got.RepoId == nil || *got.RepoId != f.website.ID {
					t.Errorf("created %s", body)
				}
			}},
		{name: "the Location of one added", viewer: olivia, method: "POST", path: fixed("/v1/orgs/acme/repos/website/secrets"), body: `{"name":"deploy","value":"k"}`, wantStatus: 201,
			check: func(t *testing.T, f *orgFixture, body []byte) {
				got := decode[Secret](t, body)
				rec := f.call(&f.olivia, "GET", "/v1/orgs/acme/secrets/"+got.Id.String(), "")
				if rec.Code != 200 {
					t.Errorf("GET of what was added = %d", rec.Code)
				}
			}},
		{name: "list the organization's", viewer: olivia, method: "GET", path: func(f *orgFixture) string { f.withSecrets(); return "/v1/orgs/acme/secrets" }, wantStatus: 200,
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				if got := decode[SecretList](t, body); len(got.Secrets) != 1 || got.Secrets[0].Name != "npm" || strings.Contains(string(body), "token") {
					t.Errorf("listed %s", body)
				}
			}},
		{name: "list the repository's", viewer: olivia, method: "GET", path: func(f *orgFixture) string { f.withSecrets(); return "/v1/orgs/acme/repos/website/secrets" }, wantStatus: 200,
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				if got := decode[SecretList](t, body); len(got.Secrets) != 1 || got.Secrets[0].Name != "deploy" {
					t.Errorf("listed %s", body)
				}
			}},
		{name: "list none", viewer: olivia, method: "GET", path: fixed("/v1/orgs/acme/secrets"), wantStatus: 200,
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				if string(body) != "{\"secrets\":[]}\n" {
					t.Errorf("listed %s", body)
				}
			}},
		{name: "list the organization's as a Member", viewer: maxim, method: "GET", path: fixed("/v1/orgs/acme/secrets"), wantStatus: 403, wantCode: "auth.forbidden"},
		{name: "list a repository's from outside", viewer: nia, method: "GET", path: fixed("/v1/orgs/acme/repos/website/secrets"), wantStatus: 404, wantCode: "organization.not_found"},
	})
}

func TestSecretAPI(t *testing.T) {
	runOrgCases(t, []orgCase{
		{name: "get one", viewer: olivia, method: "GET", path: npmPath(""), wantStatus: 200, wantHeader: http.Header{"Etag": []string{`"1"`}},
			check: func(t *testing.T, f *orgFixture, body []byte) {
				if got := decode[Secret](t, body); got.Name != "npm" || got.CreatedBy == nil || *got.CreatedBy != f.olivia.User.ID {
					t.Errorf("got %s", body)
				}
			}},
		{name: "get one the caller holds", viewer: olivia, method: "GET", path: npmPath(""), header: http.Header{"If-None-Match": []string{`"1"`}}, wantStatus: 304},
		{name: "get one that isn't there", viewer: olivia, method: "GET", path: fixed("/v1/orgs/acme/secrets/" + uuid.NewString()), wantStatus: 404, wantCode: "secret.not_found"},
		{name: "get one as a Member", viewer: maxim, method: "GET", path: npmPath(""), wantStatus: 403, wantCode: "auth.forbidden"},
		{name: "replace one", viewer: olivia, method: "PUT", path: npmPath(""), header: ifMatchHeader(`"1"`), body: `{"name":"npm-token","value":"new"}`, wantStatus: 200,
			wantHeader: http.Header{"Etag": []string{`"2"`}},
			check: func(t *testing.T, f *orgFixture, body []byte) {
				got := decode[Secret](t, body)
				if sealed := string(f.secretStore.Values[got.Id]); got.Name != "npm-token" || sealed != "sealed:"+secrets.About(got.Id)+":new" {
					t.Errorf("replaced %s, sealed as %q", body, sealed)
				}
			}},
		{name: "rename one and keep its value", viewer: olivia, method: "PUT", path: npmPath(""), header: ifMatchHeader("*"), body: `{"name":"npm-token"}`, wantStatus: 200,
			check: func(t *testing.T, f *orgFixture, body []byte) {
				got := decode[Secret](t, body)
				if sealed := string(f.secretStore.Values[got.Id]); sealed != "sealed:"+secrets.About(got.Id)+":npm-token" {
					t.Errorf("sealed as %q, want the value kept", sealed)
				}
			}},
		{name: "replace one without If-Match", viewer: olivia, method: "PUT", path: npmPath(""), body: `{"name":"npm"}`, wantStatus: 428, wantCode: "precondition.required"},
		{name: "replace one that changed", viewer: olivia, method: "PUT", path: npmPath(""), header: ifMatchHeader(`"7"`), body: `{"name":"npm"}`, wantStatus: 412, wantCode: "precondition.failed"},
		{name: "delete one", viewer: olivia, method: "DELETE", path: npmPath(""), header: ifMatchHeader(`"1"`), wantStatus: 204,
			check: func(t *testing.T, f *orgFixture, _ []byte) {
				if len(f.secretStore.Secrets) != 1 {
					t.Errorf("%d secrets left, want 1", len(f.secretStore.Secrets))
				}
			}},
		{name: "delete one without If-Match", viewer: olivia, method: "DELETE", path: npmPath(""), wantStatus: 428, wantCode: "precondition.required"},
	})
}

func TestSecretAccessAPI(t *testing.T) {
	runOrgCases(t, []orgCase{
		{name: "grant one to a job", viewer: olivia, method: "POST", path: npmPath("/access"), body: `{"principalId":"` + buildJob.String() + `"}`, wantStatus: 204,
			check: func(t *testing.T, f *orgFixture, _ []byte) {
				rec := f.call(&f.olivia, "GET", "/v1/orgs/acme/secrets/"+f.secretNamed("npm").ID.String()+"/access", "")
				got := decode[struct{ Access []Grantee }](t, rec.Body.Bytes())
				if rec.Code != 200 || len(got.Access) != 1 || got.Access[0].Kind != GranteeKindJob || got.Access[0].DisplayName != "build" {
					t.Errorf("access = %d %s", rec.Code, rec.Body)
				}
			}},
		{name: "grant one to an agent", viewer: olivia, method: "POST", path: npmPath("/access"), body: `{"principalId":"` + scoutAgent.String() + `"}`, wantStatus: 204,
			check: func(t *testing.T, f *orgFixture, _ []byte) {
				rec := f.call(&f.olivia, "GET", "/v1/orgs/acme/secrets/"+f.secretNamed("npm").ID.String()+"/access", "")
				got := decode[struct{ Access []Grantee }](t, rec.Body.Bytes())
				if rec.Code != 200 || len(got.Access) != 1 || got.Access[0].Kind != GranteeKindAgent || got.Access[0].DisplayName != "Scout" {
					t.Errorf("access = %d %s", rec.Code, rec.Body)
				}
			}},
		{name: "grant one twice", viewer: olivia, method: "POST", body: `{"principalId":"` + buildJob.String() + `"}`, wantStatus: 409, wantCode: "grant.exists",
			path: func(f *orgFixture) string {
				p := npmPath("/access")(f)
				f.store.Grant(buildJob, auth.RoleSecretReader, auth.ScopeSecret, f.secretNamed("npm").ID)
				return p
			}},
		{name: "grant one to a stranger", viewer: olivia, method: "POST", path: npmPath("/access"), body: `{"principalId":"` + uuid.NewString() + `"}`, wantStatus: 404, wantCode: "secret.not_found"},
		{name: "grant one as a Member", viewer: maxim, method: "POST", path: npmPath("/access"), body: `{"principalId":"` + buildJob.String() + `"}`, wantStatus: 403, wantCode: "auth.forbidden"},
		{name: "revoke one", viewer: olivia, method: "DELETE", wantStatus: 204,
			path: func(f *orgFixture) string {
				p := npmPath("/access/" + buildJob.String())(f)
				f.store.Grant(buildJob, auth.RoleSecretReader, auth.ScopeSecret, f.secretNamed("npm").ID)
				return p
			}},
		{name: "list the repository's jobs", viewer: olivia, method: "GET", path: func(f *orgFixture) string { f.withSecrets(); return "/v1/orgs/acme/repos/website/jobs" }, wantStatus: 200,
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				if got := decode[struct{ Jobs []Grantee }](t, body); len(got.Jobs) != 1 || got.Jobs[0].PrincipalId != buildJob || got.Jobs[0].Kind != GranteeKindJob {
					t.Errorf("jobs = %s", body)
				}
			}},
		{name: "list a repository's jobs from outside", viewer: nia, method: "GET", path: fixed("/v1/orgs/acme/repos/website/jobs"), wantStatus: 404, wantCode: "organization.not_found"},
		{name: "revoke one not granted", viewer: olivia, method: "DELETE", path: npmPath("/access/" + buildJob.String()), wantStatus: 404, wantCode: "grant.not_found"},
	})
}
