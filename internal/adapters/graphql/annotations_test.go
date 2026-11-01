package graphql

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/code"
)

// annotatedByOlivia is the viewer of next after Olivia set ci/build on main's head, c, Failed.
func annotatedByOlivia(next func(*orgFixture) *auth.Authenticated) func(*orgFixture) *auth.Authenticated {
	return func(f *orgFixture) *auth.Authenticated {
		f.codeStore.Names[f.olivia.User.ID] = "Olivia"
		in := code.AnnotationInput{Name: "ci/build", Status: code.AnnotationFailed, Summary: "2 tests failed"}
		if _, err := f.codeStore.SetAnnotation(context.Background(), f.olivia.User.ID, f.website.ID, shaOf("c"), in); err != nil {
			panic(err)
		}
		return next(f)
	}
}

// maxHolding is Max holding role on website.
func maxHolding(role uuid.UUID) func(*orgFixture) *auth.Authenticated {
	return func(f *orgFixture) *auth.Authenticated {
		f.store.Grant(f.max.User.ID, role, auth.ScopeRepository, f.website.ID)
		f.codeStore.Names[f.max.User.ID] = "Max"
		return &f.max
	}
}

func TestAnnotationMutations(t *testing.T) {
	set := func(name, input string) func(*orgFixture) string {
		return func(f *orgFixture) string {
			return `mutation { setAnnotation(repositoryId: "` + f.website.ID.String() + `", sha: "` + shaOf("c") + `", name: "` + name + `", input: ` + input +
				`) { name status summary link principal { displayName } completedAt } }`
		}
	}
	del := func(f *orgFixture) string {
		return `mutation { deleteAnnotation(repositoryId: "` + f.website.ID.String() + `", sha: "` + shaOf("c") + `", name: "ci/build") }`
	}
	runOrgCases(t, []orgCase{
		{name: "a Developer sets one", viewer: maxHolding(auth.RoleDeveloper), document: set("ci/build", `{status: IN_PROGRESS, summary: "Building"}`),
			want: []string{`"setAnnotation":{"name":"ci/build","status":"IN_PROGRESS","summary":"Building","link":"","principal":{"displayName":"Max"},"completedAt":null}`}},
		{name: "another principal's is refused, naming them", viewer: annotatedByOlivia(maxHolding(auth.RoleDeveloper)),
			document: set("ci/build", `{status: SUCCESSFUL}`), wantCode: conflict, wantReason: "annotation.taken",
			want: []string{`Olivia set annotation ci/build on this commit`}},
		{name: "a Reader may not", viewer: maxHolding(auth.RoleReader), document: set("ci/build", `{status: PENDING}`), wantCode: forbidden},
		{name: "a Member who doesn't read it finds nothing", viewer: maxim, document: set("ci/build", `{status: PENDING}`),
			wantCode: notFound, wantReason: "commit.not_found"},
		{name: "a bad link", viewer: olivia, document: set("ci/build", `{status: FAILED, link: "ci.example.com"}`), wantCode: badUserInput, wantReason: "link.invalid"},
		{name: "its principal deletes it", viewer: annotatedByOlivia(olivia), document: del, want: []string{`"deleteAnnotation":true`}},
		{name: "a Developer may not delete another's", viewer: annotatedByOlivia(maxHolding(auth.RoleDeveloper)), document: del, wantCode: forbidden},
		{name: "a Maintainer deletes another's", viewer: annotatedByOlivia(maxHolding(auth.RoleMaintainer)), document: del, want: []string{`"deleteAnnotation":true`}},
		{name: "one that isn't there", viewer: olivia, document: del, wantCode: notFound, wantReason: "annotation.not_found"},
	})
}

func TestCommitAnnotations(t *testing.T) {
	repo := func(fields string) func(*orgFixture) string {
		return fixed(`{ organization(slug: "acme") { repository(slug: "website") { ` + fields + ` } } }`)
	}
	runOrgCases(t, []orgCase{
		{name: "a commit's", viewer: annotatedByOlivia(olivia), document: repo(`commit(sha: "` + shaOf("c") + `") { annotations { name status principal { displayName } } }`),
			want: []string{`"annotations":[{"name":"ci/build","status":"FAILED","principal":{"displayName":"Olivia"}}]`}},
		{name: "a branch head's", viewer: annotatedByOlivia(olivia), document: repo(`branches { name commit { annotations { name } } }`),
			want: []string{`{"name":"main","commit":{"annotations":[{"name":"ci/build"}]}}`, `{"name":"feature","commit":{"annotations":[]}}`}},
		{name: "history's", viewer: annotatedByOlivia(olivia), document: repo(`history(first: 2) { commits { annotations { status } } }`),
			want: []string{`"commits":[{"annotations":[{"status":"FAILED"}]},{"annotations":[]}]`}},
	})
}
