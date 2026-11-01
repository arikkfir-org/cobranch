package rest

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/code"
)

// listing checks that a tree lists the entries want names, in order, each as name:kind.
func listing(want ...string) func(t *testing.T, f *orgFixture, body []byte) {
	return func(t *testing.T, _ *orgFixture, body []byte) {
		tree := decode[Tree](t, body)
		var got []string
		for _, e := range tree.Entries {
			got = append(got, e.Name+":"+string(e.Kind))
		}
		if !slices.Equal(got, want) || tree.Commit != shaOf("c") {
			t.Errorf("tree = %s, want entries %v at commit c", body, want)
		}
	}
}

func TestTagsAPI(t *testing.T) {
	runOrgCases(t, []orgCase{
		{name: "the tags, each with its object", viewer: olivia, method: "GET", path: fixed("/v1/orgs/acme/repos/website/tags"), wantStatus: 200,
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				if got := decode[struct{ Tags []Tag }](t, body).Tags; len(got) != 1 || got[0].Name != "v1.0.0" || got[0].Target != shaOf("b") {
					t.Errorf("tags = %s", body)
				}
			}},
		{name: "a Member who doesn't read it sees none", viewer: maxim, method: "GET", path: fixed("/v1/orgs/acme/repos/website/tags"),
			wantStatus: 404, wantCode: "repository.not_found"},
	})
}

func TestTreeAPI(t *testing.T) {
	runOrgCases(t, []orgCase{
		{name: "main's root, directories first", viewer: olivia, method: "GET", path: fixed("/v1/orgs/acme/repos/website/tree"), wantStatus: 200,
			check: listing("docs:tree", "vendor:tree", "README.md:blob", "latest.link:symlink", "logo.png:blob")},
		{name: "a directory", viewer: olivia, method: "GET", path: fixed("/v1/orgs/acme/repos/website/tree?ref=main&path=docs"), wantStatus: 200,
			check: listing("deep:tree", "guide.md:blob")},
		{name: "the root's README, rendered", viewer: olivia, method: "GET", path: fixed("/v1/orgs/acme/repos/website/tree"), wantStatus: 200,
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				r := decode[Tree](t, body).Readme
				if r == nil || r.Path != "README.md" || r.Text == nil || *r.Text != "# Website\n" || r.Html == nil ||
					*r.Html != `<h1 id="user-content-website">Website</h1>`+"\n" {
					t.Errorf("tree = %s, want README.md rendered", body)
				}
			}},
		{name: "a directory without a README", viewer: olivia, method: "GET", path: fixed("/v1/orgs/acme/repos/website/tree?path=docs"), wantStatus: 200,
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				if decode[Tree](t, body).Readme != nil {
					t.Errorf("tree = %s, want no README", body)
				}
			}},
		{name: "a submodule sorts with the directories", viewer: olivia, method: "GET", path: fixed("/v1/orgs/acme/repos/website/tree?path=vendor"), wantStatus: 200,
			check: listing("lib.module:submodule")},
		{name: "at a commit's full name", viewer: olivia, method: "GET", path: fixed("/v1/orgs/acme/repos/website/tree?ref=" + shaOf("c") + "&path=docs/deep"),
			wantStatus: 200, check: listing("notes.txt:blob")},
		{name: "at a tag", viewer: olivia, method: "GET", path: fixed("/v1/orgs/acme/repos/website/tree?ref=v1.0.0"), wantStatus: 200,
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				if tree := decode[Tree](t, body); tree.Commit != shaOf("b") || len(tree.Entries) != 1 {
					t.Errorf("tree = %s, want commit b's", body)
				}
			}},
		{name: "a ref that names nothing", viewer: olivia, method: "GET", path: fixed("/v1/orgs/acme/repos/website/tree?ref=gone"),
			wantStatus: 404, wantCode: "ref.not_found"},
		{name: "a ref Git's revision syntax would read", viewer: olivia, method: "GET", path: fixed("/v1/orgs/acme/repos/website/tree?ref=main~2"),
			wantStatus: 400, wantCode: "ref.invalid"},
		{name: "a path that climbs out", viewer: olivia, method: "GET", path: fixed("/v1/orgs/acme/repos/website/tree?path=docs/../.."),
			wantStatus: 400, wantCode: "path.invalid"},
		{name: "a path that holds nothing", viewer: olivia, method: "GET", path: fixed("/v1/orgs/acme/repos/website/tree?path=nothing"),
			wantStatus: 404, wantCode: "path.not_found"},
		{name: "a path that holds a file", viewer: olivia, method: "GET", path: fixed("/v1/orgs/acme/repos/website/tree?path=README.md"),
			wantStatus: 404, wantCode: "path.file"},
		{name: "a submodule's files are another repository's", viewer: olivia, method: "GET", path: fixed("/v1/orgs/acme/repos/website/tree?path=vendor/lib.module"),
			wantStatus: 404, wantCode: "path.submodule"},
		{name: "a Member who doesn't read it sees no tree", viewer: maxim, method: "GET", path: fixed("/v1/orgs/acme/repos/website/tree"),
			wantStatus: 404, wantCode: "repository.not_found"},
		{name: "a token with repo.read alone reads it", viewer: oliviaToken("repo.read"), method: "GET", path: fixed("/v1/orgs/acme/repos/website/tree"),
			wantStatus: 200},
	})
}

func TestBlobAPI(t *testing.T) {
	file := func(check func(t *testing.T, f File)) func(t *testing.T, _ *orgFixture, body []byte) {
		return func(t *testing.T, _ *orgFixture, body []byte) { check(t, decode[File](t, body)) }
	}
	runOrgCases(t, []orgCase{
		{name: "a text file", viewer: olivia, method: "GET", path: fixed("/v1/orgs/acme/repos/website/blob?path=docs/guide.md"), wantStatus: 200,
			check: file(func(t *testing.T, f File) {
				if f.Name != "guide.md" || f.Kind != FileKindBlob || f.Binary || f.Text == nil || *f.Text != "Read me.\n" || f.Size != 9 || f.Commit != shaOf("c") ||
					f.Html == nil || *f.Html != "<p>Read me.</p>\n" {
					t.Errorf("file = %+v", f)
				}
			})},
		{name: "a binary file has no text", viewer: olivia, method: "GET", path: fixed("/v1/orgs/acme/repos/website/blob?path=logo.png"), wantStatus: 200,
			check: file(func(t *testing.T, f File) {
				if !f.Binary || f.Text != nil || f.Html != nil {
					t.Errorf("file = %+v, want binary without text", f)
				}
			})},
		{name: "a symlink's text is its target", viewer: olivia, method: "GET", path: fixed("/v1/orgs/acme/repos/website/blob?path=latest.link"), wantStatus: 200,
			check: file(func(t *testing.T, f File) {
				if f.Kind != FileKindSymlink || f.Text == nil || *f.Text != "docs/guide.md" {
					t.Errorf("file = %+v", f)
				}
			})},
		{name: "a directory is no file", viewer: olivia, method: "GET", path: fixed("/v1/orgs/acme/repos/website/blob?path=docs"),
			wantStatus: 404, wantCode: "path.directory"},
		{name: "the root is no file", viewer: olivia, method: "GET", path: fixed("/v1/orgs/acme/repos/website/blob?path="),
			wantStatus: 404, wantCode: "path.directory"},
		{name: "a path is required", viewer: olivia, method: "GET", path: fixed("/v1/orgs/acme/repos/website/blob"),
			wantStatus: 400, wantCode: "request.invalid"},
		{name: "a Member who doesn't read it sees no file", viewer: maxim, method: "GET", path: fixed("/v1/orgs/acme/repos/website/blob?path=README.md"),
			wantStatus: 404, wantCode: "repository.not_found"},
	})
}

func TestRawAPI(t *testing.T) {
	tests := []struct {
		name            string
		path            string
		wantStatus      int
		wantType        string
		wantDisposition string
		wantBody        string
	}{
		{name: "text as text", path: "docs/guide.md", wantStatus: 200, wantType: "text/plain; charset=utf-8", wantBody: "Read me.\n"},
		{name: "an image as its type", path: "logo.png", wantStatus: 200, wantType: "image/png", wantBody: "\x89PNG\x00\x01"},
		{name: "text as an attachment when asked", path: "docs/guide.md&download=true", wantStatus: 200, wantType: "text/plain; charset=utf-8",
			wantDisposition: "attachment; filename=guide.md", wantBody: "Read me.\n"},
		{name: "a submodule has no bytes", path: "vendor/lib.module", wantStatus: 404},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newOrgFixture(t)
			rec := f.call(&f.olivia, "GET", "/v1/orgs/acme/repos/website/raw?path="+tt.path, "")
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tt.wantStatus, rec.Body)
			}
			if tt.wantStatus != 200 {
				return
			}
			h := rec.Header()
			if h.Get("Content-Type") != tt.wantType || h.Get("Content-Disposition") != tt.wantDisposition || rec.Body.String() != tt.wantBody {
				t.Errorf("type %q, disposition %q, body %q", h.Get("Content-Type"), h.Get("Content-Disposition"), rec.Body)
			}
			if h.Get("Content-Security-Policy") != rawPolicy || h.Get("X-Content-Type-Options") != "nosniff" ||
				h.Get("Cross-Origin-Resource-Policy") != "same-site" || h.Get("Cache-Control") != "private, no-cache" {
				t.Errorf("headers = %v, want the raw bytes' guards", h)
			}
		})
	}
}

// serve serves req to viewer.
func (f *orgFixture) serve(viewer *auth.Authenticated, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	as(viewer, NewHandler("v1", nil, f.service, f.repos, f.code)).ServeHTTP(rec, req)
	return rec
}

// TestRawAttachment checks that bytes neither text nor an image are an attachment, under their name.
func TestRawAttachment(t *testing.T) {
	f := newOrgFixture(t)
	f.objects.Commit(shaOf("f"), map[string]string{"build/app.bin": "\x00\x01\x02"}, "release")
	rec := f.call(&f.olivia, "GET", "/v1/orgs/acme/repos/website/raw?ref=release&path=build/app.bin", "")
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "application/octet-stream" ||
		rec.Header().Get("Content-Disposition") != "attachment; filename=app.bin" {
		t.Errorf("status %d, headers %v", rec.Code, rec.Header())
	}
}

// TestFilesConditional checks that every read of a file answers 304 for the ETag the caller holds, and that a tree's
// and a file's change with the commit their ref names.
func TestFilesConditional(t *testing.T) {
	for _, path := range []string{"/v1/orgs/acme/repos/website/tree?path=docs", "/v1/orgs/acme/repos/website/blob?path=README.md",
		"/v1/orgs/acme/repos/website/raw?path=README.md"} {
		t.Run(path, func(t *testing.T) {
			f := newOrgFixture(t)
			first := f.call(&f.olivia, "GET", path, "")
			tag := first.Header().Get("ETag")
			if first.Code != 200 || tag == "" {
				t.Fatalf("status %d, ETag %q", first.Code, tag)
			}
			req := httptest.NewRequest("GET", path, nil)
			req.Header.Set("If-None-Match", "W/"+tag)
			if rec := f.serve(&f.olivia, req); rec.Code != http.StatusNotModified || rec.Header().Get("ETag") != tag || rec.Body.Len() != 0 {
				t.Errorf("revalidating: status %d, ETag %q, body %q", rec.Code, rec.Header().Get("ETag"), rec.Body)
			}
			f.objects.Commit(shaOf("f"), map[string]string{"README.md": "# New\n", "docs/guide.md": "Read me.\n"}, "main")
			if rec := f.serve(&f.olivia, req); rec.Code != 200 || rec.Header().Get("ETag") == tag {
				t.Errorf("after main moved: status %d, ETag %q", rec.Code, rec.Header().Get("ETag"))
			}
		})
	}
}

// TestFilesWithoutGitServer checks that an installation without a Git server answers 503 for files, and still lists
// branches.
func TestFilesWithoutGitServer(t *testing.T) {
	f := newOrgFixture(t)
	f.code = code.NewService(f.codeStore, nil, nil, auth.NewAuthorizer(f.store))
	if rec := f.call(&f.olivia, "GET", "/v1/orgs/acme/repos/website/tree", ""); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("tree: status %d, want 503: %s", rec.Code, rec.Body)
	} else {
		checkBody(t, rec, "", "git.unavailable")
	}
	if rec := f.call(&f.olivia, "GET", "/v1/orgs/acme/repos/website/branches", ""); rec.Code != 200 {
		t.Errorf("branches: status %d", rec.Code)
	}
}
