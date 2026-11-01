package code_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/git"
	"cobranch.dev/internal/services/orgs"
	"cobranch.dev/internal/services/orgs/orgstest"
	"cobranch.dev/internal/services/repos"
)

// mergeFixture is the fixture with Dana, a Developer, and Mo, a Maintainer, on its repository, and feature/x a Ready
// branch a commit ahead of main.
type mergeFixture struct {
	*fixture
	dana, mo code.Viewer
}

func newMergeFixture() *mergeFixture {
	f := &mergeFixture{fixture: newFixture()}
	o := orgstest.NewStore()
	o.Repos[f.repo] = uuid.New()
	f.dana = code.Viewer{Principal: auth.Principal{ID: uuid.New(), Kind: auth.KindUser, DisplayName: "Dana"},
		Emails: []auth.Email{{Email: "dana@old.example.com"}, {Email: "dana@example.com", Verified: true}}}
	f.mo = code.Viewer{Principal: auth.Principal{ID: uuid.New(), Kind: auth.KindUser, DisplayName: "Mo"}}
	o.Grant(f.max.ID, auth.RoleReader, auth.ScopeRepository, f.repo)
	o.Grant(f.dana.ID, auth.RoleDeveloper, auth.ScopeRepository, f.repo)
	o.Grant(f.mo.ID, auth.RoleMaintainer, auth.ScopeRepository, f.repo)
	f.service = code.NewService(f.store, f.objects, f.markdown, auth.NewAuthorizer(o))
	f.store.UpdateBranch(f.repo, "feature/x", func(b *code.Branch) {
		b.Base, b.Status, b.Facts = "main", code.StatusReady, &git.Facts{Branch: "feature/x", Ahead: 1}
	})
	return f
}

// mergeCase merges feature/x as viewer with in, once change has changed it, or main's requirements to require linear
// history; and expects the merge the Git server is asked for, or the error.
type mergeCase struct {
	name    string
	viewer  func(f *mergeFixture) code.Viewer
	branch  string
	in      code.MergeInput
	linear  bool
	change  func(b *code.Branch)
	want    func(f *mergeFixture) code.MergeRequest
	wantErr error
}

// asked is the merge of feature/x at its head by method, for Dana, past the requirements when bypass.
func asked(method code.MergeMethod, bypass bool) func(f *mergeFixture) code.MergeRequest {
	return func(f *mergeFixture) code.MergeRequest {
		by := code.Merger{ID: f.dana.ID, Name: "Dana", Email: "dana@example.com"}
		if bypass {
			by = code.Merger{ID: f.mo.ID, Name: "Mo"}
		}
		return code.MergeRequest{Branch: "feature/x", Head: sha(6), Base: "main", Method: method, By: by, Bypass: bypass}
	}
}

func mergeCases() []mergeCase {
	dana := func(f *mergeFixture) code.Viewer { return f.dana }
	mo := func(f *mergeFixture) code.Viewer { return f.mo }
	refused := func(c string) error { return &code.MergeError{Code: c} }
	invalid := func(c string) error { return &orgs.ValidationError{Code: c} }
	blocked := func(b *code.Branch) {
		b.Status, b.Checks = code.StatusBlocked, []code.Check{{Kind: code.CheckAnnotation, Name: "ci/build", Status: code.AnnotationFailed}}
	}
	return []mergeCase{
		{name: "a Ready branch merges by a merge commit", viewer: dana, want: asked(code.MethodMerge, false)},
		{name: "the head the requester saw", viewer: dana, in: code.MergeInput{Head: sha(6)}, want: asked(code.MethodMerge, false)},
		{name: "a fast-forward asked for", viewer: dana, in: code.MergeInput{Method: code.MethodFastForward}, want: asked(code.MethodFastForward, false)},
		{name: "linear history merges by fast-forward", viewer: dana, linear: true, want: asked(code.MethodFastForward, false)},
		{name: "linear history refuses a merge commit", viewer: dana, linear: true, in: code.MergeInput{Method: code.MethodMerge}, wantErr: invalid("method.invalid")},
		{name: "a method there is none of", viewer: dana, in: code.MergeInput{Method: "rebase"}, wantErr: invalid("method.invalid")},
		{name: "a head that names nothing", viewer: dana, in: code.MergeInput{Head: "HEAD~1"}, wantErr: invalid("head.invalid")},
		{name: "a head the branch moved from", viewer: dana, in: code.MergeInput{Head: sha(5)}, wantErr: code.ErrBranchMoved},
		{name: "a Blocked branch is refused", viewer: dana, change: blocked, wantErr: refused(code.MergeNotReady)},
		{name: "repo.bypass merges a Blocked branch", viewer: mo, change: blocked, want: asked(code.MethodMerge, true)},
		{name: "a Merged branch has nothing to merge", viewer: mo, change: func(b *code.Branch) { b.Status = code.StatusMerged }, wantErr: refused(code.MergeNothing)},
		{name: "a fast-forward of a branch behind main", viewer: mo, in: code.MergeInput{Method: code.MethodFastForward},
			change: func(b *code.Branch) { b.Facts.Behind = 2 }, wantErr: refused(code.MergeNotFastForward)},
		{name: "main merges into nothing", viewer: mo, branch: "main", wantErr: invalid("branch.default")},
		{name: "a branch that isn't there", viewer: dana, branch: "gone", wantErr: repos.ErrNotFound},
		{name: "a Reader may not merge", viewer: func(f *mergeFixture) code.Viewer { return f.max }, wantErr: auth.ErrForbidden},
		{name: "a viewer who doesn't read the repository finds none", viewer: func(f *mergeFixture) code.Viewer { return f.nia }, wantErr: repos.ErrNotFound},
	}
}

func TestMerge(t *testing.T) {
	for _, tt := range mergeCases() {
		t.Run(tt.name, func(t *testing.T) {
			f := tt.fixture()
			branch := tt.branch
			if branch == "" {
				branch = "feature/x"
			}
			merged, err := f.service.Merge(t.Context(), tt.viewer(f), f.repo, branch, tt.in)
			if !sameMergeError(err, tt.wantErr) {
				t.Fatalf("got error %v, want %v", err, tt.wantErr)
			}
			tt.check(t, f, merged)
		})
	}
}

// fixture is the merge fixture once tt changed feature/x, or main's requirements.
func (tt mergeCase) fixture() *mergeFixture {
	f := newMergeFixture()
	if tt.change != nil {
		f.store.UpdateBranch(f.repo, "feature/x", tt.change)
	}
	if tt.linear {
		f.store.UpdateBranch(f.repo, "main", func(b *code.Branch) { b.Requirements = &git.Requirements{Branch: "main", LinearHistory: true} })
	}
	return f
}

// check checks the merge the Git server was asked for, none when tt expects an error, and what the merge answered.
func (tt mergeCase) check(t *testing.T, f *mergeFixture, merged code.Merged) {
	t.Helper()
	if tt.want == nil {
		if len(f.objects.Merges) != 0 {
			t.Errorf("the Git server was asked for %+v, want nothing", f.objects.Merges)
		}
		return
	}
	if want := tt.want(f); len(f.objects.Merges) != 1 || !reflect.DeepEqual(f.objects.Merges[0], want) {
		t.Errorf("the Git server was asked for %+v, want %+v", f.objects.Merges, want)
	}
	if merged.Commit != sha(6) {
		t.Errorf("merged = %+v, want main at feature/x's head", merged)
	}
}

// sameMergeError is sameError, with a *code.MergeError matched by its code.
func sameMergeError(err, want error) bool {
	var wantRefused, gotRefused *code.MergeError
	if errors.As(want, &wantRefused) {
		return errors.As(err, &gotRefused) && gotRefused.Code == wantRefused.Code
	}
	return sameError(err, want)
}
