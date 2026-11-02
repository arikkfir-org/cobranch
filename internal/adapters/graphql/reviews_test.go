package graphql

import (
	"context"
	"testing"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/code"
)

// reviewedByOlivia is the viewer of next after Olivia requested changes of main's head, c.
func reviewedByOlivia(next func(*orgFixture) *auth.Authenticated) func(*orgFixture) *auth.Authenticated {
	return func(f *orgFixture) *auth.Authenticated {
		f.codeStore.Names[f.olivia.User.ID] = "Olivia"
		in := code.ReviewInput{State: code.ReviewChangesRequested, Body: "Rename the handler"}
		if _, err := f.codeStore.SubmitReview(context.Background(), f.olivia.User.ID, f.website.ID, shaOf("c"), in); err != nil {
			panic(err)
		}
		return next(f)
	}
}

func TestSubmitReviewMutation(t *testing.T) {
	submit := func(sha, input string) func(*orgFixture) string {
		return func(f *orgFixture) string {
			return `mutation { submitReview(repositoryId: "` + f.website.ID.String() + `", sha: "` + sha + `", input: ` + input +
				`) { sha state body reviewer { displayName } person } }`
		}
	}
	runOrgCases(t, []orgCase{
		{name: "a Developer approves", viewer: maxHolding(auth.RoleDeveloper), document: submit(shaOf("c"), `{state: APPROVED, body: "Looks good"}`),
			want: []string{`"submitReview":{"sha":"` + shaOf("c") + `","state":"APPROVED","body":"Looks good","reviewer":{"displayName":"Max"},"person":true}`}},
		{name: "an approval with its state alone", viewer: maxHolding(auth.RoleDeveloper), document: submit(shaOf("c"), `{state: APPROVED}`),
			want: []string{`"state":"APPROVED","body":""`}},
		{name: "a comment that says nothing", viewer: maxHolding(auth.RoleDeveloper), document: submit(shaOf("c"), `{state: NEUTRAL}`),
			wantCode: badUserInput, wantReason: "body.required"},
		{name: "a Reader may not", viewer: maxHolding(auth.RoleReader), document: submit(shaOf("c"), `{state: APPROVED}`), wantCode: forbidden},
		{name: "a Member who doesn't read it finds nothing", viewer: maxim, document: submit(shaOf("c"), `{state: APPROVED}`),
			wantCode: notFound, wantReason: "commit.not_found"},
		{name: "a commit the repository doesn't hold", viewer: olivia, document: submit(shaOf("e"), `{state: APPROVED}`),
			wantCode: notFound, wantReason: "commit.not_found"},
		{name: "an abbreviated commit", viewer: olivia, document: submit("ccccccc", `{state: APPROVED}`), wantCode: badUserInput, wantReason: "sha.invalid"},
	})
}

func TestCommitReviews(t *testing.T) {
	repo := func(fields string) func(*orgFixture) string {
		return fixed(`{ organization(slug: "acme") { repository(slug: "website") { ` + fields + ` } } }`)
	}
	reviewOf := func(f *orgFixture) string {
		return `{ organization(slug: "acme") { repository(slug: "website") { review(id: "` + f.codeStore.ReviewList[f.website.ID][0].ID.String() +
			`") { state body reviewer { displayName } } } } }`
	}
	runOrgCases(t, []orgCase{
		{name: "a commit's", viewer: reviewedByOlivia(olivia), document: repo(`commit(sha: "` + shaOf("c") + `") { reviews { state body reviewer { displayName } } }`),
			want: []string{`"reviews":[{"state":"CHANGES_REQUESTED","body":"Rename the handler","reviewer":{"displayName":"Olivia"}}]`}},
		{name: "a branch head's", viewer: reviewedByOlivia(olivia), document: repo(`branches { name commit { reviews { state } } }`),
			want: []string{`{"name":"main","commit":{"reviews":[{"state":"CHANGES_REQUESTED"}]}}`, `{"name":"feature","commit":{"reviews":[]}}`}},
		{name: "one by its ID", viewer: reviewedByOlivia(olivia), document: reviewOf,
			want: []string{`"review":{"state":"CHANGES_REQUESTED","body":"Rename the handler","reviewer":{"displayName":"Olivia"}}`}},
		{name: "one that isn't there", viewer: olivia, document: repo(`review(id: "00000000-0000-7000-8000-000000000999") { state }`),
			want: []string{`"review":null`}},
		{name: "an ID that is none", viewer: olivia, document: repo(`review(id: "nope") { state }`), want: []string{`"review":null`}},
	})
}
