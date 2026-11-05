package graphql

import (
	"testing"

	"cobranch.dev/internal/services/code"
)

// markedOnFeature readies Olivia's marks on feature: a.go at a, and gone.go, which the branch deletes.
func markedOnFeature(f *orgFixture) {
	f.codeStore.Viewed[f.olivia.User.ID.String()+"/"+f.website.ID.String()+"/feature"] = []code.ViewedFile{
		{Path: "a.go", Object: shaOf("a"), ViewedAt: now}, {Path: "gone.go", ViewedAt: now}}
}

func TestViewedFilesQuery(t *testing.T) {
	viewed := func(branch string) func(*orgFixture) string {
		return fixed(`{ organization(slug: "acme") { repository(slug: "website") { viewedFiles(branch: "` + branch + `") { tag files { path object viewedAt } } } } }`)
	}
	tag := code.ViewedTag([]code.ViewedFile{{Path: "a.go", Object: shaOf("a"), ViewedAt: now}, {Path: "gone.go", ViewedAt: now}})
	runOrgCases(t, []orgCase{
		{name: "the viewer's marks, by path, with their tag", viewer: oliviaAfter(markedOnFeature), document: viewed("feature"),
			want: []string{`"viewedFiles":{"tag":"` + tag + `","files":[{"path":"a.go","object":"` + shaOf("a") + `","viewedAt":"2026-10-30T12:00:00Z"},` +
				`{"path":"gone.go","object":null,"viewedAt":"2026-10-30T12:00:00Z"}]}`}},
		{name: "none yet", viewer: olivia, document: viewed("feature"), want: []string{`"files":[]`}},
		{name: "a branch that doesn't live is null", viewer: olivia, document: viewed("gone"), want: []string{`"viewedFiles":null`}},
		{name: "no one signed in", document: viewed("feature"), wantCode: unauthenticated},
	})
}

func TestUpdateViewedFilesMutation(t *testing.T) {
	update := func(branch, files, expected string) func(*orgFixture) string {
		return func(f *orgFixture) string {
			args := `repositoryId: "` + f.website.ID.String() + `", branch: "` + branch + `", files: ` + files
			if expected != "" {
				args += `, expectedTag: "` + expected + `"`
			}
			return `mutation { updateViewedFiles(` + args + `) { files { path object } } }`
		}
	}
	tag := code.ViewedTag([]code.ViewedFile{{Path: "a.go", Object: shaOf("a"), ViewedAt: now}, {Path: "gone.go", ViewedAt: now}})
	runOrgCases(t, []orgCase{
		{name: "marks, moves and takes off marks", viewer: oliviaAfter(markedOnFeature),
			document: update("feature", `[{path: "b.go", object: "`+shaOf("b")+`"}, {path: "a.go", object: "`+shaOf("c")+`"}, {path: "old.go"}, {path: "gone.go", viewed: false}]`, tag),
			want:     []string{`"files":[{"path":"a.go","object":"` + shaOf("c") + `"},{"path":"b.go","object":"` + shaOf("b") + `"},{"path":"old.go","object":null}]`}},
		{name: "whichever marks are there", viewer: oliviaAfter(markedOnFeature), document: update("feature", `[{path: "a.go", viewed: false}]`, ""),
			want: []string{`"files":[{"path":"gone.go","object":null}]`}},
		{name: "marks that changed since", viewer: oliviaAfter(markedOnFeature), document: update("feature", `[{path: "a.go", viewed: false}]`, code.ViewedTag(nil)),
			wantCode: conflict, wantReason: "precondition.failed"},
		{name: "an object that isn't one's full name", viewer: olivia, document: update("feature", `[{path: "a.go", object: "abc"}]`, ""),
			wantCode: badUserInput, wantReason: "object.invalid"},
		{name: "a path twice", viewer: olivia, document: update("feature", `[{path: "a.go"}, {path: "a.go", viewed: false}]`, ""),
			wantCode: badUserInput, wantReason: "path.repeated"},
		{name: "a branch that doesn't live", viewer: olivia, document: update("gone", `[{path: "a.go"}]`, ""), wantCode: notFound, wantReason: "branch.not_found"},
		{name: "no one signed in", document: update("feature", `[{path: "a.go"}]`, ""), wantCode: unauthenticated},
	})
}
