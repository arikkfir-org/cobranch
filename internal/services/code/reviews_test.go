package code_test

import (
	"cmp"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/code"
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

// checkSubmitted checks that got is v's review in of commit at, by a person when wantPerson, and that a Reader finds it.
func checkSubmitted(t *testing.T, f *annotators, v code.Viewer, at string, in code.ReviewInput, wantPerson bool, got code.Review) {
	t.Helper()
	if got.State != in.State || got.Body != in.Body || got.SHA != at || got.Reviewer == nil || got.Reviewer.ID != v.ID || got.Person != wantPerson {
		t.Errorf("SubmitReview() = %+v", got)
	}
	list, err := f.service.Reviews(t.Context(), f.rae, f.repo, at)
	if err != nil || len(list) != 1 || list[0].ID != got.ID {
		t.Errorf("Reviews() = %+v, %v; want the review submitted", list, err)
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
			list, err := f.service.Reviews(t.Context(), tt.viewer(f), f.repo, tt.sha)
			if !sameError(err, tt.wantErr) {
				t.Fatalf("Reviews() error = %v, want %v", err, tt.wantErr)
			}
			var states []code.ReviewState
			for _, r := range list {
				states = append(states, r.State)
			}
			if !slices.Equal(states, tt.want) {
				t.Errorf("Reviews() = %v, want %v", states, tt.want)
			}
		})
	}
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
