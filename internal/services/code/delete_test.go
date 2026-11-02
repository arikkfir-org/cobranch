package code_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/git"
	"cobranch.dev/internal/services/orgs"
	"cobranch.dev/internal/services/repos"
)

// deleteCase deletes branch, feature/x if none, as viewer at head, once change has changed feature/x; and expects the
// deletion the Git server is asked for, or the error.
type deleteCase struct {
	name    string
	viewer  func(f *mergeFixture) code.Viewer
	branch  string
	head    string
	change  func(b *code.Branch)
	want    func(f *mergeFixture) code.DeleteRequest
	wantErr error
}

// deletion is the deletion of feature/x at its head, for Dana, or for Mo past its requirements when bypass.
func deletion(bypass bool) func(f *mergeFixture) code.DeleteRequest {
	return func(f *mergeFixture) code.DeleteRequest {
		if bypass {
			return code.DeleteRequest{Branch: "feature/x", Head: sha(6), By: f.mo.ID, Bypass: true}
		}
		return code.DeleteRequest{Branch: "feature/x", Head: sha(6), By: f.dana.ID}
	}
}

func deleteCases() []deleteCase {
	dana := func(f *mergeFixture) code.Viewer { return f.dana }
	mo := func(f *mergeFixture) code.Viewer { return f.mo }
	named := func(b *code.Branch) {
		b.Requirements = &git.Requirements{Branch: b.Name, Annotations: []string{"ci/build"}}
	}
	return []deleteCase{
		{name: "a merged branch", viewer: dana, change: func(b *code.Branch) { b.Status = code.StatusMerged }, want: deletion(false)},
		{name: "a branch with commits main lacks", viewer: dana, want: deletion(false)},
		{name: "at the head the requester saw", viewer: dana, head: sha(6), want: deletion(false)},
		{name: "a head the branch moved from", viewer: dana, head: sha(5), wantErr: code.ErrBranchMoved},
		{name: "a head that names nothing", viewer: dana, head: "HEAD~1", wantErr: &orgs.ValidationError{Code: "head.invalid"}},
		{name: "main is never deleted", viewer: mo, branch: "main", wantErr: &code.DeleteError{Code: code.DeleteDefault}},
		{name: "a branch /cobranch.yaml names takes repo.bypass", viewer: dana, change: named, wantErr: auth.ErrForbidden},
		{name: "repo.bypass deletes a branch /cobranch.yaml names", viewer: mo, change: named, want: deletion(true)},
		{name: "a branch that isn't there", viewer: dana, branch: "gone", wantErr: repos.ErrNotFound},
		{name: "a Reader may not delete", viewer: func(f *mergeFixture) code.Viewer { return f.max }, wantErr: auth.ErrForbidden},
		{name: "a viewer who doesn't read the repository finds none", viewer: func(f *mergeFixture) code.Viewer { return f.nia }, wantErr: repos.ErrNotFound},
	}
}

func TestDeleteBranch(t *testing.T) {
	for _, tt := range deleteCases() {
		t.Run(tt.name, func(t *testing.T) {
			f := newMergeFixture()
			if tt.change != nil {
				f.store.UpdateBranch(f.repo, "feature/x", tt.change)
			}
			branch := tt.branch
			if branch == "" {
				branch = "feature/x"
			}
			err := f.service.DeleteBranch(t.Context(), tt.viewer(f), f.repo, branch, tt.head)
			if !sameDeleteError(err, tt.wantErr) {
				t.Fatalf("got error %v, want %v", err, tt.wantErr)
			}
			var want []code.DeleteRequest
			if tt.want != nil {
				want = []code.DeleteRequest{tt.want(f)}
			}
			if !reflect.DeepEqual(f.objects.Deletions, want) {
				t.Errorf("the Git server was asked for %+v, want %+v", f.objects.Deletions, want)
			}
		})
	}
}

func TestDeleteBranchRefused(t *testing.T) {
	refused := &code.DeleteError{Code: code.DeleteRefused, Message: "cobranch/ is Cobranch's own"}
	tests := []struct {
		name    string
		fail    error
		wantErr error
	}{
		{name: "a deletion the push refuses", fail: refused, wantErr: refused},
		{name: "a branch that moved meanwhile", fail: code.ErrBranchMoved, wantErr: code.ErrBranchMoved},
		{name: "no Git server", fail: code.ErrUnavailable, wantErr: code.ErrUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newMergeFixture()
			f.objects.Fail = tt.fail
			if err := f.service.DeleteBranch(t.Context(), f.dana, f.repo, "feature/x", ""); !sameDeleteError(err, tt.wantErr) {
				t.Errorf("got error %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestDeleteBranchMovedMeanwhile(t *testing.T) {
	tests := []struct {
		name    string
		head    string
		moves   int
		want    []code.DeleteRequest
		wantErr error
	}{
		{name: "wherever it is, at the head it moved to", moves: 1, want: []code.DeleteRequest{{Branch: "feature/x", Head: sha(8)}}},
		{name: "wherever it is, after it moved twice", moves: 2, want: []code.DeleteRequest{{Branch: "feature/x", Head: sha(9)}}},
		{name: "wherever it is, while it keeps moving", moves: 3, wantErr: code.ErrBranchMoved},
		{name: "at the head the requester saw, once it moved", head: sha(6), moves: 1, wantErr: code.ErrBranchMoved},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newMergeFixture()
			moved, next := 0, 7
			f.objects.OnDelete = func(code.DeleteRequest) error {
				if moved == tt.moves {
					return nil
				}
				moved++
				next++
				f.store.UpdateBranch(f.repo, "feature/x", func(b *code.Branch) { b.Head = sha(next) })
				return code.ErrBranchMoved
			}
			err := f.service.DeleteBranch(t.Context(), f.dana, f.repo, "feature/x", tt.head)
			if !sameError(err, tt.wantErr) {
				t.Fatalf("got error %v, want %v", err, tt.wantErr)
			}
			for i := range tt.want {
				tt.want[i].By = f.dana.ID
			}
			if !reflect.DeepEqual(f.objects.Deletions, tt.want) {
				t.Errorf("the Git server recorded %+v, want %+v", f.objects.Deletions, tt.want)
			}
		})
	}
}

func TestDeletedBranch(t *testing.T) {
	at := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name     string
		viewer   func(f *mergeFixture) code.Viewer
		branch   string
		wantHead string
		wantErr  error
	}{
		{name: "the latest deletion of a name", viewer: func(f *mergeFixture) code.Viewer { return f.max }, branch: "old", wantHead: sha(2)},
		{name: "a live branch's name", viewer: func(f *mergeFixture) code.Viewer { return f.max }, branch: "feature/x", wantErr: repos.ErrNotFound},
		{name: "a viewer who doesn't read the repository finds none", viewer: func(f *mergeFixture) code.Viewer { return f.nia }, branch: "old",
			wantErr: repos.ErrNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newMergeFixture()
			f.store.Deleted[f.repo] = []code.DeletedBranch{{Name: "old", Head: sha(1), DeletedAt: at},
				{Name: "old", Head: sha(2), DeletedAt: at.Add(time.Hour)}, {Name: "feature/x", Head: sha(3), DeletedAt: at}}
			got, err := f.service.DeletedBranch(t.Context(), tt.viewer(f), f.repo, tt.branch)
			if !sameError(err, tt.wantErr) || got.Head != tt.wantHead {
				t.Errorf("DeletedBranch(%s) = %+v, %v; want at %q, %v", tt.branch, got, err, tt.wantHead, tt.wantErr)
			}
		})
	}
}

// sameDeleteError reports whether err is want: a *code.DeleteError of the same code, or the same error.
func sameDeleteError(err, want error) bool {
	var wantRefused, gotRefused *code.DeleteError
	if errors.As(want, &wantRefused) {
		return errors.As(err, &gotRefused) && gotRefused.Code == wantRefused.Code
	}
	return sameError(err, want)
}
