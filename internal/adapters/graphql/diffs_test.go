package graphql

import (
	"errors"
	"strings"
	"testing"

	"cobranch.dev/internal/services/code"
)

func TestDiffQueries(t *testing.T) {
	repo := func(fields string) func(*orgFixture) string {
		return fixed(`{ organization(slug: "acme") { repository(slug: "website") { ` + fields + ` } } }`)
	}
	runOrgCases(t, []orgCase{
		{name: "a branch's, from its merge base with its base's tip", viewer: olivia,
			document: repo(`branchDiff(name: "feature") { base baseTip head additions deletions filesLeftOut files { status path oldMode mode } }`),
			want: []string{`"base":"` + shaOf("c") + `","baseTip":"` + shaOf("c") + `","head":"` + shaOf("d") + `","additions":2,"deletions":6,"filesLeftOut":0`,
				`{"status":"MODIFIED","path":"README.md","oldMode":"100644","mode":"100644"},{"status":"DELETED","path":"docs/deep/notes.txt","oldMode":"100644","mode":null}`,
				`{"status":"ADDED","path":"feature.txt","oldMode":null,"mode":"100644"}`}},
		{name: "its hunks and lines", viewer: olivia,
			document: repo(`branchDiff(name: "feature") { files { path hunks { oldStart oldLines newStart newLines section lines { kind old new text noNewline } } } }`),
			want: []string{`{"path":"README.md","hunks":[{"oldStart":1,"oldLines":1,"newStart":1,"newLines":1,"section":"","lines":[` +
				`{"kind":"REMOVED","old":1,"new":null,"text":"# Website","noNewline":false},{"kind":"ADDED","old":null,"new":1,"text":"# First","noNewline":false}]}]}`}},
		{name: "a branch that doesn't live is null", viewer: olivia, document: repo(`branchDiff(name: "gone") { head }`), want: []string{`"branchDiff":null`}},
		{name: "main merges into nothing", viewer: olivia, document: repo(`branchDiff(name: "main") { head }`), wantCode: badUserInput, wantReason: "branch.default"},
		{name: "a commit's, against its first parent", viewer: olivia, document: repo(`commitDiff(sha: "` + shaOf("b") + `") { base baseTip head files { status path } }`),
			want: []string{`"commitDiff":{"base":"` + shaOf("a") + `","baseTip":null,"head":"` + shaOf("b") + `","files":[{"status":"MODIFIED","path":"README.md"}]}`}},
		{name: "a root commit's, against the empty tree", viewer: olivia, document: repo(`commitDiff(sha: "` + shaOf("a") + `") { base files { status path } }`),
			want: []string{`"commitDiff":{"base":null,"files":[{"status":"ADDED","path":"README.md"}]}`}},
		{name: "a commit the repository doesn't hold is null", viewer: olivia, document: repo(`commitDiff(sha: "` + shaOf("e") + `") { head }`),
			want: []string{`"commitDiff":null`}},
		{name: "an abbreviated name", viewer: olivia, document: repo(`commitDiff(sha: "bbb") { head }`), wantCode: badUserInput, wantReason: "sha.invalid"},
	})
}

func TestDiffQueriesWithoutGit(t *testing.T) {
	tests := []struct {
		name     string
		fail     error
		wantCode string
	}{
		{name: "without a Git server", fail: code.ErrUnavailable, wantCode: unavailable},
		{name: "when Git fails", fail: errors.New("boom"), wantCode: internal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newOrgFixture()
			f.objects.Fail = tt.fail
			body := f.query(&f.olivia, `{ organization(slug: "acme") { repository(slug: "website") { branchDiff(name: "feature") { head } } } }`)
			if !strings.Contains(body, `"code":"`+tt.wantCode+`"`) {
				t.Errorf("answer = %s, want an error coded %s", body, tt.wantCode)
			}
		})
	}
}
