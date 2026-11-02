package rest

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

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

func TestListReviewsAPIPages(t *testing.T) {
	f := newOrgFixture(t)
	for _, state := range []code.ReviewState{code.ReviewApproved, code.ReviewNeutral, code.ReviewApproved} {
		if _, err := f.codeStore.SubmitReview(context.Background(), f.olivia.User.ID, f.website.ID, shaOf("c"), code.ReviewInput{State: state, Body: "Reviewed"}); err != nil {
			t.Fatal(err)
		}
	}
	first := f.call(&f.olivia, "GET", reviewsPath+"?limit=2", "")
	got := decode[struct{ Reviews []Review }](t, first.Body.Bytes()).Reviews
	link := first.Header().Get("Link")
	if first.Code != 200 || len(got) != 2 || got[0].State != "approved" || got[1].State != "neutral" {
		t.Fatalf("first page: %d %s", first.Code, first.Body)
	}
	want := `<?cursor=` + url.QueryEscape(cursorAfter(f.codeStore.ReviewList[f.website.ID][1].ID)) + `&limit=2>; rel="next"`
	if link != want {
		t.Fatalf("Link = %q, want %q", link, want)
	}
	next := f.call(&f.olivia, "GET", reviewsPath+strings.TrimSuffix(strings.TrimPrefix(link, "<"), `>; rel="next"`), "")
	got = decode[struct{ Reviews []Review }](t, next.Body.Bytes()).Reviews
	if next.Code != 200 || len(got) != 1 || got[0].Id != f.codeStore.ReviewList[f.website.ID][2].ID || next.Header().Get("Link") != "" {
		t.Errorf("last page: %d %s, Link %q", next.Code, next.Body, next.Header().Get("Link"))
	}
	if rec := f.call(&f.olivia, "GET", reviewsPath+"?cursor=nope", ""); rec.Code != 400 || !strings.Contains(rec.Body.String(), "cursor.invalid") {
		t.Errorf("a cursor that isn't one: %d %s", rec.Code, rec.Body)
	}
	if rec := f.call(&f.olivia, "GET", reviewsPath+"?limit=101", ""); rec.Code != 400 {
		t.Errorf("a page too large: %d %s", rec.Code, rec.Body)
	}
}

// cursorAfter is the cursor of the page of reviews after the one named id, as the service writes it.
func cursorAfter(id uuid.UUID) string {
	return base64.RawURLEncoding.EncodeToString(id[:])
}

func TestReviewsAPIRevalidates(t *testing.T) {
	tests := []struct {
		name string
		path func(*orgFixture) string
	}{
		{name: "a commit's reviews", path: fixed(reviewsPath)},
		{name: "a review", path: firstReviewPath},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newOrgFixture(t)
			oliviaReviewed(f)
			path := tt.path(f)
			first := f.call(&f.olivia, "GET", path, "")
			tag := first.Header().Get("ETag")
			if first.Code != 200 || tag == "" {
				t.Fatalf("status %d, ETag %q", first.Code, tag)
			}
			if rec := f.callWith(&f.olivia, "GET", path, "", http.Header{"If-None-Match": {tag}}); rec.Code != http.StatusNotModified ||
				rec.Header().Get("ETag") != tag || rec.Body.Len() != 0 {
				t.Errorf("revalidating: status %d, ETag %q, body %q", rec.Code, rec.Header().Get("ETag"), rec.Body)
			}
			f.codeStore.ReviewList[f.website.ID][0].Reviewer.DisplayName = "Olivia B."
			if rec := f.callWith(&f.olivia, "GET", path, "", http.Header{"If-None-Match": {tag}}); rec.Code != 200 || rec.Header().Get("ETag") == tag {
				t.Errorf("after the reviewer's name changed: status %d, ETag %q", rec.Code, rec.Header().Get("ETag"))
			}
		})
	}
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
