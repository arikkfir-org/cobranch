package code_test

import (
	"cmp"
	"encoding/base64"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/code/codetest"
	"cobranch.dev/internal/services/orgs"
	"cobranch.dev/internal/services/repos"
)

func TestSubmitReview(t *testing.T) {
	approve := code.ReviewInput{State: code.ReviewApproved, Body: "Looks good"}
	tests := []struct {
		name       string
		before     func(*annotators)
		viewer     func(*annotators) code.Viewer
		sha        string
		in         code.ReviewInput
		wantErr    error
		wantPerson bool
	}{
		{name: "a Developer approves", viewer: func(f *annotators) code.Viewer { return f.dev }, in: approve, wantPerson: true},
		{name: "a Maintainer requests changes", viewer: func(f *annotators) code.Viewer { return f.mia },
			in: code.ReviewInput{State: code.ReviewChangesRequested, Body: "Rename the handler"}, wantPerson: true},
		{name: "Automation comments, as no person", viewer: func(f *annotators) code.Viewer { return f.ci },
			before: func(f *annotators) { f.store.NotPeople[f.ci.ID] = true }, in: code.ReviewInput{State: code.ReviewNeutral, Body: "Ran the linters"}},
		{name: "an approval that says nothing", viewer: func(f *annotators) code.Viewer { return f.dev }, in: code.ReviewInput{State: code.ReviewApproved},
			wantPerson: true},
		{name: "changes requested that say nothing", viewer: func(f *annotators) code.Viewer { return f.dev },
			in: code.ReviewInput{State: code.ReviewChangesRequested}, wantErr: invalid("body.required")},
		{name: "a comment of blanks", viewer: func(f *annotators) code.Viewer { return f.dev }, in: code.ReviewInput{State: code.ReviewNeutral, Body: " \n\t"},
			wantErr: invalid("body.required")},
		{name: "a body of 65,536 characters", viewer: func(f *annotators) code.Viewer { return f.dev },
			in: code.ReviewInput{State: code.ReviewNeutral, Body: strings.Repeat("é", code.MaxReviewBody)}, wantPerson: true},
		{name: "a body of 65,537 characters", viewer: func(f *annotators) code.Viewer { return f.dev },
			in: code.ReviewInput{State: code.ReviewNeutral, Body: strings.Repeat("é", code.MaxReviewBody+1)}, wantErr: invalid("body.invalid")},
		{name: "an unknown state", viewer: func(f *annotators) code.Viewer { return f.dev }, in: code.ReviewInput{State: "lgtm"}, wantErr: invalid("state.invalid")},
		{name: "a Reader may not", viewer: func(f *annotators) code.Viewer { return f.rae }, in: approve, wantErr: auth.ErrForbidden},
		{name: "a viewer who doesn't read the repository finds nothing", viewer: func(f *annotators) code.Viewer { return f.nia }, in: approve,
			wantErr: repos.ErrNotFound},
		{name: "a commit the repository doesn't hold", viewer: func(f *annotators) code.Viewer { return f.dev }, sha: sha(9), in: approve,
			wantErr: repos.ErrNotFound},
		{name: "an abbreviated commit", viewer: func(f *annotators) code.Viewer { return f.dev }, sha: "0000003", in: approve, wantErr: invalid("sha.invalid")},
		{name: "the store's failure", viewer: func(f *annotators) code.Viewer { return f.dev }, before: func(f *annotators) { f.store.Fail = errors.New("down") },
			in: approve, wantErr: errors.New("down")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newAnnotators()
			if tt.before != nil {
				tt.before(f)
			}
			at := cmp.Or(tt.sha, sha(3))
			v := tt.viewer(f)
			got, err := f.service.SubmitReview(t.Context(), v, f.repo, at, tt.in)
			if !sameError(err, tt.wantErr) {
				t.Fatalf("SubmitReview() error = %v, want %v", err, tt.wantErr)
			}
			if err == nil {
				checkSubmitted(t, f, v, at, tt.in, tt.wantPerson, got)
			}
		})
	}
}

func TestSubmitReviewComments(t *testing.T) {
	on := func(sha string, start, end int, body string) code.ReviewComment {
		return code.ReviewComment{Anchor: code.Anchor{SHA: sha, Path: "cmd/main.go", StartLine: start, EndLine: end}, Body: body}
	}
	review := func(state code.ReviewState, branch string, comments ...code.ReviewComment) code.ReviewInput {
		return code.ReviewInput{State: state, Branch: branch, Comments: comments}
	}
	many := make([]code.ReviewComment, code.MaxReviewComments+1)
	for i := range many {
		many[i] = on(sha(3), 1, 1, "Here")
	}
	tests := []struct {
		name    string
		viewer  func(*annotators) code.Viewer
		in      code.ReviewInput
		wantErr error
	}{
		{name: "changes requested in comments alone", in: review(code.ReviewChangesRequested, "feature", on(sha(3), 3, 4, "Rename this"), on(sha(2), 7, 7, "Why go?"))},
		{name: "a comment beside a body", in: code.ReviewInput{State: code.ReviewNeutral, Body: "Two notes", Branch: "feature",
			Comments: []code.ReviewComment{on(sha(3), 1, 1, "First")}}},
		{name: "Automation comments", viewer: func(f *annotators) code.Viewer { return f.ci }, in: review(code.ReviewApproved, "feature", on(sha(3), 1, 2, "Fine"))},
		{name: "as many comments as a review carries", in: review(code.ReviewNeutral, "feature", many[1:]...)},
		{name: "one comment too many", in: review(code.ReviewNeutral, "feature", many...), wantErr: invalid("comments.too_many")},
		{name: "comments without a branch", in: review(code.ReviewNeutral, "", on(sha(3), 1, 1, "Here")), wantErr: invalid("branch.required")},
		{name: "a branch the repository doesn't have", in: review(code.ReviewNeutral, "gone", on(sha(3), 1, 1, "Here")), wantErr: invalid("branch.unknown")},
		{name: "a commit the repository doesn't hold", in: review(code.ReviewNeutral, "feature", on(sha(3), 1, 1, "Here"), on(sha(9), 1, 1, "There")),
			wantErr: invalid("anchor.commit_not_found")},
		{name: "an anchor before the first line", in: review(code.ReviewNeutral, "feature", on(sha(3), 0, 1, "Here")), wantErr: invalid("anchor.invalid")},
		{name: "an anchor on an abbreviated commit", in: review(code.ReviewNeutral, "feature", on("0000003", 1, 1, "Here")), wantErr: invalid("anchor.invalid")},
		{name: "a comment of blanks", in: review(code.ReviewNeutral, "feature", on(sha(3), 1, 1, " \n")), wantErr: invalid("body.empty")},
		{name: "a comment of 20,001 characters", in: review(code.ReviewNeutral, "feature", on(sha(3), 1, 1, strings.Repeat("é", code.MaxMessageBody+1))),
			wantErr: invalid("body.too_long")},
		{name: "a reviewer who may not post to the chat", viewer: func(f *annotators) code.Viewer { return f.nia },
			in: review(code.ReviewNeutral, "feature", on(sha(3), 1, 1, "Here")), wantErr: auth.ErrForbidden},
		{name: "a reviewer who may not post to the chat approves without comments", viewer: func(f *annotators) code.Viewer { return f.nia },
			in: code.ReviewInput{State: code.ReviewApproved}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newAnnotators()
			f.store.BranchList[f.repo] = []code.Branch{{Name: "feature", Head: sha(3)}}
			reviewer := orgs.Role{ID: uuid.New(), Name: "Reviewer", Permissions: []string{"repo.read", code.PermReview}}
			f.orgs.RepositoryRoles = append(f.orgs.RepositoryRoles, reviewer)
			f.orgs.Grant(f.nia.ID, reviewer.ID, auth.ScopeRepository, f.repo)
			v := f.dev
			if tt.viewer != nil {
				v = tt.viewer(f)
			}
			got, err := f.service.SubmitReview(t.Context(), v, f.repo, sha(3), tt.in)
			if !sameError(err, tt.wantErr) {
				t.Fatalf("SubmitReview() error = %v, want %v", err, tt.wantErr)
			}
			if submitted := f.store.Submitted[got.ID]; err == nil && !reflect.DeepEqual(submitted, tt.in) {
				t.Errorf("the store took %+v, want %+v", submitted, tt.in)
			}
		})
	}
}

// checkSubmitted checks that got is v's review in of commit at, by a person when wantPerson, and that a Reader finds it.
func checkSubmitted(t *testing.T, f *annotators, v code.Viewer, at string, in code.ReviewInput, wantPerson bool, got code.Review) {
	t.Helper()
	if got.State != in.State || got.Body != in.Body || got.SHA != at || got.Reviewer == nil || got.Reviewer.ID != v.ID || got.Person != wantPerson {
		t.Errorf("SubmitReview() = %+v", got)
	}
	page, err := f.service.Reviews(t.Context(), f.rae, f.repo, at, code.ReviewQuery{})
	if err != nil || len(page.Reviews) != 1 || page.Reviews[0].ID != got.ID || page.Next != "" {
		t.Errorf("Reviews() = %+v, %v; want the review submitted", page, err)
	}
	found, err := f.service.Review(t.Context(), f.rae, f.repo, got.ID)
	if err != nil || found.ID != got.ID {
		t.Errorf("Review() = %+v, %v; want the review submitted", found, err)
	}
}

func TestReviews(t *testing.T) {
	tests := []struct {
		name    string
		viewer  func(*annotators) code.Viewer
		sha     string
		want    []code.ReviewState
		wantErr error
	}{
		{name: "in the order they were submitted", viewer: func(f *annotators) code.Viewer { return f.rae }, sha: sha(3),
			want: []code.ReviewState{code.ReviewChangesRequested, code.ReviewApproved}},
		{name: "a commit with none", viewer: func(f *annotators) code.Viewer { return f.rae }, sha: sha(2)},
		{name: "a commit the repository doesn't hold", viewer: func(f *annotators) code.Viewer { return f.rae }, sha: sha(9), wantErr: repos.ErrNotFound},
		{name: "a viewer who doesn't read the repository", viewer: func(f *annotators) code.Viewer { return f.nia }, sha: sha(3), wantErr: repos.ErrNotFound},
		{name: "an object name that isn't one", viewer: func(f *annotators) code.Viewer { return f.rae }, sha: "main", wantErr: invalid("sha.invalid")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newAnnotators()
			for _, state := range []code.ReviewState{code.ReviewChangesRequested, code.ReviewApproved} {
				if _, err := f.service.SubmitReview(t.Context(), f.mia, f.repo, sha(3), code.ReviewInput{State: state, Body: "Reviewed"}); err != nil {
					t.Fatal(err)
				}
			}
			page, err := f.service.Reviews(t.Context(), tt.viewer(f), f.repo, tt.sha, code.ReviewQuery{})
			if !sameError(err, tt.wantErr) {
				t.Fatalf("Reviews() error = %v, want %v", err, tt.wantErr)
			}
			var states []code.ReviewState
			for _, r := range page.Reviews {
				states = append(states, r.State)
			}
			if !slices.Equal(states, tt.want) {
				t.Errorf("Reviews() = %v, want %v", states, tt.want)
			}
		})
	}
}

func TestReviewsPages(t *testing.T) {
	f := newAnnotators()
	var ids []uuid.UUID
	for range 5 {
		r, err := f.service.SubmitReview(t.Context(), f.mia, f.repo, sha(3), code.ReviewInput{State: code.ReviewNeutral, Body: "Reviewed"})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, r.ID)
	}
	other, err := f.service.SubmitReview(t.Context(), f.mia, f.repo, sha(2), code.ReviewInput{State: code.ReviewApproved})
	if err != nil {
		t.Fatal(err)
	}
	var got []uuid.UUID
	pages, cursor := 0, ""
	for {
		page, err := f.service.Reviews(t.Context(), f.rae, f.repo, sha(3), code.ReviewQuery{Cursor: cursor, Limit: 2})
		if err != nil {
			t.Fatalf("page %d: %v", pages+1, err)
		}
		for _, r := range page.Reviews {
			got = append(got, r.ID)
		}
		if pages++; page.Next == "" || pages > 3 {
			break
		}
		cursor = page.Next
	}
	if pages != 3 || !slices.Equal(got, ids) {
		t.Errorf("paged %d times through %v, want 3 through %v", pages, got, ids)
	}

	badCursor, badLimit := invalid("cursor.invalid"), invalid("limit.invalid")
	tests := []struct {
		name    string
		q       code.ReviewQuery
		wantErr error
	}{
		{name: "a cursor that isn't one", q: code.ReviewQuery{Cursor: "not a cursor"}, wantErr: badCursor},
		{name: "a cursor of no review", q: code.ReviewQuery{Cursor: base64.RawURLEncoding.EncodeToString(uuid.Nil[:])}, wantErr: badCursor},
		{name: "a cursor of a review that isn't there", q: code.ReviewQuery{Cursor: cursorOf(uuid.New())}, wantErr: badCursor},
		{name: "a cursor of another commit's review", q: code.ReviewQuery{Cursor: cursorOf(other.ID)}, wantErr: badCursor},
		{name: "a page too large", q: code.ReviewQuery{Limit: code.MaxPageSize + 1}, wantErr: badLimit},
		{name: "a page of none", q: code.ReviewQuery{Limit: -1}, wantErr: badLimit},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := f.service.Reviews(t.Context(), f.rae, f.repo, sha(3), tt.q); !sameError(err, tt.wantErr) {
				t.Errorf("Reviews() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

// cursorOf is the cursor of the page of reviews after the one named id, as the service writes it.
func cursorOf(id uuid.UUID) string {
	return base64.RawURLEncoding.EncodeToString(id[:])
}

func TestReview(t *testing.T) {
	tests := []struct {
		name    string
		viewer  func(*annotators) code.Viewer
		id      func(code.Review) uuid.UUID
		wantErr error
	}{
		{name: "a Reader reads it", viewer: func(f *annotators) code.Viewer { return f.rae }, id: func(r code.Review) uuid.UUID { return r.ID }},
		{name: "one the repository doesn't hold", viewer: func(f *annotators) code.Viewer { return f.rae }, id: func(code.Review) uuid.UUID { return uuid.New() },
			wantErr: repos.ErrNotFound},
		{name: "a viewer who doesn't read the repository", viewer: func(f *annotators) code.Viewer { return f.nia }, id: func(r code.Review) uuid.UUID { return r.ID },
			wantErr: repos.ErrNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newAnnotators()
			submitted, err := f.service.SubmitReview(t.Context(), f.dev, f.repo, sha(3), code.ReviewInput{State: code.ReviewApproved})
			if err != nil {
				t.Fatal(err)
			}
			got, err := f.service.Review(t.Context(), tt.viewer(f), f.repo, tt.id(submitted))
			if !sameError(err, tt.wantErr) {
				t.Fatalf("Review() error = %v, want %v", err, tt.wantErr)
			}
			if err == nil && got.ID != submitted.ID {
				t.Errorf("Review() = %+v, want %+v", got, submitted)
			}
		})
	}
}

func TestLatest(t *testing.T) {
	alice, bob := &code.Principal{ID: uuid.New(), DisplayName: "Alice"}, &code.Principal{ID: uuid.New(), DisplayName: "Bob"}
	review := func(by *code.Principal, state code.ReviewState) code.Review {
		return code.Review{ID: uuid.New(), Reviewer: by, State: state}
	}
	a1, b1, a2, gone := review(alice, code.ReviewChangesRequested), review(bob, code.ReviewApproved), review(alice, code.ReviewApproved), review(nil, code.ReviewApproved)
	tests := []struct {
		name    string
		reviews []code.Review
		want    []code.Review
	}{
		{name: "none"},
		{name: "one each", reviews: []code.Review{a1, b1}, want: []code.Review{a1, b1}},
		{name: "a reviewer's latest, where they first reviewed", reviews: []code.Review{a1, b1, a2}, want: []code.Review{a2, b1}},
		{name: "a gone reviewer's left out", reviews: []code.Review{gone, b1}, want: []code.Review{b1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := code.Latest(tt.reviews)
			if len(got) != len(tt.want) {
				t.Fatalf("Latest() = %+v, want %+v", got, tt.want)
			}
			for i := range got {
				if got[i].ID != tt.want[i].ID {
					t.Errorf("Latest()[%d] = %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestReviewsMarkdown(t *testing.T) {
	tests := []struct {
		name    string
		fail    error
		read    func(f *annotators, submitted code.Review) (code.Review, error)
		wantErr bool
	}{
		{name: "as submitted", read: func(_ *annotators, r code.Review) (code.Review, error) { return r, nil }},
		{name: "as read", read: func(f *annotators, r code.Review) (code.Review, error) {
			return f.service.Review(t.Context(), f.rae, f.repo, r.ID)
		}},
		{name: "as listed", read: func(f *annotators, _ code.Review) (code.Review, error) {
			page, err := f.service.Reviews(t.Context(), f.rae, f.repo, sha(3), code.ReviewQuery{})
			if err != nil || len(page.Reviews) != 2 {
				return code.Review{}, fmt.Errorf("listed %d: %w", len(page.Reviews), err)
			}
			if page.Reviews[0].HTML != "" {
				return code.Review{}, fmt.Errorf("a review without a body renders as %q", page.Reviews[0].HTML)
			}
			return page.Reviews[1], nil
		}},
		{name: "a renderer that fails", fail: errors.New("broken"), wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newAnnotators()
			f.store.SlugList[f.repo] = [2]string{"acme", "website"}
			f.service = code.NewService(f.store, nil, &codetest.Renderer{Fail: tt.fail}, auth.NewAuthorizer(f.orgs))
			if _, err := f.service.SubmitReview(t.Context(), f.dev, f.repo, sha(3), code.ReviewInput{State: code.ReviewApproved}); err != nil {
				t.Fatal(err)
			}
			submitted, err := f.service.SubmitReview(t.Context(), f.mia, f.repo, sha(3), code.ReviewInput{State: code.ReviewNeutral, Body: "See [the docs](docs/a.md)."})
			if (err != nil) != tt.wantErr {
				t.Fatalf("SubmitReview() error = %v, want an error: %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			got, err := tt.read(f, submitted)
			want := `<div data-doc="acme/website@` + sha(3) + `:` + sha(3) + `:">See [the docs](docs/a.md).</div>`
			if err != nil || got.HTML != want || got.Body != "See [the docs](docs/a.md)." {
				t.Errorf("the review = %+v, %v; want its body rendered as %s", got, err, want)
			}
		})
	}
}
