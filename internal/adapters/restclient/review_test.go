package restclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/reviewer"
)

var reviewedSHA = strings.Repeat("a", 40)

// codeAPI is the REST API's calls that the AI reviewer makes, over repository acme/website in memory.
type codeAPI struct {
	mu        sync.Mutex
	submitted []ReviewInput
	// limited submissions are refused as too many before they are recorded; lost ones are recorded, then answered 502.
	limited, lost int
}

func (a *codeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	repo, q := "/v1/orgs/acme/repos/website/", r.URL.Query()
	switch r.Method + " " + strings.TrimPrefix(r.URL.Path, repo) {
	case "GET branches/feature/diff":
		answer(w, http.StatusOK, Diff{Base: ptr("bbb"), BaseTip: ptr("ccc"), Head: reviewedSHA, Additions: 1, Deletions: 1, Files: []FileDiff{{Status: "renamed",
			Path: "b.go", OldPath: ptr("a.go"), Additions: 1, Deletions: 1, Hunks: []Hunk{{OldStart: 1, OldLines: 1, NewStart: 1, NewLines: 1, Section: "func f()",
				Lines: []DiffLine{{Kind: "removed", Old: ptr(1), Text: "x"}, {Kind: "added", New: ptr(1), Text: "y", NoNewline: ptr(true)}}}}}}})
	case "GET blob":
		if q.Get("ref") != "main" || q.Get("path") != "cobranch-review.yaml" {
			problem(w, http.StatusNotFound, "path.not_found", "No such file.")
			return
		}
		answer(w, http.StatusOK, File{Commit: "ddd", Path: "cobranch-review.yaml", Name: "cobranch-review.yaml", Kind: "blob", Mode: "100644", Sha: "eee",
			Size: 14, Text: ptr("maxDiffLines: 9")})
	case "GET tree":
		answer(w, http.StatusOK, Tree{Commit: "ddd", Path: q.Get("path"), Sha: "fff", Entries: []TreeEntry{{Name: "a.md", Path: q.Get("path") + "/a.md",
			Kind: "blob", Mode: "100644", Sha: "111", Size: ptr(int64(3))}}})
	case "GET commits/" + reviewedSHA + "/reviews":
		a.reviews(w, q.Get("cursor"))
	case "POST commits/" + reviewedSHA + "/reviews":
		a.submit(w, r)
	default:
		problem(w, http.StatusNotFound, "branch.not_found", "No such branch.")
	}
}

// reviews answers the commit's reviews in two pages.
func (a *codeAPI) reviews(w http.ResponseWriter, cursor string) {
	page := []Review{{Id: uuid.New(), Sha: reviewedSHA, State: "approved", Body: "One.", Person: true, SubmittedAt: time.Unix(1, 0).UTC()}}
	if cursor == "" {
		w.Header().Set("Link", `<?cursor=second&limit=100>; rel="next"`)
	} else {
		page[0].Body, page[0].Reviewer = "Two.", &Principal{Id: agentID, DisplayName: "AI reviewer"}
	}
	answer(w, http.StatusOK, map[string]any{"reviews": page})
}

// submit records a review, and answers it; a blank neutral one is refused.
func (a *codeAPI) submit(w http.ResponseWriter, r *http.Request) {
	if a.limited > 0 {
		a.limited--
		problem(w, http.StatusTooManyRequests, "rate.limited", "Too many requests.")
		return
	}
	var in ReviewInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.State == "neutral" && (in.Body == nil || *in.Body == "") {
		problem(w, http.StatusBadRequest, "body.required", "A review that comments says what.")
		return
	}
	a.submitted = append(a.submitted, in)
	if a.lost > 0 {
		a.lost--
		problem(w, http.StatusBadGateway, "gateway.failed", "The answer was lost.")
		return
	}
	answer(w, http.StatusCreated, Review{Id: uuid.New(), Sha: reviewedSHA, State: in.State, Body: *in.Body, Reviewer: &Principal{Id: agentID},
		SubmittedAt: time.Unix(2, 0).UTC()})
}

// codeClient is a client of a, as the token cba_valid.
func codeClient(t *testing.T, a http.Handler) *API {
	t.Helper()
	srv := httptest.NewServer(a)
	t.Cleanup(srv.Close)
	file := filepath.Join(t.TempDir(), "token")
	write(t, file, "cba_valid")
	c, err := newClient(srv.URL, file, &retrier{client: srv.Client(), backoff: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestReviewerReads(t *testing.T) {
	t.Parallel()
	c, ctx := codeClient(t, &codeAPI{}), context.Background()
	d, err := c.BranchDiff(ctx, "acme", "website", "feature")
	want := code.Diff{Base: "bbb", BaseTip: "ccc", Head: reviewedSHA, Additions: 1, Deletions: 1, Files: []code.FileDiff{{Status: code.FileRenamed, Path: "b.go",
		OldPath: "a.go", Additions: 1, Deletions: 1, Hunks: []code.Hunk{{OldStart: 1, OldLines: 1, NewStart: 1, NewLines: 1, Section: "func f()",
			Lines: []code.DiffLine{{Kind: code.LineRemoved, Old: 1, Text: "x"}, {Kind: code.LineAdded, New: 1, Text: "y", NoNewline: true}}}}}}}
	if err != nil || !reflect.DeepEqual(d, want) {
		t.Errorf("BranchDiff() = %+v, %v", d, err)
	}
	if _, err := c.BranchDiff(ctx, "acme", "website", "gone"); !errors.Is(err, reviewer.ErrNotFound) || !strings.Contains(err.Error(), "No such branch.") {
		t.Errorf("BranchDiff(a branch that is gone) = %v", err)
	}
	f, err := c.File(ctx, "acme", "website", "main", "cobranch-review.yaml")
	if err != nil || f.Text != "maxDiffLines: 9" || f.Kind != code.KindBlob || f.Commit != "ddd" {
		t.Errorf("File() = %+v, %v", f, err)
	}
	if _, err := c.File(ctx, "acme", "website", "main", "missing.md"); !errors.Is(err, reviewer.ErrNotFound) {
		t.Errorf("File(a missing file) = %v", err)
	}
	tree, err := c.Tree(ctx, "acme", "website", "main", "docs")
	if err != nil || len(tree.Entries) != 1 || tree.Entries[0] != (code.Entry{Name: "a.md", Path: "docs/a.md", Kind: code.KindBlob, Mode: "100644", SHA: "111", Size: 3}) {
		t.Errorf("Tree() = %+v, %v", tree, err)
	}
	reviews, err := c.Reviews(ctx, "acme", "website", reviewedSHA)
	if err != nil || len(reviews) != 2 || reviews[0].Body != "One." || reviews[0].Reviewer != nil || reviews[1].Reviewer == nil || reviews[1].Reviewer.ID != agentID {
		t.Errorf("Reviews() = %+v, %v", reviews, err)
	}
}

func TestReviewerSubmits(t *testing.T) {
	t.Parallel()
	a := &codeAPI{}
	c, ctx := codeClient(t, a), context.Background()
	in := code.ReviewInput{State: code.ReviewChangesRequested, Body: "Fix it.", Branch: "feature", Comments: []code.ReviewComment{
		{Anchor: code.Anchor{SHA: reviewedSHA, Path: "b.go", StartLine: 1, EndLine: 2}, Body: "Here."}}}
	r, err := c.SubmitReview(ctx, "acme", "website", reviewedSHA, in)
	if err != nil || r.State != code.ReviewChangesRequested || r.Body != "Fix it." || r.Reviewer == nil || r.Reviewer.ID != agentID {
		t.Fatalf("SubmitReview() = %+v, %v", r, err)
	}
	want := ReviewInput{State: "changes_requested", Body: ptr("Fix it."), Branch: ptr("feature"), Comments: &[]ReviewComment{
		{Anchor: ChatAnchor{Sha: reviewedSHA, Path: "b.go", StartLine: 1, EndLine: 2}, Body: "Here."}}}
	if len(a.submitted) != 1 || !reflect.DeepEqual(a.submitted[0], want) {
		t.Errorf("the API was sent %+v", a.submitted)
	}
	if _, err := c.SubmitReview(ctx, "acme", "website", reviewedSHA, code.ReviewInput{State: code.ReviewNeutral}); err == nil ||
		!strings.Contains(err.Error(), "400, body.required: A review that comments says what.") {
		t.Errorf("SubmitReview(a blank neutral review) = %v", err)
	}
}

func TestReviewerSubmitsOnce(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		limited, lost int
		wantErr       string
	}{
		{name: "an answer lost after the review was recorded", lost: 1, wantErr: "502"},
		{name: "too many requests, before it was", limited: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a := &codeAPI{limited: tt.limited, lost: tt.lost}
			_, err := codeClient(t, a).SubmitReview(context.Background(), "acme", "website", reviewedSHA, code.ReviewInput{State: code.ReviewApproved, Body: "Fine."})
			if tt.wantErr == "" && err != nil || tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Errorf("SubmitReview() = %v, want an error with %q", err, tt.wantErr)
			}
			if len(a.submitted) != 1 {
				t.Errorf("the API recorded %d reviews, want 1", len(a.submitted))
			}
		})
	}
}

func TestNextCursor(t *testing.T) {
	tests := []struct{ link, want string }{
		{link: `<?cursor=abc&limit=100>; rel="next"`, want: "abc"},
		{link: `<?cursor=x>; rel="prev", <?cursor=y>; rel="next"`, want: "y"},
		{link: `<?cursor=x>; rel="prev"`},
		{link: ""},
	}
	for _, tt := range tests {
		if got := nextCursor(tt.link); got != tt.want {
			t.Errorf("nextCursor(%q) = %q, want %q", tt.link, got, tt.want)
		}
	}
}
