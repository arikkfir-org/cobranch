package rest

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"testing"

	"cobranch.dev/internal/services/code"
)

// files checks that a diff runs from base, with tip as its base's tip, to head, and lists the files want names, each
// as status:path:+additions-deletions, and that its body holds each of raw.
func files(base, tip, head string, want []string, raw ...string) func(t *testing.T, f *orgFixture, body []byte) {
	return func(t *testing.T, _ *orgFixture, body []byte) {
		d := decode[Diff](t, body)
		var got []string
		for _, f := range d.Files {
			got = append(got, string(f.Status)+":"+f.Path+":+"+strconv.Itoa(f.Additions)+"-"+strconv.Itoa(f.Deletions))
		}
		if !slices.Equal(got, want) || text(d.Base) != base || text(d.BaseTip) != tip || d.Head != head {
			t.Errorf("diff = %s, want %v from %q (tip %q) to %q", body, want, base, tip, head)
		}
		for _, r := range raw {
			if !bytes.Contains(body, []byte(r)) {
				t.Errorf("diff = %s, want it to hold %s", body, r)
			}
		}
	}
}

func TestDiffAPI(t *testing.T) {
	runOrgCases(t, []orgCase{
		{name: "a branch's, from its merge base with its base's tip", viewer: olivia, method: "GET", path: fixed("/v1/orgs/acme/repos/website/branches/feature/diff"),
			wantStatus: 200, check: files(shaOf("c"), shaOf("c"), shaOf("d"), []string{"modified:README.md:+1-1", "deleted:docs/deep/notes.txt:+0-1",
				"deleted:docs/guide.md:+0-1", "added:feature.txt:+1-0", "deleted:latest.link:+0-1", "deleted:logo.png:+0-1", "deleted:vendor/lib.module:+0-1"},
				`{"kind":"removed","old":1,"text":"# Website"},{"kind":"added","new":1,"text":"# First"}`, `"mode":"100644","oldMode":"100644"`)},
		{name: "a branch with nothing to merge lists no files", viewer: olivia, method: "GET",
			path: fixed("/v1/orgs/acme/repos/website/branches/feature%2Flogin/diff"), wantStatus: 200,
			check: files(shaOf("c"), shaOf("c"), shaOf("c"), nil, `"files":[]`, `"additions":0`)},
		{name: "main merges into nothing", viewer: olivia, method: "GET", path: fixed("/v1/orgs/acme/repos/website/branches/main/diff"),
			wantStatus: 400, wantCode: "branch.default"},
		{name: "a branch that isn't", viewer: olivia, method: "GET", path: fixed("/v1/orgs/acme/repos/website/branches/gone/diff"),
			wantStatus: 404, wantCode: "branch.not_found"},
		{name: "a commit's, against its first parent", viewer: olivia, method: "GET", path: fixed("/v1/orgs/acme/repos/website/commits/" + shaOf("b") + "/diff"),
			wantStatus: 200, check: files(shaOf("a"), "", shaOf("b"), []string{"modified:README.md:+1-1"})},
		{name: "a root commit's, against the empty tree", viewer: olivia, method: "GET", path: fixed("/v1/orgs/acme/repos/website/commits/" + shaOf("a") + "/diff"),
			wantStatus: 200, check: files("", "", shaOf("a"), []string{"added:README.md:+1-0"}, `"hunks":[{"lines":[{"kind":"added","new":1,"text":"# First"}]`)},
		{name: "a name that isn't a commit's", viewer: olivia, method: "GET", path: fixed("/v1/orgs/acme/repos/website/commits/main/diff"),
			wantStatus: 400, wantCode: "sha.invalid"},
		{name: "a commit the repository doesn't hold", viewer: olivia, method: "GET", path: fixed("/v1/orgs/acme/repos/website/commits/" + shaOf("e") + "/diff"),
			wantStatus: 404, wantCode: "commit.not_found"},
		{name: "a Member who doesn't read it sees no diff", viewer: maxim, method: "GET", path: fixed("/v1/orgs/acme/repos/website/branches/feature/diff"),
			wantStatus: 404, wantCode: "repository.not_found"},
		{name: "a token with repo.read alone reads it", viewer: oliviaToken("repo.read"), method: "GET",
			path: fixed("/v1/orgs/acme/repos/website/commits/" + shaOf("b") + "/diff"), wantStatus: 200},
	})
}

func TestDiffAPIRevalidates(t *testing.T) {
	tests := []struct{ name, path string }{
		{name: "a branch's", path: "/v1/orgs/acme/repos/website/branches/feature/diff"},
		{name: "a commit's", path: "/v1/orgs/acme/repos/website/commits/" + shaOf("b") + "/diff"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newOrgFixture(t)
			first := f.serve(&f.olivia, httptest.NewRequest("GET", tt.path, nil))
			tag := first.Header().Get("ETag")
			if first.Code != 200 || tag == "" {
				t.Fatalf("status %d, ETag %q", first.Code, tag)
			}
			req := httptest.NewRequest("GET", tt.path, nil)
			req.Header.Set("If-None-Match", tag)
			if rec := f.serve(&f.olivia, req); rec.Code != http.StatusNotModified || rec.Header().Get("ETag") != tag || rec.Body.Len() != 0 {
				t.Errorf("revalidating: status %d, ETag %q, body %q", rec.Code, rec.Header().Get("ETag"), rec.Body)
			}
		})
	}
}

func TestDiffAPIMoves(t *testing.T) {
	f := newOrgFixture(t)
	path := "/v1/orgs/acme/repos/website/branches/feature/diff"
	tag := f.serve(&f.olivia, httptest.NewRequest("GET", path, nil)).Header().Get("ETag")
	f.objects.Commit(shaOf("f"), map[string]string{"README.md": "# New\n"}, "main")
	req := httptest.NewRequest("GET", path, nil)
	req.Header.Set("If-None-Match", tag)
	if rec := f.serve(&f.olivia, req); rec.Code != 200 || rec.Header().Get("ETag") == tag {
		t.Errorf("after main moved: status %d, ETag %q", rec.Code, rec.Header().Get("ETag"))
	}
	if want := (code.DiffRequest{Base: "main", Head: shaOf("d"), MergeBase: true}); f.objects.Diffs[0] != want {
		t.Errorf("asked for %+v, want %+v", f.objects.Diffs[0], want)
	}
	f.objects.Fail = code.ErrUnavailable
	if rec := f.serve(&f.olivia, httptest.NewRequest("GET", path, nil)); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("without a Git server: status %d, want 503", rec.Code)
	}
	f.objects.Fail = errors.New("boom")
	if rec := f.serve(&f.olivia, httptest.NewRequest("GET", path, nil)); rec.Code != http.StatusInternalServerError {
		t.Errorf("when Git fails: status %d, want 500", rec.Code)
	}
}

// text is s, or "" for nil.
func text(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
