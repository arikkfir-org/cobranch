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
		{name: "main, with what a merge into it needs", viewer: olivia, document: repo(`branch(name: "main") { base status checks { kind } requirements { annotations linearHistory } }`),
			want: []string{`"branch":{"base":null,"status":null,"checks":[],"requirements":{"annotations":["ci/build"],"linearHistory":false}}`}},
		{name: "a branch with its status, checks and facts", viewer: olivia,
			document: repo(`branch(name: "feature") { base status ahead behind checks { kind name status count met } requirements { annotations } }`),
			want: []string{`"branch":{"base":"main","status":"BLOCKED","ahead":1,"behind":2,"checks":[{"kind":"ANNOTATION","name":"ci/build","status":null,"count":null,"met":false},` +
				`{"kind":"UP_TO_DATE","name":null,"status":null,"count":2,"met":false}],"requirements":null}`}},
		{name: "a branch no push has worked out", viewer: olivia, document: repo(`branch(name: "feature/login") { status ahead merge { head } }`),
			want: []string{`"branch":{"status":"PROCESSING","ahead":null,"merge":null}`}},
		{name: "a branch's last merge", viewer: olivia, document: repo(`branch(name: "feature") { merge { head by { displayName } at } }`),
			want: []string{`"merge":{"head":"` + shaOf("a") + `","by":{"displayName":"Olivia"},"at":"2026-11-01T09:00:00Z"}`}},
		{name: "a branch that doesn't live is null", viewer: olivia, document: repo(`branch(name: "gone") { name }`),
			want: []string{`"branch":null`}},
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
		{name: "every parent, newest first", viewer: olivia, document: repo(`history(parents: ALL) { commits { sha } nextCursor }`),
			want: []string{`"commits":[{"sha":"` + shaOf("c") + `"},{"sha":"` + shaOf("b") + `"},{"sha":"` + shaOf("a") + `"}],"nextCursor":null`}},
		{name: "every parent from a tag, a page at a time", viewer: olivia, document: repo(`history(ref: "v1.0.0", parents: ALL, first: 1) { commits { sha } nextCursor }`),
			want: []string{`"commits":[{"sha":"` + shaOf("b") + `"}],"nextCursor":"`}},
		{name: "every parent from a ref that names nothing", viewer: olivia, document: repo(`history(ref: "gone", parents: ALL) { nextCursor }`),
			wantCode: notFound, wantReason: "ref.not_found"},
		{name: "every parent from a cursor of nothing", viewer: olivia, document: repo(`history(parents: ALL, after: "nonsense") { nextCursor }`),
			wantCode: badUserInput, wantReason: "cursor.invalid"},
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

// failingFor is Olivia once the Git server fails every merge with err.
func failingFor(err error) func(*orgFixture) *auth.Authenticated {
	return func(f *orgFixture) *auth.Authenticated {
		f.objects.Fail = err
		return &f.olivia
	}
}

func TestMergeMutation(t *testing.T) {
	merge := func(branch, input string) func(*orgFixture) string {
		return func(f *orgFixture) string {
			args := `repositoryId: "` + f.website.ID.String() + `", branch: "` + branch + `"`
			if input != "" {
				args += ", input: " + input
			}
			return `mutation { mergeBranch(` + args + `) { base head commit } }`
		}
	}
	merged := `"mergeBranch":{"base":"main","head":"` + shaOf("d") + `","commit":"` + shaOf("d") + `"}`
	runOrgCases(t, []orgCase{
		{name: "an Owner merges a Blocked branch past main's requirements", viewer: olivia, document: merge("feature", ""), want: []string{merged}},
		{name: "at the head the viewer saw, by a merge commit", viewer: olivia, document: merge("feature", `{head: "`+shaOf("d")+`", method: MERGE}`),
			want: []string{merged}},
		{name: "a head the branch moved from", viewer: olivia, document: merge("feature", `{head: "`+shaOf("c")+`"}`),
			wantCode: conflict, wantReason: "branch.moved"},
		{name: "a fast-forward of a branch behind main", viewer: olivia, document: merge("feature", `{method: FAST_FORWARD}`),
			wantCode: conflict, wantReason: code.MergeNotFastForward, want: []string{`feature is 2 commits behind main, so it can't fast-forward`}},
		{name: "a Developer may not merge a Blocked branch", viewer: maxHolding(auth.RoleDeveloper), document: merge("feature", ""),
			wantCode: conflict, wantReason: code.MergeNotReady},
		{name: "a Reader may not merge", viewer: maxHolding(auth.RoleReader), document: merge("feature", ""), wantCode: forbidden},
		{name: "main merges into nothing", viewer: olivia, document: merge("main", ""), wantCode: badUserInput, wantReason: "branch.default"},
		{name: "a branch that doesn't live", viewer: olivia, document: merge("gone", ""), wantCode: notFound, wantReason: "branch.not_found"},
		{name: "a Member who doesn't read it finds no branch", viewer: maxim, document: merge("feature", ""),
			wantCode: notFound, wantReason: "branch.not_found"},
		{name: "a merge the Git server won't make", viewer: failingFor(&code.MergeError{Code: code.MergeConflict, Message: "feature and main both change README.md"}),
			document: merge("feature", ""), wantCode: conflict, wantReason: code.MergeConflict, want: []string{`feature and main both change README.md`}},
		{name: "a branch that moved while the Git server merged it", viewer: failingFor(code.ErrBranchMoved), document: merge("feature", ""),
			wantCode: conflict, wantReason: "branch.moved"},
		{name: "an installation without a Git server", viewer: failingFor(code.ErrUnavailable), document: merge("feature", ""),
			wantCode: unavailable, wantReason: "git.unavailable"},
	})
}
