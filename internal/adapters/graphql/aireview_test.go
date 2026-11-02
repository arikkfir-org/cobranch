package graphql

import (
	"testing"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/code"
)

// reviewerOff is viewer with the organization's AI reviewer unable to review, for err.
func reviewerOff(err error, viewer func(f *orgFixture) *auth.Authenticated) func(f *orgFixture) *auth.Authenticated {
	return func(f *orgFixture) *auth.Authenticated {
		f.codeStore.ReviewerOff = err
		return viewer(f)
	}
}

func TestRequestReviewMutation(t *testing.T) {
	request := func(branch string) func(*orgFixture) string {
		return func(f *orgFixture) string {
			return `mutation { requestReview(repositoryId: "` + f.website.ID.String() + `", branch: "` + branch + `") { task state reason } }`
		}
	}
	runOrgCases(t, []orgCase{
		{name: "a Developer asks for another review", viewer: maxHolding(auth.RoleDeveloper), document: request("feature"),
			want: []string{`"requestReview":{"task":1,"state":"QUEUED","reason":null}`}},
		{name: "a branch whose name holds a slash", viewer: olivia, document: request("feature/login"), want: []string{`"state":"QUEUED"`}},
		{name: "a Reader may not", viewer: maxHolding(auth.RoleReader), document: request("feature"), wantCode: forbidden},
		{name: "a branch the repository doesn't have", viewer: olivia, document: request("gone"), wantCode: notFound, wantReason: "branch.not_found"},
		{name: "main's pushes are never reviewed", viewer: olivia, document: request("main"), wantCode: conflict, wantReason: "review.off"},
		{name: "a reviewer without a model", viewer: reviewerOff(code.ErrReviewerNoModel, olivia), document: request("feature"),
			wantCode: conflict, wantReason: "reviewer.no_model"},
	})
}

func TestBranchAIReview(t *testing.T) {
	failed := func(f *orgFixture) *auth.Authenticated {
		f.codeStore.UpdateBranch(f.website.ID, "feature", func(b *code.Branch) {
			b.AIReview = &code.AIReview{Task: 7, State: code.ReviewWaiting, Reason: "Pushed from outside the organization."}
		})
		return &f.olivia
	}
	document := fixed(`{ organization(slug: "acme") { repository(slug: "website") { branch(name: "feature") { aiReview { task state reason } } } } }`)
	runOrgCases(t, []orgCase{
		{name: "a branch shows its AI review", viewer: failed, document: document,
			want: []string{`"aiReview":{"task":7,"state":"WAITING","reason":"Pushed from outside the organization."}`}},
		{name: "a branch without one", viewer: olivia, document: document, want: []string{`"aiReview":null`}},
	})
}
