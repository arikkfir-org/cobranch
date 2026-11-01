package graphql

import (
	"strings"
	"testing"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/code"
)

func TestCodeQueries(t *testing.T) {
	repo := func(fields string) func(*orgFixture) string {
		return fixed(`{ organization(slug: "acme") { repository(slug: "website") { ` + fields + ` } } }`)
	}
	runOrgCases(t, []orgCase{
		{name: "the branches, main first", viewer: olivia, document: repo(`branches { name head commit { sha message } }`),
			want: []string{`"branches":[{"name":"main","head":"` + shaOf("c") + `","commit":{"sha":"` + shaOf("c") + `","message":"Commit c\n"}},{"name":"feature"`}},
		{name: "a page of main's history and where the next starts", viewer: olivia, document: repo(`history(first: 2) { commits { sha push { id } } nextCursor }`),
			want: []string{`"commits":[{"sha":"` + shaOf("c") + `","push":null},{"sha":"` + shaOf("b") + `","push":null}],"nextCursor":"` + shaOf("a") + `"`}},
		{name: "the last page", viewer: olivia, document: repo(`history(first: 2, after: "` + shaOf("a") + `") { commits { sha } nextCursor }`),
			want: []string{`"commits":[{"sha":"` + shaOf("a") + `"}],"nextCursor":null`}},
		{name: "another branch's", viewer: olivia, document: repo(`history(branch: "feature") { commits { parents } }`),
			want: []string{`"commits":[{"parents":["` + shaOf("a") + `"]},{"parents":[]}]`}},
		{name: "a branch that doesn't live", viewer: olivia, document: repo(`history(branch: "gone") { nextCursor }`),
			wantCode: notFound, wantReason: "branch.not_found"},
		{name: "a page too long", viewer: olivia, document: repo(`history(first: 101) { nextCursor }`),
			wantCode: badUserInput, wantReason: "limit.invalid"},
		{name: "a commit with its push and principals", viewer: olivia,
			document: repo(`commit(sha: "` + shaOf("c") + `") { sha author { name principal { displayName } } push { transport pusher { displayName } } }`),
			want:     []string{`"author":{"name":"Pusher","principal":{"displayName":"Olivia"}}`, `"push":{"transport":"https","pusher":{"displayName":"Olivia"}}`}},
		{name: "a commit the repository doesn't hold is null", viewer: olivia, document: repo(`commit(sha: "` + shaOf("e") + `") { sha }`),
			want: []string{`"commit":null`}},
		{name: "an abbreviated name", viewer: olivia, document: repo(`commit(sha: "ccc") { sha }`),
			wantCode: badUserInput, wantReason: "sha.invalid"},
	})
}

func TestFileQueries(t *testing.T) {
	repo := func(fields string) func(*orgFixture) string {
		return fixed(`{ organization(slug: "acme") { repository(slug: "website") { ` + fields + ` } } }`)
	}
	runOrgCases(t, []orgCase{
		{name: "the tags", viewer: olivia, document: repo(`tags { name target }`),
			want: []string{`"tags":[{"name":"v1.0.0","target":"` + shaOf("b") + `"}]`}},
		{name: "main's root, directories first", viewer: olivia, document: repo(`tree { commit path entries { name kind size } }`),
			want: []string{`"commit":"` + shaOf("c") + `","path":"","entries":[{"name":"docs","kind":"TREE","size":null},{"name":"vendor","kind":"TREE","size":null},` +
				`{"name":"README.md","kind":"BLOB","size":10},{"name":"latest.link","kind":"SYMLINK","size":13},{"name":"logo.png","kind":"BLOB","size":6}]`}},
		{name: "a directory at a tag", viewer: olivia, document: repo(`tree(ref: "v1.0.0", path: "") { commit entries { path } }`),
			want: []string{`"commit":"` + shaOf("b") + `","entries":[{"path":"README.md"}]`}},
		{name: "a ref that names nothing", viewer: olivia, document: repo(`tree(ref: "gone") { sha }`), wantCode: notFound, wantReason: "ref.not_found"},
		{name: "a file's path", viewer: olivia, document: repo(`tree(path: "README.md") { sha }`), wantCode: notFound, wantReason: "path.file"},
		{name: "a path that climbs out", viewer: olivia, document: repo(`tree(path: "..") { sha }`), wantCode: badUserInput, wantReason: "path.invalid"},
		{name: "a text file", viewer: olivia, document: repo(`blob(path: "docs/guide.md") { name kind size binary truncated text }`),
			want: []string{`"blob":{"name":"guide.md","kind":"BLOB","size":9,"binary":false,"truncated":false,"text":"Read me.\n"}`}},
		{name: "a binary file has no text", viewer: olivia, document: repo(`blob(path: "logo.png") { binary text }`),
			want: []string{`"blob":{"binary":true,"text":null}`}},
		{name: "a Markdown file, rendered", viewer: olivia, document: repo(`blob(path: "docs/guide.md") { html }`),
			want: []string{`"blob":{"html":"\u003cp\u003eRead me.\u003c/p\u003e\n"}`}},
		{name: "a file that isn't Markdown has no HTML", viewer: olivia, document: repo(`blob(path: "logo.png") { html }`),
			want: []string{`"blob":{"html":null}`}},
		{name: "the root's README", viewer: olivia, document: repo(`tree { readme { path text html } }`),
			want: []string{`"readme":{"path":"README.md","text":"# Website\n","html":"\u003ch1 id=\"user-content-website\"\u003eWebsite\u003c/h1\u003e\n"}`}},
		{name: "a directory without one", viewer: olivia, document: repo(`tree(path: "docs") { readme { path } }`),
			want: []string{`"tree":{"readme":null}`}},
		{name: "a directory is no file", viewer: olivia, document: repo(`blob(path: "docs") { sha }`), wantCode: notFound, wantReason: "path.directory"},
		{name: "a submodule is no file", viewer: olivia, document: repo(`blob(path: "vendor/lib.module") { sha }`), wantCode: notFound, wantReason: "path.submodule"},
	})
}

// TestFilesWithoutGitServer checks that an installation without a Git server answers UNAVAILABLE for a tree.
func TestFilesWithoutGitServer(t *testing.T) {
	f := newOrgFixture()
	f.code = code.NewService(f.codeStore, nil, nil, auth.NewAuthorizer(f.store))
	body := f.query(&f.olivia, `{ organization(slug: "acme") { repository(slug: "website") { tree { sha } } } }`)
	if !strings.Contains(body, `"code":"UNAVAILABLE"`) || !strings.Contains(body, `"reason":"git.unavailable"`) {
		t.Errorf("body = %s, want UNAVAILABLE", body)
	}
}
