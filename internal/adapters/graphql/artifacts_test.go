package graphql

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/artifacts"
	"cobranch.dev/internal/services/artifacts/artifactstest"
	"cobranch.dev/internal/services/auth"
)

// The fixture's artifacts' IDs, each also its first version's: Olivia's plan, which only grants reach, and her chart,
// which website's readers view.
var (
	planID  = uuid.MustParse("00000000-0000-7000-8000-0000000ac001")
	chartID = uuid.MustParse("00000000-0000-7000-8000-0000000ac002")
)

// withArtifacts makes Max a Developer of website, and adds Olivia's plan, a Markdown document only grants reach, and
// her chart, an SVG diagram website's readers view.
func (f *orgFixture) withArtifacts() {
	f.store.Grant(f.max.User.ID, auth.RoleDeveloper, auth.ScopeRepository, f.website.ID)
	f.artifactStore.Others[buildJob] = artifactstest.Other{OrgID: f.acme.ID, Principal: artifacts.Principal{ID: buildJob, Kind: auth.KindJob, DisplayName: "build"}}
	for _, a := range []struct {
		id                 uuid.UUID
		title, media, body string
		kind               artifacts.Kind
		reach              artifacts.Reach
	}{
		{id: planID, title: "Plan", media: artifacts.MediaMarkdown, body: "# Plan\n", kind: artifacts.KindDocument, reach: artifacts.ReachNone},
		{id: chartID, title: "Chart", media: artifacts.MediaSVG, body: "<svg/>", kind: artifacts.KindDiagram, reach: artifacts.ReachView},
	} {
		if _, err := f.artifactStore.Create(context.Background(), f.olivia.User.ID, artifacts.New{
			Artifact: artifacts.Artifact{ID: a.id, OrgID: f.acme.ID, RepoID: f.website.ID, Title: a.title, Kind: a.kind, RepositoryAccess: a.reach,
				OrganizationAccess: artifacts.ReachNone},
			First:  artifacts.VersionInput{ID: a.id, MediaType: a.media, Body: a.body},
			Owners: []artifacts.NewGrant{{PrincipalID: f.olivia.User.ID, RoleID: auth.RoleArtifactOwner}},
		}); err != nil {
			panic(err)
		}
	}
}

// onArtifact is a mutation of field on the fixture's artifact id, with args after its IDs and selection after them.
func onArtifact(id uuid.UUID, field, args, selection string) func(f *orgFixture) string {
	return func(f *orgFixture) string {
		f.withArtifacts()
		return `mutation { ` + field + `(organizationId: "` + f.acme.ID.String() + `", id: "` + id.String() + `"` + args + `)` + selection + ` }`
	}
}

// artifactQuery is a query of the fixture's artifact id, with selection.
func artifactQuery(id uuid.UUID, selection string) func(f *orgFixture) string {
	return func(f *orgFixture) string {
		f.withArtifacts()
		return `{ organization(slug: "acme") { artifact(id: "` + id.String() + `") ` + selection + ` } }`
	}
}

func TestArtifacts(t *testing.T) {
	create := func(input string) func(f *orgFixture) string {
		return func(f *orgFixture) string {
			f.withArtifacts()
			return `mutation { createArtifact(organizationId: "` + f.acme.ID.String() + `", repositoryId: "` + f.website.ID.String() + `", input: {` + input +
				`}) { title kind audience repository { slug } head version createdBy { displayName } agent { id } permissions } }`
		}
	}
	runOrgCases(t, []orgCase{
		{name: "make one", viewer: maxim, document: create(`title: "Notes", kind: REPORT, repositoryAccess: VIEW, mediaType: "text/markdown", body: "# Notes"`),
			want: []string{`"title":"Notes","kind":"REPORT","audience":"REPOSITORY","repository":{"slug":"website"},"head":1,"version":1,"createdBy":{"displayName":"Max"},"agent":null,"permissions":null`}},
		{name: "make one under an ID another took", viewer: maxim, wantCode: conflict, wantReason: "artifact.exists",
			document: create(`id: "` + planID.String() + `", title: "Notes", kind: REPORT, mediaType: "text/markdown", body: "# Notes"`)},
		{name: "make one under an ID that isn't one", viewer: maxim, wantCode: badUserInput, wantReason: "artifact.id_invalid",
			document: create(`id: "x", title: "Notes", kind: REPORT, mediaType: "text/markdown", body: "# Notes"`)},
		{name: "make one of no format", viewer: maxim, wantCode: badUserInput, wantReason: "artifact.media_type_invalid",
			document: create(`title: "Notes", kind: REPORT, mediaType: "text/javascript", body: "alert(1)"`)},
		{name: "make one from outside", viewer: nia, wantCode: notFound, wantReason: "repository.not_found",
			document: create(`title: "Notes", kind: REPORT, mediaType: "text/markdown", body: "# Notes"`)},
		{name: "list a repository's", viewer: olivia, document: func(f *orgFixture) string {
			f.withArtifacts()
			return `{ organization(slug: "acme") { repository(slug: "website") { all: artifacts { title } diagrams: artifacts(kind: DIAGRAM) { title } } } }`
		}, want: []string{`"diagrams":[{"title":"Chart"}]`, `{"title":"Plan"}`}},
		{name: "list what a Developer reads", viewer: maxim, document: func(f *orgFixture) string {
			f.withArtifacts()
			return `{ organization(slug: "acme") { repository(slug: "website") { artifacts { title } } } }`
		}, want: []string{`"artifacts":[{"title":"Chart"}]`}},
		{name: "one, its versions and who reaches it", viewer: olivia,
			document: artifactQuery(planID, `{ title audience mediaType permissions versions { number body } versionByNumber(number: 0) { number body html } access { repositoryAccess grants { displayName kind role } } }`),
			want: []string{`"title":"Plan","audience":"PRIVATE","mediaType":"text/markdown","permissions":["artifacts.manage","artifacts.read","artifacts.write"]`,
				`"versions":[{"number":1,"body":null}]`, `"versionByNumber":{"number":1,"body":"# Plan\n","html":"`, `Plan\u003c/h1\u003e`,
				`"access":{"repositoryAccess":"NONE","grants":[{"displayName":"Olivia","kind":"user","role":"OWNER"}]}`}},
		{name: "a version that isn't there", viewer: olivia, document: artifactQuery(planID, `{ versionByNumber(number: 4) { number } }`),
			want: []string{`"versionByNumber":null`}},
		{name: "a private one", viewer: maxim, document: artifactQuery(planID, `{ title }`), want: []string{`"artifact":null`}},
		{name: "an ID that isn't one", viewer: olivia, document: fixed(`{ organization(slug: "acme") { artifact(id: "x") { title } } }`), want: []string{`"artifact":null`}},
	})
}

func TestArtifactChanges(t *testing.T) {
	runOrgCases(t, []orgCase{
		{name: "change one", viewer: olivia, document: onArtifact(planID, "updateArtifact", `, input: {title: "Roadmap", organizationAccess: EDIT}, expectedVersion: 1`, ` { title audience version }`),
			want: []string{`"title":"Roadmap","audience":"ORGANIZATION","version":2`}},
		{name: "change one changed since", viewer: olivia, document: onArtifact(planID, "updateArtifact", `, input: {title: "Roadmap"}, expectedVersion: 3`, ` { title }`),
			wantCode: conflict, wantReason: "precondition.failed"},
		{name: "change who a viewed one reaches", viewer: maxim, document: onArtifact(chartID, "updateArtifact", `, input: {repositoryAccess: EDIT}`, ` { title }`),
			wantCode: forbidden},
		{name: "add a version", viewer: olivia, document: onArtifact(chartID, "addArtifactVersion", `, input: {mediaType: "image/svg+xml", body: "<svg></svg>", note: "redrawn"}, expectedVersion: 1`,
			` { number note size body author { displayName } }`),
			want: []string{`"number":2,"note":"redrawn","size":11,"body":null,"author":{"displayName":"Olivia"}`}},
		{name: "add a version as a viewer", viewer: maxim, document: onArtifact(chartID, "addArtifactVersion", `, input: {mediaType: "image/svg+xml", body: "<svg/>"}`, ` { number }`),
			wantCode: forbidden},
		{name: "share one", viewer: olivia, document: func(f *orgFixture) string {
			return onArtifact(planID, "shareArtifact", `, principalId: "`+f.max.User.ID.String()+`", role: VIEWER`, ``)(f)
		}, want: []string{`"shareArtifact":true`}, check: func(t *testing.T, f *orgFixture, _ string) {
			if body := f.query(&f.max, `{ organization(slug: "acme") { artifact(id: "`+planID.String()+`") { title } } }`); body != `{"data":{"organization":{"artifact":{"title":"Plan"}}}}` {
				t.Errorf("Max reads %s", body)
			}
		}},
		{name: "share one with a job", viewer: olivia, document: onArtifact(planID, "shareArtifact", `, principalId: "`+buildJob.String()+`", role: VIEWER`, ``),
			wantCode: badUserInput, wantReason: "artifact.grantee_invalid"},
		{name: "unshare what nobody holds", viewer: olivia, document: onArtifact(planID, "unshareArtifact", `, principalId: "`+buildJob.String()+`"`, ``),
			wantCode: notFound, wantReason: "grant.not_found"},
		{name: "delete one", viewer: olivia, document: onArtifact(planID, "deleteArtifact", `, expectedVersion: 1`, ``), want: []string{`"deleteArtifact":true`},
			check: func(t *testing.T, f *orgFixture, _ string) {
				if _, ok := f.artifactStore.Artifacts[planID]; ok {
					t.Error("the plan is still there")
				}
			}},
		{name: "delete one as a viewer", viewer: maxim, document: onArtifact(chartID, "deleteArtifact", ``, ``), wantCode: forbidden},
	})
}
