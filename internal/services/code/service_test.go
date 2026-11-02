package code_test

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/code/codetest"
	"cobranch.dev/internal/services/git"
	"cobranch.dev/internal/services/orgs"
	"cobranch.dev/internal/services/orgs/orgstest"
	"cobranch.dev/internal/services/repos"
)

var now = time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)

// sha is the n-th commit's name.
func sha(n int) string { return fmt.Sprintf("%040x", n) }

// fixture is a repository whose main runs 1 ← 2 ← 3 ← 4 ← 5 (5 merging feature's 6 as its second parent), feature
// at 6 on 2, and stray at an object no push recorded as a commit. Max reads the repository; Nia doesn't.
type fixture struct {
	store    *codetest.Store
	objects  *codetest.Objects
	markdown *codetest.Renderer
	authz    *auth.Authorizer
	repo     uuid.UUID
	service  *code.Service
	max      code.Viewer
	nia      code.Viewer
	push     code.Push
}

func newFixture() *fixture {
	o := orgstest.NewStore()
	f := &fixture{store: codetest.NewStore(), objects: codetest.NewObjects(), markdown: &codetest.Renderer{}, repo: uuid.New(),
		max: code.Viewer{Principal: auth.Principal{ID: uuid.New(), Kind: auth.KindUser, DisplayName: "Max"}},
		nia: code.Viewer{Principal: auth.Principal{ID: uuid.New(), Kind: auth.KindUser, DisplayName: "Nia"}}}
	o.Repos[f.repo] = uuid.New()
	f.store.SlugList[f.repo] = [2]string{"acme", "website"}
	f.authz = auth.NewAuthorizer(o)
	f.service = code.NewService(f.store, f.objects, f.markdown, f.authz)
	o.Grant(f.max.ID, auth.RoleReader, auth.ScopeRepository, f.repo)
	f.push = code.Push{ID: uuid.New(), Pusher: &code.Principal{ID: f.max.ID, DisplayName: "Max"}, Transport: git.TransportHTTPS, At: now}
	parents := map[int][]string{1: nil, 2: {sha(1)}, 3: {sha(2)}, 4: {sha(3)}, 5: {sha(4), sha(6)}, 6: {sha(2)}}
	for n, p := range parents {
		f.store.Add(f.repo, f.push, code.Commit{SHA: sha(n), Parents: p, Message: fmt.Sprintf("Commit %d\n", n)})
	}
	f.store.SetBranch(f.repo, "stray", sha(99))
	f.store.SetBranch(f.repo, "main", sha(5))
	f.store.SetBranch(f.repo, "feature/x", sha(6))
	return f
}

func shas(commits []code.Commit) []string {
	var out []string
	for _, c := range commits {
		out = append(out, strings.TrimLeft(c.SHA, "0"))
	}
	return out
}

func TestBranches(t *testing.T) {
	tests := []struct {
		name    string
		viewer  func(*fixture) code.Viewer
		fail    error
		want    []string
		wantErr error
	}{
		{name: "main first, then by name", viewer: func(f *fixture) code.Viewer { return f.max }, want: []string{"main", "feature/x", "stray"}},
		{name: "a viewer who doesn't read the repository finds none", viewer: func(f *fixture) code.Viewer { return f.nia }, wantErr: repos.ErrNotFound},
		{name: "the store's failure", viewer: func(f *fixture) code.Viewer { return f.max }, fail: errors.New("down"), wantErr: errors.New("down")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture()
			f.store.Fail = tt.fail
			got, err := f.service.Branches(t.Context(), tt.viewer(f), f.repo)
			if !sameError(err, tt.wantErr) {
				t.Fatalf("got error %v, want %v", err, tt.wantErr)
			}
			var names []string
			for _, b := range got {
				names = append(names, b.Name)
			}
			if !slices.Equal(names, tt.want) {
				t.Errorf("got %v, want %v", names, tt.want)
			}
		})
	}
}

func TestBranch(t *testing.T) {
	tests := []struct {
		name     string
		viewer   func(*fixture) code.Viewer
		branch   string
		wantHead string
		wantErr  error
	}{
		{name: "a branch named with a slash", viewer: func(f *fixture) code.Viewer { return f.max }, branch: "feature/x", wantHead: sha(6)},
		{name: "a branch that isn't there", viewer: func(f *fixture) code.Viewer { return f.max }, branch: "gone", wantErr: repos.ErrNotFound},
		{name: "a viewer who doesn't read the repository finds none", viewer: func(f *fixture) code.Viewer { return f.nia }, branch: "main",
			wantErr: repos.ErrNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture()
			got, err := f.service.Branch(t.Context(), tt.viewer(f), f.repo, tt.branch)
			if !sameError(err, tt.wantErr) {
				t.Fatalf("got error %v, want %v", err, tt.wantErr)
			}
			if got.Head != tt.wantHead {
				t.Errorf("head = %s, want %s", got.Head, tt.wantHead)
			}
		})
	}
}

// sameError reports whether err is want: the same domain error, or one whose message holds want's.
func sameError(err, want error) bool {
	if err == nil || want == nil {
		return err == nil && want == nil
	}
	var verr *orgs.ValidationError
	if errors.As(want, &verr) {
		var got *orgs.ValidationError
		return errors.As(err, &got) && got.Code == verr.Code
	}
	return errors.Is(err, want) || strings.Contains(err.Error(), want.Error())
}

func TestCommit(t *testing.T) {
	tests := []struct {
		name     string
		viewer   func(*fixture) code.Viewer
		sha      string
		wantErr  error
		wantPush bool
	}{
		{name: "a commit with the push that brought it", sha: sha(5), wantPush: true},
		{name: "a commit the repository didn't record", sha: sha(77), wantErr: repos.ErrNotFound},
		{name: "an abbreviated name", sha: "0000005", wantErr: &orgs.ValidationError{Code: "sha.invalid"}},
		{name: "an uppercase name", sha: strings.ToUpper(fmt.Sprintf("%040x", 0xabc)), wantErr: &orgs.ValidationError{Code: "sha.invalid"}},
		{name: "a viewer who doesn't read the repository", viewer: func(f *fixture) code.Viewer { return f.nia }, sha: sha(5), wantErr: repos.ErrNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture()
			v := f.max
			if tt.viewer != nil {
				v = tt.viewer(f)
			}
			commit, push, err := f.service.Commit(t.Context(), v, f.repo, tt.sha)
			if !sameError(err, tt.wantErr) {
				t.Fatalf("got error %v, want %v", err, tt.wantErr)
			}
			if tt.wantPush && (commit.SHA != tt.sha || push.ID != f.push.ID || push.Pusher.DisplayName != "Max") {
				t.Errorf("got %+v pushed by %+v, want %s pushed in %s by Max", commit, push, tt.sha, f.push.ID)
			}
		})
	}
}

func TestValidateSHA(t *testing.T) {
	tests := []struct {
		sha  string
		want bool
	}{
		{sha: sha(1), want: true},
		{sha: strings.Repeat("ab", 32), want: true},
		{sha: strings.Repeat("a", 39)},
		{sha: strings.Repeat("a", 41)},
		{sha: strings.Repeat("g", 40)},
		{sha: ""},
	}
	for _, tt := range tests {
		t.Run(tt.sha, func(t *testing.T) {
			if got := code.ValidateSHA("sha", tt.sha) == nil; got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}
