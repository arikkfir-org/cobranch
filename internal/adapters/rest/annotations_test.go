package rest

import (
	"context"
	"net/http"
	"testing"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/code"
)

// maxDeveloping is Max as a Developer of website, who sets annotations and deletes his own.
func maxDeveloping(f *orgFixture) *auth.Authenticated {
	f.store.Grant(f.max.User.ID, auth.RoleDeveloper, auth.ScopeRepository, f.website.ID)
	f.codeStore.Names[f.max.User.ID] = "Max"
	return &f.max
}

// maxReading is Max as a Reader of website.
func maxReading(f *orgFixture) *auth.Authenticated {
	f.store.Grant(f.max.User.ID, auth.RoleReader, auth.ScopeRepository, f.website.ID)
	return &f.max
}

// oliviaAnnotated is Olivia, who set ci/build on main's head, c, Failed.
func oliviaAnnotated(f *orgFixture) *auth.Authenticated {
	f.codeStore.Names[f.olivia.User.ID] = "Olivia"
	in := code.AnnotationInput{Name: "ci/build", Status: code.AnnotationFailed, Summary: "2 tests failed", Link: "https://ci.example.com/7"}
	if _, err := f.codeStore.SetAnnotation(context.Background(), f.olivia.User.ID, f.website.ID, shaOf("c"), in); err != nil {
		panic(err)
	}
	return &f.olivia
}

// then makes the viewer of first's setup, as next.
func then(first, next func(*orgFixture) *auth.Authenticated) func(*orgFixture) *auth.Authenticated {
	return func(f *orgFixture) *auth.Authenticated {
		first(f)
		return next(f)
	}
}

const buildPath = "/v1/orgs/acme/repos/website/commits/cccccccccccccccccccccccccccccccccccccccc/annotations/ci%2Fbuild"

func TestSetAnnotationAPI(t *testing.T) {
	runOrgCases(t, []orgCase{
		{name: "a Developer sets one, its name escaped", viewer: maxDeveloping, method: http.MethodPut, path: fixed(buildPath),
			body: `{"status":"in_progress","summary":"Building","link":"https://ci.example.com/8"}`, wantStatus: 200,
			check: func(t *testing.T, f *orgFixture, body []byte) {
				a := decode[Annotation](t, body)
				if a.Name != "ci/build" || a.Status != "in_progress" || a.Summary != "Building" || a.Link != "https://ci.example.com/8" ||
					a.Principal == nil || a.Principal.Id != f.max.User.ID || a.Principal.DisplayName != "Max" || a.CompletedAt != nil {
					t.Errorf("annotation = %s", body)
				}
			}},
		{name: "with its status alone", viewer: maxDeveloping, method: http.MethodPut, path: fixed(buildPath), body: `{"status":"successful"}`, wantStatus: 200,
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				if a := decode[Annotation](t, body); a.Summary != "" || a.Link != "" || a.CompletedAt == nil {
					t.Errorf("annotation = %s", body)
				}
			}},
		{name: "its principal changes it", viewer: oliviaAnnotated, method: http.MethodPut, path: fixed(buildPath), body: `{"status":"successful"}`, wantStatus: 200},
		{name: "another principal's is refused, naming them", viewer: then(oliviaAnnotated, maxDeveloping), method: http.MethodPut, path: fixed(buildPath),
			body: `{"status":"successful"}`, wantStatus: 409, wantCode: "annotation.taken",
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				if p := decode[Problem](t, body); p.Detail == nil || *p.Detail != "Olivia set annotation ci/build on this commit; only they change it." {
					t.Errorf("problem = %s", body)
				}
			}},
		{name: "a Reader may not", viewer: maxReading, method: http.MethodPut, path: fixed(buildPath), body: `{"status":"pending"}`,
			wantStatus: 403, wantCode: "auth.forbidden"},
		{name: "a Member who doesn't read it finds nothing", viewer: maxim, method: http.MethodPut, path: fixed(buildPath), body: `{"status":"pending"}`,
			wantStatus: 404, wantCode: "repository.not_found"},
		{name: "a token with repo.read alone may not", viewer: oliviaToken("repo.read"), method: http.MethodPut, path: fixed(buildPath), body: `{"status":"pending"}`,
			wantStatus: 403, wantCode: "auth.forbidden"},
		{name: "a commit the repository doesn't hold", viewer: olivia, method: http.MethodPut, body: `{"status":"pending"}`,
			path: fixed("/v1/orgs/acme/repos/website/commits/" + shaOf("e") + "/annotations/ci"), wantStatus: 404, wantCode: "commit.not_found"},
		{name: "an unknown status", viewer: olivia, method: http.MethodPut, path: fixed(buildPath), body: `{"status":"passed"}`,
			wantStatus: 400, wantCode: "status.invalid"},
		{name: "a name in uppercase", viewer: olivia, method: http.MethodPut, body: `{"status":"pending"}`,
			path: fixed("/v1/orgs/acme/repos/website/commits/" + shaOf("c") + "/annotations/CI"), wantStatus: 400, wantCode: "name.invalid"},
		{name: "a link that isn't a URL of the web", viewer: olivia, method: http.MethodPut, path: fixed(buildPath), body: `{"status":"failed","link":"file:///etc/passwd"}`,
			wantStatus: 400, wantCode: "link.invalid"},
	})
}

func TestListAnnotationsAPI(t *testing.T) {
	path := "/v1/orgs/acme/repos/website/commits/" + shaOf("c") + "/annotations"
	runOrgCases(t, []orgCase{
		{name: "a Reader lists them", viewer: then(oliviaAnnotated, maxReading), method: http.MethodGet, path: fixed(path), wantStatus: 200,
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				got := decode[struct{ Annotations []Annotation }](t, body).Annotations
				if len(got) != 1 || got[0].Name != "ci/build" || got[0].Status != "failed" || got[0].CompletedAt == nil {
					t.Errorf("annotations = %s", body)
				}
			}},
		{name: "none yet", viewer: olivia, method: http.MethodGet, path: fixed(path), wantStatus: 200,
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				if string(body) != `{"annotations":[]}`+"\n" {
					t.Errorf("annotations = %s", body)
				}
			}},
		{name: "a commit the repository doesn't hold", viewer: olivia, method: http.MethodGet, path: fixed("/v1/orgs/acme/repos/website/commits/" + shaOf("e") + "/annotations"),
			wantStatus: 404, wantCode: "commit.not_found"},
		{name: "a Member who doesn't read it", viewer: maxim, method: http.MethodGet, path: fixed(path), wantStatus: 404, wantCode: "repository.not_found"},
	})
}

func TestDeleteAnnotationAPI(t *testing.T) {
	runOrgCases(t, []orgCase{
		{name: "its principal deletes it", viewer: oliviaAnnotated, method: http.MethodDelete, path: fixed(buildPath), wantStatus: 204,
			check: func(t *testing.T, f *orgFixture, _ []byte) {
				if left := f.codeStore.Notes[f.website.ID][shaOf("c")]; len(left) != 0 {
					t.Errorf("annotations left = %+v", left)
				}
			}},
		{name: "a Developer may not delete another's", viewer: then(oliviaAnnotated, maxDeveloping), method: http.MethodDelete, path: fixed(buildPath),
			wantStatus: 403, wantCode: "auth.forbidden"},
		{name: "a Maintainer deletes another's", viewer: then(oliviaAnnotated, func(f *orgFixture) *auth.Authenticated {
			f.store.Grant(f.max.User.ID, auth.RoleMaintainer, auth.ScopeRepository, f.website.ID)
			return &f.max
		}), method: http.MethodDelete, path: fixed(buildPath), wantStatus: 204},
		{name: "one that isn't there", viewer: olivia, method: http.MethodDelete, path: fixed(buildPath), wantStatus: 404, wantCode: "annotation.not_found"},
	})
}

func TestCommitsCarryAnnotations(t *testing.T) {
	runOrgCases(t, []orgCase{
		{name: "a commit with its annotations", viewer: oliviaAnnotated, method: http.MethodGet, path: fixed("/v1/orgs/acme/repos/website/commits/" + shaOf("c")),
			wantStatus: 200, check: func(t *testing.T, _ *orgFixture, body []byte) {
				if c := decode[Commit](t, body); len(c.Annotations) != 1 || c.Annotations[0].Name != "ci/build" {
					t.Errorf("commit = %s", body)
				}
			}},
		{name: "a commit without any lists none", viewer: olivia, method: http.MethodGet, path: fixed("/v1/orgs/acme/repos/website/commits/" + shaOf("b")),
			wantStatus: 200, check: func(t *testing.T, _ *orgFixture, body []byte) {
				if c := decode[Commit](t, body); c.Annotations == nil || len(c.Annotations) != 0 {
					t.Errorf("commit = %s", body)
				}
			}},
	})
}
