package rest

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/artifacts"
	"cobranch.dev/internal/services/artifacts/artifactstest"
	"cobranch.dev/internal/services/auth"
)

// The fixture's artifacts' IDs, each also its first version's: Olivia's plan, which only grants reach, her chart, which
// website's readers view, and the notes a case makes.
var (
	planID  = uuid.MustParse("00000000-0000-7000-8000-0000000ac001")
	chartID = uuid.MustParse("00000000-0000-7000-8000-0000000ac002")
	notesID = uuid.MustParse("00000000-0000-7000-8000-0000000ac003")
	// scoutTask is the task Olivia assigned Scout, number 12 on branch scout/notes.
	scoutTask = uuid.MustParse("00000000-0000-7000-8000-0000000ac0f1")
)

// withArtifacts makes Max a Developer of website and Scout, a Guest of Acme's, one too, and adds Olivia's plan, a Markdown document only
// grants reach, and her chart, an SVG diagram website's readers view.
func (f *orgFixture) withArtifacts(t *testing.T) {
	t.Helper()
	f.store.Grant(f.max.User.ID, auth.RoleDeveloper, auth.ScopeRepository, f.website.ID)
	f.store.Grant(scoutAgent, auth.RoleGuest, auth.ScopeOrganization, f.acme.ID)
	f.store.Grant(scoutAgent, auth.RoleDeveloper, auth.ScopeRepository, f.website.ID)
	f.artifactStore.Others[scoutAgent] = artifactstest.Other{OrgID: f.acme.ID, Principal: artifacts.Principal{ID: scoutAgent, Kind: auth.KindAgent, DisplayName: "Scout"}}
	f.artifactStore.Others[buildJob] = artifactstest.Other{OrgID: f.acme.ID, Principal: artifacts.Principal{ID: buildJob, Kind: auth.KindJob, DisplayName: "build"}}
	f.artifactStore.Tasks[scoutTask] = artifactstest.Task{Number: 12, Branch: "scout/notes"}
	for _, a := range []struct {
		id                 uuid.UUID
		title, media, body string
		kind               artifacts.Kind
		reach              artifacts.Reach
	}{
		{id: planID, title: "Plan", media: artifacts.MediaMarkdown, body: "# Plan\n", kind: artifacts.KindDocument, reach: artifacts.ReachNone},
		{id: chartID, title: "Q1 / Q2 chart", media: artifacts.MediaSVG, body: "<svg/>", kind: artifacts.KindDiagram, reach: artifacts.ReachView},
	} {
		if _, err := f.artifactStore.Create(t.Context(), f.olivia.User.ID, artifacts.New{
			Artifact: artifacts.Artifact{ID: a.id, OrgID: f.acme.ID, RepoID: f.website.ID, Title: a.title, Kind: a.kind, RepositoryAccess: a.reach,
				OrganizationAccess: artifacts.ReachNone},
			First:  artifacts.VersionInput{ID: a.id, MediaType: a.media, Body: a.body},
			Owners: []artifacts.NewGrant{{PrincipalID: f.olivia.User.ID, RoleID: auth.RoleArtifactOwner}},
		}); err != nil {
			t.Fatal(err)
		}
	}
}

// artifactPath is the URL of the fixture's artifact id, with suffix after it.
func artifactPath(t *testing.T, id uuid.UUID, suffix string) func(f *orgFixture) string {
	return func(f *orgFixture) string {
		f.withArtifacts(t)
		return "/v1/orgs/acme/artifacts/" + id.String() + suffix
	}
}

// repoArtifacts is the URL of website's artifacts, with query after it.
func repoArtifacts(t *testing.T, query string) func(f *orgFixture) string {
	return func(f *orgFixture) string {
		f.withArtifacts(t)
		return "/v1/orgs/acme/repos/website/artifacts" + query
	}
}

// scoutOnTask is Scout signed in through a token of scoutTask, which Olivia assigned.
func scoutOnTask(f *orgFixture) *auth.Authenticated {
	return &auth.Authenticated{User: auth.User{Principal: auth.Principal{ID: scoutAgent, Kind: auth.KindAgent, DisplayName: "Scout"}},
		Credential: auth.Credential{Kind: auth.CredentialTaskToken, PrincipalID: scoutAgent, Task: auth.TaskGrant{ID: scoutTask, AssignerID: f.olivia.User.ID}}}
}

func TestArtifactsAPI(t *testing.T) {
	notes := `{"id":"` + notesID.String() + `","title":"Notes","kind":"report","repositoryAccess":"view","mediaType":"text/markdown","body":"# Notes\n"}`
	runOrgCases(t, []orgCase{
		{name: "make one", viewer: maxim, method: "POST", path: repoArtifacts(t, ""), body: notes, wantStatus: 201,
			wantHeader: http.Header{"Etag": []string{`"1"`}, "Location": []string{"/v1/orgs/acme/artifacts/" + notesID.String()}},
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				got := decode[Artifact](t, body)
				if got.Id != notesID || got.Repository.Slug != "website" || got.Audience != ArtifactAudienceRepository || got.Head != 1 ||
					got.CreatedBy == nil || got.CreatedBy.DisplayName != "Max" || got.Agent != nil || got.Permissions != nil {
					t.Errorf("made %s", body)
				}
			}},
		{name: "make the same one again", viewer: maxim, method: "POST", body: notes, wantStatus: 200, wantHeader: http.Header{"Etag": []string{`"1"`}},
			path: func(f *orgFixture) string {
				p := repoArtifacts(t, "")(f)
				if rec := f.call(&f.max, "POST", p, notes); rec.Code != 201 {
					t.Fatalf("first call: %d %s", rec.Code, rec.Body)
				}
				return p
			}},
		{name: "make one under an ID another took", viewer: maxim, method: "POST", path: repoArtifacts(t, ""), wantStatus: 409, wantCode: "artifact.exists",
			body: strings.Replace(notes, notesID.String(), planID.String(), 1)},
		{name: "make one of an unknown kind", viewer: maxim, method: "POST", path: repoArtifacts(t, ""), wantStatus: 400, wantCode: "artifact.kind_invalid",
			body: strings.Replace(notes, `"report"`, `"movie"`, 1)},
		{name: "make one without artifacts.create", viewer: olivia, method: "POST", wantStatus: 403, wantCode: "auth.forbidden", body: notes,
			path: func(f *orgFixture) string {
				p := repoArtifacts(t, "")(f)
				f.store.Grants = f.store.Grants[:0]
				f.store.Grant(f.olivia.User.ID, auth.RoleGuest, auth.ScopeOrganization, f.acme.ID)
				f.store.Grant(f.olivia.User.ID, auth.RoleReader, auth.ScopeRepository, f.website.ID)
				return p
			}},
		{name: "make one from outside", viewer: nia, method: "POST", path: repoArtifacts(t, ""), body: notes, wantStatus: 404, wantCode: "organization.not_found"},
		{name: "an agent's task makes one", viewer: scoutOnTask, method: "POST", path: repoArtifacts(t, ""), body: notes, wantStatus: 201,
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				got := decode[Artifact](t, body)
				if got.Agent == nil || got.Agent.DisplayName != "Scout" || got.TaskNumber == nil || *got.TaskNumber != 12 || got.Branch != "scout/notes" {
					t.Errorf("made %s", body)
				}
			}},
		{name: "list", viewer: olivia, method: "GET", path: repoArtifacts(t, ""), wantStatus: 200, check: func(t *testing.T, _ *orgFixture, body []byte) {
			if got := decode[ArtifactList](t, body).Artifacts; len(got) != 2 {
				t.Errorf("listed %s", body)
			}
		}},
		{name: "list a kind", viewer: olivia, method: "GET", path: repoArtifacts(t, "?kind=diagram"), wantStatus: 200, check: func(t *testing.T, _ *orgFixture, body []byte) {
			if got := decode[ArtifactList](t, body).Artifacts; len(got) != 1 || got[0].Id != chartID || got[0].Kind != Diagram {
				t.Errorf("listed %s", body)
			}
		}},
		{name: "list what a Developer reads", viewer: maxim, method: "GET", path: repoArtifacts(t, ""), wantStatus: 200, check: func(t *testing.T, _ *orgFixture, body []byte) {
			if got := decode[ArtifactList](t, body).Artifacts; len(got) != 1 || got[0].Id != chartID {
				t.Errorf("listed %s", body)
			}
		}},
	})
}

func TestArtifactAPI(t *testing.T) {
	runOrgCases(t, []orgCase{
		{name: "get one", viewer: olivia, method: "GET", path: artifactPath(t, planID, ""), wantStatus: 200, wantHeader: http.Header{"Etag": []string{`"1"`}},
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				got := decode[Artifact](t, body)
				if got.Audience != ArtifactAudiencePrivate || got.Permissions == nil || len(*got.Permissions) != 3 || got.MediaType != Textmarkdown {
					t.Errorf("got %s", body)
				}
			}},
		{name: "get one held as it is", viewer: olivia, method: "GET", path: artifactPath(t, planID, ""), header: http.Header{"If-None-Match": []string{`"1"`}}, wantStatus: 304},
		{name: "get a private one", viewer: maxim, method: "GET", path: artifactPath(t, planID, ""), wantStatus: 404, wantCode: "artifact.not_found"},
		{name: "change one without If-Match", viewer: olivia, method: "PATCH", path: artifactPath(t, planID, ""), body: `{"title":"Roadmap"}`, wantStatus: 428, wantCode: "precondition.required"},
		{name: "change one changed since", viewer: olivia, method: "PATCH", path: artifactPath(t, planID, ""), body: `{"title":"Roadmap"}`, header: ifMatchHeader(`"4"`),
			wantStatus: 412, wantCode: "precondition.failed"},
		{name: "change one", viewer: olivia, method: "PATCH", path: artifactPath(t, planID, ""), body: `{"title":"Roadmap","organizationAccess":"edit"}`,
			header: ifMatchHeader(`"1"`), wantStatus: 200, wantHeader: http.Header{"Etag": []string{`"2"`}},
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				if got := decode[Artifact](t, body); got.Title != "Roadmap" || got.Audience != ArtifactAudienceOrganization {
					t.Errorf("changed %s", body)
				}
			}},
		{name: "change who a viewed one reaches", viewer: maxim, method: "PATCH", path: artifactPath(t, chartID, ""), body: `{"repositoryAccess":"edit"}`,
			header: ifMatchHeader("*"), wantStatus: 403, wantCode: "auth.forbidden"},
		{name: "delete one", viewer: olivia, method: "DELETE", path: artifactPath(t, planID, ""), header: ifMatchHeader(`"1"`), wantStatus: 204,
			check: func(t *testing.T, f *orgFixture, _ []byte) {
				if _, ok := f.artifactStore.Artifacts[planID]; ok {
					t.Error("the plan is still there")
				}
			}},
	})
}

func TestArtifactVersionsAPI(t *testing.T) {
	runOrgCases(t, []orgCase{
		{name: "add a version", viewer: olivia, method: "POST", path: artifactPath(t, planID, "/versions"), header: ifMatchHeader(`"1"`),
			body: `{"mediaType":"text/html","body":"<p>Plan</p>","note":"as a page"}`, wantStatus: 201,
			wantHeader: http.Header{"Etag": []string{`"2"`}, "Location": []string{"/v1/orgs/acme/artifacts/" + planID.String() + "/versions/2"}},
			check: func(t *testing.T, f *orgFixture, body []byte) {
				got := decode[ArtifactVersion](t, body)
				if got.Number != 2 || got.Body != nil || got.Note != "as a page" || got.Size != 11 || len(f.artifactStore.Kept[planID]) != 2 {
					t.Errorf("added %s", body)
				}
			}},
		{name: "add a version without If-Match", viewer: olivia, method: "POST", path: artifactPath(t, planID, "/versions"),
			body: `{"mediaType":"text/html","body":"<p>Plan</p>"}`, wantStatus: 428, wantCode: "precondition.required"},
		{name: "add a version as a viewer", viewer: maxim, method: "POST", path: artifactPath(t, chartID, "/versions"), header: ifMatchHeader("*"),
			body: `{"mediaType":"image/svg+xml","body":"<svg/>"}`, wantStatus: 403, wantCode: "auth.forbidden"},
		{name: "list the versions", viewer: maxim, method: "GET", path: artifactPath(t, chartID, "/versions"), wantStatus: 200,
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				if got := decode[ArtifactVersionList](t, body).Versions; len(got) != 1 || got[0].Id != chartID || got[0].Body != nil {
					t.Errorf("listed %s", body)
				}
			}},
		{name: "get the newest version, rendered", viewer: olivia, method: "GET", path: artifactPath(t, planID, "/versions/0"), wantStatus: 200,
			wantHeader: http.Header{"Etag": []string{`"` + planID.String() + `"`}},
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				got := decode[ArtifactVersion](t, body)
				if got.Body == nil || *got.Body != "# Plan\n" || got.Html == nil || !strings.Contains(*got.Html, "Plan</h1>") {
					t.Errorf("got %s", body)
				}
			}},
		{name: "get a version that isn't there", viewer: olivia, method: "GET", path: artifactPath(t, planID, "/versions/2"), wantStatus: 404, wantCode: "artifact.not_found"},
		{name: "get a body, sandboxed", viewer: maxim, method: "GET", path: artifactPath(t, chartID, "/versions/1/raw"), wantStatus: 200,
			wantHeader: http.Header{"Content-Type": []string{"image/svg+xml"}, "Content-Security-Policy": []string{artifactPolicy},
				"X-Content-Type-Options": []string{"nosniff"}, "Cross-Origin-Resource-Policy": []string{"same-site"}, "Content-Disposition": []string{""},
				"Cache-Control": []string{"private, no-cache"}, "Etag": []string{`"` + chartID.String() + `"`}},
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				if string(body) != "<svg/>" {
					t.Errorf("body = %q", body)
				}
			}},
		{name: "download a body", viewer: maxim, method: "GET", path: artifactPath(t, chartID, "/versions/0/raw?download=true"), wantStatus: 200,
			wantHeader: http.Header{"Content-Disposition": []string{`attachment; filename="Q1 - Q2 chart.svg"`}}},
		{name: "get a body held as it is", viewer: maxim, method: "GET", path: artifactPath(t, chartID, "/versions/1/raw"), wantStatus: 304,
			header: http.Header{"If-None-Match": []string{`"` + chartID.String() + `"`}}},
		{name: "get a private body", viewer: maxim, method: "GET", path: artifactPath(t, planID, "/versions/1/raw"), wantStatus: 404, wantCode: "artifact.not_found"},
	})
}

func TestArtifactAccessAPI(t *testing.T) {
	runOrgCases(t, []orgCase{
		{name: "share one", viewer: olivia, method: "POST", path: artifactPath(t, planID, "/access"), wantStatus: 204,
			bodyOf: func(f *orgFixture) string { return `{"principalId":"` + f.max.User.ID.String() + `","role":"editor"}` },
			check: func(t *testing.T, f *orgFixture, _ []byte) {
				if rec := f.call(&f.max, "GET", "/v1/orgs/acme/artifacts/"+planID.String()+"/access", ""); rec.Code != 200 ||
					!strings.Contains(rec.Body.String(), `"displayName":"Max","kind":"user","principalId":"`+f.max.User.ID.String()+`","role":"editor"`) {
					t.Errorf("access = %d %s", rec.Code, rec.Body)
				}
			}},
		{name: "share one with a job", viewer: olivia, method: "POST", path: artifactPath(t, planID, "/access"), wantStatus: 400, wantCode: "artifact.grantee_invalid",
			body: `{"principalId":"` + buildJob.String() + `","role":"viewer"}`},
		{name: "share one as a viewer", viewer: maxim, method: "POST", path: artifactPath(t, chartID, "/access"), wantStatus: 403, wantCode: "auth.forbidden",
			body: `{"principalId":"` + scoutAgent.String() + `","role":"viewer"}`},
		{name: "unshare one", viewer: olivia, method: "DELETE", path: artifactPath(t, planID, "/access/"+scoutAgent.String()), wantStatus: 204,
			headerFor: func(f *orgFixture) http.Header {
				f.store.Grant(scoutAgent, auth.RoleArtifactViewer, auth.ScopeArtifact, planID)
				return nil
			}},
		{name: "unshare what nobody holds", viewer: olivia, method: "DELETE", path: artifactPath(t, planID, "/access/"+scoutAgent.String()), wantStatus: 404,
			wantCode: "grant.not_found"},
	})
}

func TestFileName(t *testing.T) {
	tests := []struct{ title, want string }{
		{title: "Plan", want: "Plan"},
		{title: "Q1/Q2\\Q3", want: "Q1-Q2-Q3"},
		{title: "a\tb", want: "a-b"},
		{title: "..", want: "artifact"},
		{title: "  ", want: "artifact"},
	}
	for _, tt := range tests {
		if got := fileName(tt.title); got != tt.want {
			t.Errorf("fileName(%q) = %q, want %q", tt.title, got, tt.want)
		}
	}
}
