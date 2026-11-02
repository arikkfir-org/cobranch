package rest

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/code"
)

const reviewsPath = "/v1/orgs/acme/repos/website/commits/cccccccccccccccccccccccccccccccccccccccc/reviews"

// oliviaReviewed is Olivia, who requested changes of main's head, c.
func oliviaReviewed(f *orgFixture) *auth.Authenticated {
	f.codeStore.Names[f.olivia.User.ID] = "Olivia"
	if _, err := f.codeStore.SubmitReview(context.Background(), f.olivia.User.ID, f.website.ID, shaOf("c"),
		code.ReviewInput{State: code.ReviewChangesRequested, Body: "Rename the handler"}); err != nil {
		panic(err)
	}
	return &f.olivia
}

// firstReviewPath is the path of the first review of website.
func firstReviewPath(f *orgFixture) string {
	return "/v1/orgs/acme/repos/website/reviews/" + f.codeStore.ReviewList[f.website.ID][0].ID.String()
}

func TestSubmitReviewAPI(t *testing.T) {
	runOrgCases(t, []orgCase{
		{name: "a Developer approves", viewer: maxDeveloping, method: http.MethodPost, path: fixed(reviewsPath), body: `{"state":"approved","body":"Looks good"}`,
			wantStatus: 201, check: func(t *testing.T, f *orgFixture, body []byte) {
				r := decode[Review](t, body)
				if r.State != "approved" || r.Body != "Looks good" || r.Sha != shaOf("c") || r.Reviewer == nil || r.Reviewer.Id != f.max.User.ID ||
					r.Reviewer.DisplayName != "Max" || !r.Person {
					t.Errorf("review = %s", body)
				}
			}},
		{name: "an approval with its state alone", viewer: maxDeveloping, method: http.MethodPost, path: fixed(reviewsPath), body: `{"state":"approved"}`, wantStatus: 201,
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				if r := decode[Review](t, body); r.State != "approved" || r.Body != "" {
					t.Errorf("review = %s", body)
				}
			}},
		{name: "changes requested with nothing said", viewer: maxDeveloping, method: http.MethodPost, path: fixed(reviewsPath), body: `{"state":"changes_requested"}`,
			wantStatus: 400, wantCode: "body.required"},
		{name: "an unknown state", viewer: maxDeveloping, method: http.MethodPost, path: fixed(reviewsPath), body: `{"state":"lgtm"}`, wantStatus: 400,
			wantCode: "state.invalid"},
		{name: "a body too long", viewer: maxDeveloping, method: http.MethodPost, path: fixed(reviewsPath),
			body: `{"state":"neutral","body":"` + strings.Repeat("a", code.MaxReviewBody+1) + `"}`, wantStatus: 400, wantCode: "body.invalid"},
		{name: "a Reader may not", viewer: maxReading, method: http.MethodPost, path: fixed(reviewsPath), body: `{"state":"approved"}`,
			wantStatus: 403, wantCode: "auth.forbidden"},
		{name: "a token with repo.read alone may not", viewer: oliviaToken("repo.read"), method: http.MethodPost, path: fixed(reviewsPath), body: `{"state":"approved"}`,
			wantStatus: 403, wantCode: "auth.forbidden"},
		{name: "a Member who doesn't read it finds nothing", viewer: maxim, method: http.MethodPost, path: fixed(reviewsPath), body: `{"state":"approved"}`,
			wantStatus: 404, wantCode: "repository.not_found"},
		{name: "a commit the repository doesn't hold", viewer: olivia, method: http.MethodPost, body: `{"state":"approved"}`,
			path: fixed("/v1/orgs/acme/repos/website/commits/" + shaOf("e") + "/reviews"), wantStatus: 404, wantCode: "commit.not_found"},
	})
}

func TestSubmitReviewLocation(t *testing.T) {
	f := newOrgFixture(t)
	rec := f.callWith(maxDeveloping(f), http.MethodPost, reviewsPath, `{"state":"approved"}`, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body)
	}
	want := "../../reviews/" + f.codeStore.ReviewList[f.website.ID][0].ID.String()
	if got := rec.Header().Get("Location"); got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}
	base, err := url.Parse("https://cobranch.example" + reviewsPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := base.ResolveReference(&url.URL{Path: want}).Path; got != firstReviewPath(f) {
		t.Errorf("Location resolves to %s, want %s", got, firstReviewPath(f))
	}
}

func TestListReviewsAPI(t *testing.T) {
	runOrgCases(t, []orgCase{
		{name: "a Reader lists them", viewer: then(oliviaReviewed, maxReading), method: http.MethodGet, path: fixed(reviewsPath), wantStatus: 200,
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				got := decode[struct{ Reviews []Review }](t, body).Reviews
				if len(got) != 1 || got[0].State != "changes_requested" || got[0].Body != "Rename the handler" {
					t.Errorf("reviews = %s", body)
				}
			}},
		{name: "none yet", viewer: olivia, method: http.MethodGet, path: fixed(reviewsPath), wantStatus: 200,
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				if string(body) != `{"reviews":[]}`+"\n" {
					t.Errorf("reviews = %s", body)
				}
			}},
		{name: "a commit the repository doesn't hold", viewer: olivia, method: http.MethodGet, path: fixed("/v1/orgs/acme/repos/website/commits/" + shaOf("e") + "/reviews"),
			wantStatus: 404, wantCode: "commit.not_found"},
		{name: "a Member who doesn't read it", viewer: maxim, method: http.MethodGet, path: fixed(reviewsPath), wantStatus: 404, wantCode: "repository.not_found"},
	})
}

func TestGetReviewAPI(t *testing.T) {
	runOrgCases(t, []orgCase{
		{name: "a Reader reads it", viewer: then(oliviaReviewed, maxReading), method: http.MethodGet, path: firstReviewPath, wantStatus: 200,
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				if r := decode[Review](t, body); r.State != "changes_requested" || r.Reviewer == nil || r.Reviewer.DisplayName != "Olivia" {
					t.Errorf("review = %s", body)
				}
			}},
		{name: "one the repository doesn't hold", viewer: olivia, method: http.MethodGet,
			path: fixed("/v1/orgs/acme/repos/website/reviews/00000000-0000-7000-8000-000000000999"), wantStatus: 404, wantCode: "review.not_found"},
		{name: "a Member who doesn't read it", viewer: then(oliviaReviewed, maxim), method: http.MethodGet, path: firstReviewPath, wantStatus: 404,
			wantCode: "repository.not_found"},
	})
}

func TestCommitsCarryReviews(t *testing.T) {
	runOrgCases(t, []orgCase{
		{name: "a commit with its reviews", viewer: oliviaReviewed, method: http.MethodGet, path: fixed("/v1/orgs/acme/repos/website/commits/" + shaOf("c")),
			wantStatus: 200, check: func(t *testing.T, _ *orgFixture, body []byte) {
				if c := decode[Commit](t, body); len(c.Reviews) != 1 || c.Reviews[0].State != "changes_requested" {
					t.Errorf("commit = %s", body)
				}
			}},
		{name: "a commit without any lists none", viewer: olivia, method: http.MethodGet, path: fixed("/v1/orgs/acme/repos/website/commits/" + shaOf("b")),
			wantStatus: 200, check: func(t *testing.T, _ *orgFixture, body []byte) {
				if c := decode[Commit](t, body); c.Reviews == nil || len(c.Reviews) != 0 {
					t.Errorf("commit = %s", body)
				}
			}},
	})
}
