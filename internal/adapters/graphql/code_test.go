package graphql

import "testing"

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
