package rest

import (
	"net/http"
	"testing"
	"time"

	"cobranch.dev/internal/services/code"
)

func TestViewedFilesAPI(t *testing.T) {
	const feature = "/v1/orgs/acme/repos/website/branches/feature/viewed"
	// marked readies Olivia's marks on feature: a.go at a, and gone.go, which the branch deletes.
	marked := func(f *orgFixture) {
		f.codeStore.Viewed[f.olivia.User.ID.String()+"/"+f.website.ID.String()+"/feature"] = []code.ViewedFile{
			{Path: "a.go", Object: shaOf("a"), ViewedAt: now}, {Path: "gone.go", ViewedAt: now}}
	}
	// tagOf is If-None-Match or If-Match, by name, naming Olivia's marks as marked readies them.
	tagOf := func(name string) func(f *orgFixture) http.Header {
		return func(f *orgFixture) http.Header {
			marked(f)
			h := http.Header{"Content-Type": {"application/merge-patch+json"}}
			h.Set(name, viewedTag(f.codeStore.Viewed[f.olivia.User.ID.String()+"/"+f.website.ID.String()+"/feature"]))
			return h
		}
	}
	whichever := http.Header{"Content-Type": {"application/merge-patch+json"}, "If-Match": {"*"}}
	runOrgCases(t, []orgCase{
		{name: "the viewer's marks, by path", viewer: olivia, before: marked, method: "GET", path: fixed(feature), wantStatus: 200,
			check: wantViewed(map[string]string{"a.go": shaOf("a"), "gone.go": ""})},
		{name: "none yet", viewer: olivia, method: "GET", path: fixed(feature), wantStatus: 200, check: wantViewed(map[string]string{})},
		{name: "unchanged since the caller read them", viewer: olivia, method: "GET", path: fixed(feature), headerFor: tagOf("If-None-Match"),
			wantStatus: 304},
		{name: "a branch named with a slash, escaped", viewer: olivia, method: "GET", path: fixed("/v1/orgs/acme/repos/website/branches/feature%2Flogin/viewed"),
			wantStatus: 200, check: wantViewed(map[string]string{})},
		{name: "a branch that doesn't live", viewer: olivia, method: "GET", path: fixed("/v1/orgs/acme/repos/website/branches/gone/viewed"),
			wantStatus: 404, wantCode: "branch.not_found"},
		{name: "a Member who doesn't read it finds no repository", viewer: maxim, method: "GET", path: fixed(feature), wantStatus: 404,
			wantCode: "repository.not_found"},
		{name: "no one signed in", method: "GET", path: fixed(feature), wantStatus: 401, wantCode: "auth.unauthenticated"},
		{name: "marks, moves and takes off marks", viewer: olivia, method: "PATCH", path: fixed(feature), headerFor: tagOf("If-Match"),
			body:       `{"files":{"b.go":{"object":"` + shaOf("b") + `"},"a.go":{"object":"` + shaOf("c") + `"},"old.go":{},"gone.go":null}}`,
			wantStatus: 200, check: wantViewed(map[string]string{"a.go": shaOf("c"), "b.go": shaOf("b"), "old.go": ""})},
		{name: "whichever marks are there", viewer: olivia, before: marked, method: "PATCH", path: fixed(feature), header: whichever,
			body: `{"files":{"a.go":null}}`, wantStatus: 200, check: wantViewed(map[string]string{"gone.go": ""})},
		{name: "a patch without files changes nothing", viewer: olivia, before: marked, method: "PATCH", path: fixed(feature), header: whichever,
			body: `{}`, wantStatus: 200, check: wantViewed(map[string]string{"a.go": shaOf("a"), "gone.go": ""})},
		{name: "marks that changed since", viewer: olivia, before: marked, method: "PATCH", path: fixed(feature),
			header: http.Header{"Content-Type": {"application/merge-patch+json"}, "If-Match": {`"` + code.ViewedTag(nil) + `"`}},
			body:   `{"files":{"a.go":null}}`, wantStatus: 412, wantCode: "precondition.failed"},
		{name: "a weak tag", viewer: olivia, method: "PATCH", path: fixed(feature),
			header: http.Header{"Content-Type": {"application/merge-patch+json"}, "If-Match": {`W/"` + code.ViewedTag(nil) + `"`}},
			body:   `{"files":{"a.go":null}}`, wantStatus: 412, wantCode: "precondition.failed"},
		{name: "no If-Match", viewer: olivia, method: "PATCH", path: fixed(feature), header: mergePatch, body: `{"files":{"a.go":null}}`,
			wantStatus: 428, wantCode: "precondition.required"},
		{name: "an object that isn't one's full name", viewer: olivia, method: "PATCH", path: fixed(feature), header: whichever,
			body: `{"files":{"a.go":{"object":"abc"}}}`, wantStatus: 400, wantCode: "object.invalid"},
		{name: "a path that climbs out", viewer: olivia, method: "PATCH", path: fixed(feature), header: whichever,
			body: `{"files":{"../a.go":{}}}`, wantStatus: 400, wantCode: "path.invalid"},
		{name: "a branch that doesn't live", viewer: olivia, method: "PATCH", path: fixed("/v1/orgs/acme/repos/website/branches/gone/viewed"),
			header: whichever, body: `{"files":{"a.go":{}}}`, wantStatus: 404, wantCode: "branch.not_found"},
		{name: "a Member who doesn't read it finds no repository", viewer: maxim, method: "PATCH", path: fixed(feature), header: whichever,
			body: `{"files":{"a.go":{}}}`, wantStatus: 404, wantCode: "repository.not_found"},
		{name: "no one signed in", method: "PATCH", path: fixed(feature), header: whichever, body: `{"files":{"a.go":{}}}`, wantStatus: 401,
			wantCode: "auth.unauthenticated"},
	})
}

// wantViewed checks that the answer holds the marks of want, path by path, each with its object ("" for none) and the
// moment it was made, in UTC.
func wantViewed(want map[string]string) func(t *testing.T, f *orgFixture, body []byte) {
	return func(t *testing.T, _ *orgFixture, body []byte) {
		t.Helper()
		got := decode[ViewedFiles](t, body)
		if len(got.Files) != len(want) {
			t.Fatalf("got %s, want %v", body, want)
		}
		for path, object := range want {
			m, ok := got.Files[path]
			if !ok || (m.Object == nil) != (object == "") || (m.Object != nil && *m.Object != object) || m.ViewedAt.IsZero() ||
				m.ViewedAt.Location() != time.UTC {
				t.Errorf("%s: got %s, want it at %q", path, body, object)
			}
		}
	}
}
