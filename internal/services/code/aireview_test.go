package code_test

import (
	"errors"
	"strings"
	"testing"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/repos"
)

func TestRequestReview(t *testing.T) {
	tests := []struct {
		name     string
		before   func(*annotators)
		viewer   func(*annotators) code.Viewer
		branch   string
		wantErr  error
		wantTask int64
	}{
		{name: "a Developer asks for another review", viewer: func(f *annotators) code.Viewer { return f.dev }, wantTask: 1},
		{name: "a Maintainer asks after an earlier request", viewer: func(f *annotators) code.Viewer { return f.mia },
			before: func(f *annotators) { f.store.Tasks = 4 }, wantTask: 5},
		{name: "Automation can't push, so may not", viewer: func(f *annotators) code.Viewer { return f.ci }, wantErr: auth.ErrForbidden},
		{name: "a Reader may not", viewer: func(f *annotators) code.Viewer { return f.rae }, wantErr: auth.ErrForbidden},
		{name: "a viewer who doesn't read the repository finds nothing", viewer: func(f *annotators) code.Viewer { return f.nia },
			wantErr: repos.ErrNotFound},
		{name: "main's pushes are never reviewed", viewer: func(f *annotators) code.Viewer { return f.dev }, branch: "main", wantErr: code.ErrReviewOff},
		{name: "a branch the repository doesn't have", viewer: func(f *annotators) code.Viewer { return f.dev }, branch: "gone",
			wantErr: repos.ErrNotFound},
		{name: "a reviewer without a model", viewer: func(f *annotators) code.Viewer { return f.dev },
			before: func(f *annotators) { f.store.ReviewerOff = code.ErrReviewerNoModel }, wantErr: code.ErrReviewerNoModel},
		{name: "the store's failure", viewer: func(f *annotators) code.Viewer { return f.dev },
			before: func(f *annotators) { f.store.Fail = errors.New("down") }, wantErr: errors.New("down")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newAnnotators()
			f.store.SetBranch(f.repo, "main", sha(2))
			f.store.SetBranch(f.repo, "feature", sha(3))
			if tt.before != nil {
				tt.before(f)
			}
			branch := tt.branch
			if branch == "" {
				branch = "feature"
			}
			got, err := f.service.RequestReview(t.Context(), tt.viewer(f), f.repo, branch)
			if !sameError(err, tt.wantErr) {
				t.Fatalf("RequestReview() error = %v, want %v", err, tt.wantErr)
			}
			if err == nil && (got.Task != tt.wantTask || got.State != code.ReviewQueued) {
				t.Errorf("RequestReview() = %+v, want task %d, queued", got, tt.wantTask)
			}
		})
	}
}

func TestReviewTitle(t *testing.T) {
	long := strings.Repeat("b", 250)
	tests := []struct {
		name, branch, want string
	}{
		{name: "a branch and its head", branch: "feature/login", want: "Review feature/login at 0123456789ab"},
		{name: "a name too long for a title", branch: long, want: ("Review " + long)[:197] + "..."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := code.ReviewTitle(tt.branch, "0123456789abcdef0123456789abcdef01234567"); got != tt.want {
				t.Errorf("ReviewTitle() = %q, want %q", got, tt.want)
			}
		})
	}
}
