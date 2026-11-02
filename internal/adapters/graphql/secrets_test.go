package graphql

import (
	"testing"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/secrets"
	"cobranch.dev/internal/services/secrets/secretstest"
)

// buildJob is the principal of build, a job of website's.
var buildJob = uuid.MustParse("00000000-0000-7000-8000-00000000b001")

// withSecrets adds Acme's secret npm and website's deploy, sealed as authtest.Sealer seals them, and buildJob, to the
// fixture.
func (f *orgFixture) withSecrets() (npm, deploy secrets.Secret) {
	npm = secrets.Secret{ID: uuid.New(), OrgID: f.acme.ID, Name: "npm", Version: 1, CreatedBy: f.olivia.User.ID, CreatedAt: now, UpdatedAt: now}
	deploy = secrets.Secret{ID: uuid.New(), OrgID: f.acme.ID, RepoID: f.website.ID, Name: "deploy", Version: 2, CreatedAt: now, UpdatedAt: now}
	for s, value := range map[secrets.Secret]string{npm: "npm-token", deploy: "deploy-key"} {
		f.secretStore.Add(s, []byte("sealed:"+secrets.About(s.ID)+":"+value))
	}
	f.secretStore.Others[buildJob] = secretstest.Other{OrgID: f.acme.ID, RepoID: f.website.ID, Principal: secrets.Principal{ID: buildJob, Kind: auth.KindJob, DisplayName: "build"}}
	return npm, deploy
}

// onNpm is a mutation of field on the fixture's secret npm, with args after its IDs and selection after them.
func onNpm(field, args, selection string) func(f *orgFixture) string {
	return func(f *orgFixture) string {
		npm, _ := f.withSecrets()
		return `mutation { ` + field + `(organizationId: "` + f.acme.ID.String() + `", id: "` + npm.ID.String() + `"` + args + `)` + selection + ` }`
	}
}

func TestSecrets(t *testing.T) {
	job := `, principalId: "` + buildJob.String() + `"`
	runOrgCases(t, []orgCase{
		{name: "add one to the organization", viewer: olivia, document: func(f *orgFixture) string {
			return `mutation { createSecret(organizationId: "` + f.acme.ID.String() + `", input: {name: "npm", value: "npm-token"}) { name repositoryId version createdBy } }`
		}, want: []string{`"name":"npm","repositoryId":null,"version":1,"createdBy":"`}},
		{name: "add one to a repository", viewer: olivia, document: func(f *orgFixture) string {
			return `mutation { createSecret(organizationId: "` + f.acme.ID.String() + `", repositoryId: "` + f.website.ID.String() + `", input: {name: "deploy", value: "k"}) { name repositoryId } }`
		}, want: []string{`"name":"deploy","repositoryId":"`}},
		{name: "add one as a Member", viewer: maxim, document: func(f *orgFixture) string {
			return `mutation { createSecret(organizationId: "` + f.acme.ID.String() + `", input: {name: "npm", value: "x"}) { name } }`
		}, wantCode: forbidden},
		{name: "add one under a taken name", viewer: olivia, document: func(f *orgFixture) string {
			f.withSecrets()
			return `mutation { createSecret(organizationId: "` + f.acme.ID.String() + `", input: {name: "npm", value: "x"}) { name } }`
		}, wantCode: conflict, wantReason: "secret.name_taken"},
		{name: "add one without a secrets key", viewer: olivia, document: func(f *orgFixture) string {
			f.secrets = secrets.NewService(f.secretStore, auth.NewAuthorizer(f.store), nil)
			return `mutation { createSecret(organizationId: "` + f.acme.ID.String() + `", input: {name: "npm", value: "x"}) { name } }`
		}, wantCode: conflict, wantReason: "secrets_key.missing"},
		{name: "add one to a repository that isn't", viewer: olivia, document: func(f *orgFixture) string {
			return `mutation { createSecret(organizationId: "` + f.acme.ID.String() + `", repositoryId: "x", input: {name: "npm", value: "x"}) { name } }`
		}, wantCode: notFound, wantReason: "repository.not_found"},
		{name: "list the organization's and a repository's", viewer: olivia, document: func(f *orgFixture) string {
			f.withSecrets()
			return `{ organization(slug: "acme") { secrets { name } repository(slug: "website") { secrets { name version } } } }`
		}, want: []string{`"secrets":[{"name":"npm"}]`, `"secrets":[{"name":"deploy","version":2}]`}},
		{name: "list the organization's as a Member", viewer: maxim, document: func(*orgFixture) string {
			return `{ organization(slug: "acme") { secrets { name } } }`
		}, wantCode: forbidden},
		{name: "rename one and replace its value", viewer: olivia, document: onNpm("updateSecret", `, input: {name: "npm-token", value: "new"}, expectedVersion: 1`, ` { name version }`),
			want: []string{`"name":"npm-token","version":2`}},
		{name: "change one that changed", viewer: olivia, document: onNpm("updateSecret", `, input: {name: "npm"}, expectedVersion: 7`, ` { name }`),
			wantCode: conflict, wantReason: "precondition.failed"},
		{name: "change one with a version that isn't", viewer: olivia, document: onNpm("updateSecret", `, input: {name: "npm"}, expectedVersion: 0`, ` { name }`),
			wantCode: badUserInput, wantReason: "version.invalid"},
		{name: "delete one", viewer: olivia, document: onNpm("deleteSecret", ``, ``), want: []string{`"deleteSecret":true`}},
		{name: "delete one that isn't", viewer: olivia, document: func(f *orgFixture) string {
			return `mutation { deleteSecret(organizationId: "` + f.acme.ID.String() + `", id: "` + uuid.NewString() + `") }`
		}, wantCode: notFound, wantReason: "secret.not_found"},
		{name: "grant one to a job", viewer: olivia, document: onNpm("grantSecret", job, ``), want: []string{`"grantSecret":true`}},
		{name: "grant one as a Member", viewer: maxim, document: onNpm("grantSecret", job, ``), wantCode: forbidden},
		{name: "who receives one", viewer: olivia, document: func(f *orgFixture) string {
			npm, _ := f.withSecrets()
			f.store.Grant(buildJob, auth.RoleSecretReader, auth.ScopeSecret, npm.ID)
			return `{ organization(slug: "acme") { secrets { name access { kind displayName } } } }`
		}, want: []string{`"access":[{"kind":"job","displayName":"build"}]`}},
		{name: "the repository's jobs", viewer: olivia, document: func(f *orgFixture) string {
			f.withSecrets()
			return `{ organization(slug: "acme") { repository(slug: "website") { jobPrincipals { principalId kind displayName } } } }`
		}, want: []string{`"jobPrincipals":[{"principalId":"` + buildJob.String() + `","kind":"job","displayName":"build"}]`}},
		{name: "revoke one not granted", viewer: olivia, document: onNpm("revokeSecret", job, ``), wantCode: notFound, wantReason: "grant.not_found"},
	})
}
