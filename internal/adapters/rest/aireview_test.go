package rest

import (
	"net/http"
	"testing"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/git"
)

// reviewerOff is viewer with the organization's AI reviewer unable to review, for err.
func reviewerOff(err error, viewer func(f *orgFixture) *auth.Authenticated) func(f *orgFixture) *auth.Authenticated {
	return func(f *orgFixture) *auth.Authenticated {
		f.codeStore.ReviewerOff = err
		return viewer(f)
	}
}

func TestRequestReviewAPI(t *testing.T) {
	const feature = "/v1/orgs/acme/repos/website/branches/feature/review"
	queued := func(t *testing.T, f *orgFixture, body []byte) {
		r := decode[AIReview](t, body)
		if r.Task != 1 || r.State != AIReviewStateQueued || r.Reason != nil {
			t.Errorf("review = %s", body)
		}
		if b := f.codeStore.BranchList[f.website.ID]; b[1].Name != "feature" || b[1].AIReview == nil || b[1].AIReview.Task != 1 {
			t.Errorf("branch feature's review = %+v", b[1].AIReview)
		}
	}
	runOrgCases(t, []orgCase{
		{name: "a Developer asks for another review", viewer: maxDeveloping, method: http.MethodPost, path: fixed(feature), wantStatus: 202,
			wantHeader: http.Header{"Location": {"/v1/orgs/acme/tasks/1"}}, check: queued},
		{name: "a branch whose name holds a slash", viewer: maxDeveloping, method: http.MethodPost,
			path: fixed("/v1/orgs/acme/repos/website/branches/feature%2Flogin/review"), wantStatus: 202},
		{name: "a Reader may not", viewer: maxReading, method: http.MethodPost, path: fixed(feature), wantStatus: 403, wantCode: "auth.forbidden"},
		{name: "a Member who doesn't read it finds nothing", viewer: maxim, method: http.MethodPost, path: fixed(feature), wantStatus: 404,
			wantCode: "repository.not_found"},
		{name: "a branch the repository doesn't have", viewer: olivia, method: http.MethodPost, path: fixed("/v1/orgs/acme/repos/website/branches/gone/review"),
			wantStatus: 404, wantCode: "branch.not_found"},
		{name: "main's pushes are never reviewed", viewer: olivia, method: http.MethodPost, path: fixed("/v1/orgs/acme/repos/website/branches/main/review"),
			wantStatus: 409, wantCode: "review.off"},
		{name: "a reviewer without a model", viewer: reviewerOff(code.ErrReviewerNoModel, olivia), method: http.MethodPost, path: fixed(feature),
			wantStatus: 409, wantCode: "reviewer.no_model"},
		{name: "a suspended reviewer", viewer: reviewerOff(code.ErrReviewerSuspended, olivia), method: http.MethodPost, path: fixed(feature),
			wantStatus: 409, wantCode: "reviewer.suspended"},
	})
}

func TestBranchAIReviewAPI(t *testing.T) {
	failed := func(f *orgFixture) *auth.Authenticated {
		f.codeStore.UpdateBranch(f.website.ID, "feature", func(b *code.Branch) {
			b.AIReview = &code.AIReview{Task: 7, State: code.ReviewFailed, Reason: "The model's provider refused the credentials."}
		})
		return &f.olivia
	}
	runOrgCases(t, []orgCase{
		{name: "a branch shows its AI review", viewer: failed, method: http.MethodGet, path: fixed("/v1/orgs/acme/repos/website/branches/feature"),
			wantStatus: 200, check: func(t *testing.T, _ *orgFixture, body []byte) {
				b := decode[Branch](t, body)
				if r := b.AiReview; r == nil || r.Task != 7 || r.State != AIReviewStateFailed || r.Reason == nil ||
					*r.Reason != "The model's provider refused the credentials." {
					t.Errorf("branch = %s", body)
				}
			}},
		{name: "a branch without one leaves it out", viewer: olivia, method: http.MethodGet, path: fixed("/v1/orgs/acme/repos/website/branches/feature"),
			wantStatus: 200, check: func(t *testing.T, _ *orgFixture, body []byte) {
				if b := decode[Branch](t, body); b.AiReview != nil {
					t.Errorf("branch = %s", body)
				}
			}},
	})
}

func TestBranchAICheckAPI(t *testing.T) {
	failed := func(f *orgFixture) *auth.Authenticated {
		f.codeStore.UpdateBranch(f.website.ID, "main", func(b *code.Branch) {
			b.Requirements = &git.Requirements{Branch: "main", Reviews: &git.ReviewRequirements{Approvals: 1, AI: true}}
		})
		f.codeStore.UpdateBranch(f.website.ID, "feature", func(b *code.Branch) {
			b.Checks = []code.Check{{Kind: code.CheckAI, AI: code.AIFailed, Reason: "The model's provider refused the credentials."},
				{Kind: code.CheckUpToDate}}
		})
		return &f.olivia
	}
	runOrgCases(t, []orgCase{
		{name: "the AI reviewer's check and why it failed", viewer: failed, method: http.MethodGet,
			path: fixed("/v1/orgs/acme/repos/website/branches/feature"), wantStatus: 200, check: func(t *testing.T, _ *orgFixture, body []byte) {
				c := decode[Branch](t, body).Checks
				if len(c) != 2 || c[0].Kind != BranchCheckKindAi || c[0].Ai == nil || *c[0].Ai != AICheckStateFailed ||
					c[0].Reason == nil || *c[0].Reason != "The model's provider refused the credentials." || c[0].Count != nil ||
					c[1].Ai != nil || c[1].Reason != nil {
					t.Errorf("checks = %s", body)
				}
			}},
		{name: "main requires the AI reviewer's approval", viewer: failed, method: http.MethodGet,
			path: fixed("/v1/orgs/acme/repos/website/branches/main"), wantStatus: 200, check: func(t *testing.T, _ *orgFixture, body []byte) {
				if r := decode[Branch](t, body).Requirements; r == nil || r.Reviews == nil || !r.Reviews.Ai || r.Reviews.Approvals != 1 {
					t.Errorf("requirements = %s", body)
				}
			}},
	})
}
