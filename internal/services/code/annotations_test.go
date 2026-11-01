package code_test

import (
	"cmp"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/code/codetest"
	"cobranch.dev/internal/services/orgs"
	"cobranch.dev/internal/services/orgs/orgstest"
	"cobranch.dev/internal/services/repos"
)

// annotators is a repository with commits 1 to 3 and a viewer per built-in repository role: Rae reads it, Dev
// develops, Mia maintains it, and the CI's API key automates; Nia holds nothing there.
type annotators struct {
	store                  *codetest.Store
	repo                   uuid.UUID
	service                *code.Service
	rae, dev, mia, ci, nia code.Viewer
}

func newAnnotators() *annotators {
	o := orgstest.NewStore()
	viewer := func(name string) code.Viewer {
		return code.Viewer{Principal: auth.Principal{ID: uuid.New(), Kind: auth.KindUser, DisplayName: name}}
	}
	f := &annotators{store: codetest.NewStore(), repo: uuid.New(), rae: viewer("Rae"), dev: viewer("Dev"), mia: viewer("Mia"), ci: viewer("CI"), nia: viewer("Nia")}
	f.ci.Kind = auth.KindAPIKey
	o.Repos[f.repo] = uuid.New()
	f.service = code.NewService(f.store, nil, auth.NewAuthorizer(o))
	o.Grant(f.rae.ID, auth.RoleReader, auth.ScopeRepository, f.repo)
	o.Grant(f.dev.ID, auth.RoleDeveloper, auth.ScopeRepository, f.repo)
	o.Grant(f.mia.ID, auth.RoleMaintainer, auth.ScopeRepository, f.repo)
	o.Grant(f.ci.ID, auth.RoleAutomation, auth.ScopeRepository, f.repo)
	for _, v := range []code.Viewer{f.rae, f.dev, f.mia, f.ci, f.nia} {
		f.store.Names[v.ID] = v.DisplayName
	}
	f.store.Now = now
	f.store.Line(f.repo, code.Push{ID: uuid.New(), At: now}, sha(1), sha(2), sha(3))
	return f
}

func build(status code.AnnotationStatus) code.AnnotationInput {
	return code.AnnotationInput{Name: "ci/build", Status: status, Summary: "Built", Link: "https://ci.example.com/runs/7"}
}

// from is in, made from version of the annotation.
func from(version int64, in code.AnnotationInput) code.AnnotationInput {
	in.Version = version
	return in
}

// twice is CI setting ci/build on commit 3 at version 2.
func twice(f *annotators) {
	f.set(f.ci, sha(3), build(code.AnnotationPending))
	f.set(f.ci, sha(3), build(code.AnnotationInProgress))
}

// setCase sets in on commit sha, 3 by default, as viewer, Dev by default, after before.
type setCase struct {
	name    string
	before  func(*annotators)
	viewer  func(*annotators) code.Viewer
	sha     string
	in      code.AnnotationInput
	wantErr error
	taken   string
}

func invalid(code string) error { return &orgs.ValidationError{Code: code} }

func TestSetAnnotation(t *testing.T) {
	runSetCases(t, []setCase{
		{name: "Automation sets one", viewer: func(f *annotators) code.Viewer { return f.ci }, in: build(code.AnnotationPending)},
		{name: "a Developer sets one", viewer: func(f *annotators) code.Viewer { return f.dev }, in: build(code.AnnotationSuccessful)},
		{name: "its principal changes it", before: func(f *annotators) { f.set(f.ci, sha(3), build(code.AnnotationInProgress)) },
			viewer: func(f *annotators) code.Viewer { return f.ci }, in: build(code.AnnotationFailed)},
		{name: "another principal is refused, naming whose it is", before: func(f *annotators) { f.set(f.ci, sha(3), build(code.AnnotationPending)) },
			viewer: func(f *annotators) code.Viewer { return f.dev }, in: build(code.AnnotationSuccessful), wantErr: code.ErrAnnotationTaken, taken: "CI"},
		{name: "a Maintainer is refused another's too", before: func(f *annotators) { f.set(f.ci, sha(3), build(code.AnnotationPending)) },
			viewer: func(f *annotators) code.Viewer { return f.mia }, in: build(code.AnnotationSuccessful), wantErr: code.ErrAnnotationTaken, taken: "CI"},
		{name: "the same name on another commit is anyone's", before: func(f *annotators) { f.set(f.ci, sha(2), build(code.AnnotationPending)) },
			viewer: func(f *annotators) code.Viewer { return f.dev }, in: build(code.AnnotationSuccessful)},
		{name: "a Reader may not", viewer: func(f *annotators) code.Viewer { return f.rae }, in: build(code.AnnotationPending), wantErr: auth.ErrForbidden},
		{name: "a viewer who doesn't read the repository finds nothing", viewer: func(f *annotators) code.Viewer { return f.nia }, in: build(code.AnnotationPending), wantErr: repos.ErrNotFound},
		{name: "a commit the repository doesn't hold", sha: sha(9), in: build(code.AnnotationPending), wantErr: repos.ErrNotFound},
		{name: "an abbreviated commit", sha: "0000003", in: build(code.AnnotationPending), wantErr: invalid("sha.invalid")},
		{name: "a name with uppercase", in: code.AnnotationInput{Name: "CI/build", Status: code.AnnotationPending}, wantErr: invalid("name.invalid")},
		{name: "a name starting with a slash", in: code.AnnotationInput{Name: "/build", Status: code.AnnotationPending}, wantErr: invalid("name.invalid")},
		{name: "a name of 101 characters", in: code.AnnotationInput{Name: strings.Repeat("a", 101), Status: code.AnnotationPending}, wantErr: invalid("name.invalid")},
		{name: "a name of 100 characters", in: code.AnnotationInput{Name: strings.Repeat("a", 100), Status: code.AnnotationPending}},
		{name: "an unknown status", in: code.AnnotationInput{Name: "ci", Status: "passed"}, wantErr: invalid("status.invalid")},
		{name: "a summary of 501 characters", in: code.AnnotationInput{Name: "ci", Status: code.AnnotationFailed, Summary: strings.Repeat("é", 501)}, wantErr: invalid("summary.invalid")},
		{name: "a summary of 500 characters", in: code.AnnotationInput{Name: "ci", Status: code.AnnotationFailed, Summary: strings.Repeat("é", 500)}},
		{name: "a relative link", in: code.AnnotationInput{Name: "ci", Status: code.AnnotationFailed, Link: "/runs/7"}, wantErr: invalid("link.invalid")},
		{name: "a link of another scheme", in: code.AnnotationInput{Name: "ci", Status: code.AnnotationFailed, Link: "javascript:alert(1)"}, wantErr: invalid("link.invalid")},
		{name: "an http link", in: code.AnnotationInput{Name: "ci", Status: code.AnnotationFailed, Link: "http://ci.example.com/7"}},
		{name: "the store's failure", before: func(f *annotators) { f.store.Fail = errors.New("down") }, in: build(code.AnnotationPending), wantErr: errors.New("down")},
	})
}

func TestSetAnnotationVersions(t *testing.T) {
	runSetCases(t, []setCase{
		{name: "its principal changes the version it read", before: twice, viewer: func(f *annotators) code.Viewer { return f.ci },
			in: from(2, build(code.AnnotationSuccessful))},
		{name: "a version since changed is refused", before: twice, viewer: func(f *annotators) code.Viewer { return f.ci },
			in: from(1, build(code.AnnotationSuccessful)), wantErr: code.ErrAnnotationChanged},
		{name: "any version changes the one there", before: twice, viewer: func(f *annotators) code.Viewer { return f.ci },
			in: from(code.AnyVersion, build(code.AnnotationSuccessful))},
		{name: "a version of one that isn't there is refused", in: from(1, build(code.AnnotationPending)), wantErr: code.ErrAnnotationChanged},
		{name: "any version of one that isn't there is refused", in: from(code.AnyVersion, build(code.AnnotationPending)), wantErr: code.ErrAnnotationChanged},
		{name: "another principal's is refused first, whatever the version", before: twice,
			in: from(1, build(code.AnnotationSuccessful)), wantErr: code.ErrAnnotationTaken, taken: "CI"},
		{name: "a new one where there is none", in: from(code.NoAnnotation, build(code.AnnotationPending))},
		{name: "a new one where its own is, is refused", before: twice, viewer: func(f *annotators) code.Viewer { return f.ci },
			in: from(code.NoAnnotation, build(code.AnnotationSuccessful)), wantErr: code.ErrAnnotationChanged},
		{name: "a new one where another's is, is refused naming them", before: twice,
			in: from(code.NoAnnotation, build(code.AnnotationSuccessful)), wantErr: code.ErrAnnotationTaken, taken: "CI"},
		{name: "a version that can't be one", in: from(-3, build(code.AnnotationPending)), wantErr: invalid("version.invalid")},
	})
}

func runSetCases(t *testing.T, tests []setCase) {
	t.Helper()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newAnnotators()
			if tt.before != nil {
				tt.before(f)
			}
			v := f.dev
			if tt.viewer != nil {
				v = tt.viewer(f)
			}
			got, err := f.service.SetAnnotation(t.Context(), v, f.repo, cmp.Or(tt.sha, sha(3)), tt.in)
			if !sameError(err, tt.wantErr) {
				t.Fatalf("got error %v, want %v", err, tt.wantErr)
			}
			if tt.taken != "" {
				checkTaken(t, err, tt.taken)
			}
			if err == nil && (got.Name != tt.in.Name || got.Status != tt.in.Status || got.Principal == nil || got.Principal.ID != v.ID) {
				t.Errorf("got %+v, want %+v set by %s", got, tt.in, v.DisplayName)
			}
		})
	}
}

// checkTaken checks that err refuses an annotation that by set, naming them.
func checkTaken(t *testing.T, err error, by string) {
	t.Helper()
	var taken *code.TakenError
	if !errors.As(err, &taken) || taken.By == nil || taken.By.DisplayName != by || !strings.Contains(err.Error(), by) {
		t.Errorf("got %v, want it to name %s", err, by)
	}
}

// set sets in on the repository's commit at as v, failing the test's setup otherwise.
func (f *annotators) set(v code.Viewer, at string, in code.AnnotationInput) {
	if _, err := f.store.SetAnnotation(context.Background(), v.ID, f.repo, at, in); err != nil {
		panic(err)
	}
}

func TestAnnotationRuns(t *testing.T) {
	f := newAnnotators()
	later := func(d time.Duration) { f.store.Now = now.Add(d) }
	step := func(status code.AnnotationStatus) code.Annotation {
		t.Helper()
		a, err := f.service.SetAnnotation(t.Context(), f.ci, f.repo, sha(3), build(status))
		if err != nil {
			t.Fatalf("setting %s: %v", status, err)
		}
		return a
	}
	tests := []struct {
		status        code.AnnotationStatus
		after         time.Duration
		wantStarted   time.Duration
		wantCompleted time.Duration // -1: none
		wantVersion   int64
	}{
		{status: code.AnnotationPending, wantCompleted: -1, wantVersion: 1},
		{status: code.AnnotationInProgress, after: time.Minute, wantCompleted: -1, wantVersion: 2},
		{status: code.AnnotationSuccessful, after: 3 * time.Minute, wantCompleted: 3 * time.Minute, wantVersion: 3},
		{status: code.AnnotationSuccessful, after: 4 * time.Minute, wantCompleted: 3 * time.Minute, wantVersion: 3}, // unchanged
		{status: code.AnnotationPending, after: 5 * time.Minute, wantStarted: 5 * time.Minute, wantCompleted: -1, wantVersion: 4},
		{status: code.AnnotationFailed, after: 6 * time.Minute, wantStarted: 5 * time.Minute, wantCompleted: 6 * time.Minute, wantVersion: 5},
		{status: code.AnnotationSuccessful, after: 7 * time.Minute, wantStarted: 5 * time.Minute, wantCompleted: 7 * time.Minute, wantVersion: 6},
	}
	for _, tt := range tests {
		later(tt.after)
		a := step(tt.status)
		if a.Version != tt.wantVersion {
			t.Errorf("%s after %s: version %d, want %d", tt.status, tt.after, a.Version, tt.wantVersion)
		}
		if !a.StartedAt.Equal(now.Add(tt.wantStarted)) {
			t.Errorf("%s after %s: started at %s, want %s", tt.status, tt.after, a.StartedAt, now.Add(tt.wantStarted))
		}
		switch {
		case tt.wantCompleted < 0 && a.CompletedAt != nil:
			t.Errorf("%s after %s: completed at %s, want it running", tt.status, tt.after, a.CompletedAt)
		case tt.wantCompleted >= 0 && (a.CompletedAt == nil || !a.CompletedAt.Equal(now.Add(tt.wantCompleted))):
			t.Errorf("%s after %s: completed at %v, want %s", tt.status, tt.after, a.CompletedAt, now.Add(tt.wantCompleted))
		}
	}
}

func TestDeleteAnnotation(t *testing.T) {
	tests := []struct {
		name    string
		viewer  func(*annotators) code.Viewer
		annName string
		version int64
		wantErr error
	}{
		{name: "its principal deletes it", viewer: func(f *annotators) code.Viewer { return f.ci }},
		{name: "at the version it read", viewer: func(f *annotators) code.Viewer { return f.ci }, version: 1},
		{name: "at any version", viewer: func(f *annotators) code.Viewer { return f.mia }, version: code.AnyVersion},
		{name: "a version since changed is refused", viewer: func(f *annotators) code.Viewer { return f.ci }, version: 2, wantErr: code.ErrAnnotationChanged},
		{name: "a version that can't be one", viewer: func(f *annotators) code.Viewer { return f.ci }, version: -3, wantErr: &orgs.ValidationError{Code: "version.invalid"}},
		{name: "none, which deletes nothing", viewer: func(f *annotators) code.Viewer { return f.ci }, version: code.NoAnnotation, wantErr: &orgs.ValidationError{Code: "version.invalid"}},
		{name: "a version of one that isn't there", viewer: func(f *annotators) code.Viewer { return f.ci }, annName: "ci/test", version: 1, wantErr: code.ErrAnnotationChanged},
		{name: "a Maintainer deletes another's", viewer: func(f *annotators) code.Viewer { return f.mia }},
		{name: "a Developer may not delete another's", viewer: func(f *annotators) code.Viewer { return f.dev }, wantErr: auth.ErrForbidden},
		{name: "a Reader may not", viewer: func(f *annotators) code.Viewer { return f.rae }, wantErr: auth.ErrForbidden},
		{name: "a viewer who doesn't read the repository finds nothing", viewer: func(f *annotators) code.Viewer { return f.nia }, wantErr: repos.ErrNotFound},
		{name: "one that isn't there", viewer: func(f *annotators) code.Viewer { return f.ci }, annName: "ci/test", wantErr: repos.ErrNotFound},
		{name: "a name no annotation has", viewer: func(f *annotators) code.Viewer { return f.ci }, annName: "CI", wantErr: &orgs.ValidationError{Code: "name.invalid"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newAnnotators()
			f.set(f.ci, sha(3), build(code.AnnotationFailed))
			name := "ci/build"
			if tt.annName != "" {
				name = tt.annName
			}
			err := f.service.DeleteAnnotation(t.Context(), tt.viewer(f), f.repo, sha(3), name, tt.version)
			if !sameError(err, tt.wantErr) {
				t.Fatalf("got error %v, want %v", err, tt.wantErr)
			}
			left, err := f.service.Annotations(t.Context(), f.rae, f.repo, sha(3))
			if err != nil {
				t.Fatal(err)
			}
			if gone := len(left) == 0; gone != (tt.wantErr == nil) {
				t.Errorf("got %d annotations left, want it deleted: %t", len(left), tt.wantErr == nil)
			}
		})
	}
}

func TestDeletedAnnotationFreesItsName(t *testing.T) {
	f := newAnnotators()
	f.set(f.ci, sha(3), build(code.AnnotationFailed))
	if err := f.service.DeleteAnnotation(t.Context(), f.mia, f.repo, sha(3), "ci/build", 0); err != nil {
		t.Fatal(err)
	}
	a, err := f.service.SetAnnotation(t.Context(), f.dev, f.repo, sha(3), build(code.AnnotationSuccessful))
	if err != nil || a.Principal.ID != f.dev.ID {
		t.Fatalf("got %+v, %v; want Dev's", a, err)
	}
}

func TestAnnotations(t *testing.T) {
	tests := []struct {
		name    string
		viewer  func(*annotators) code.Viewer
		sha     string
		want    []string
		wantErr error
	}{
		{name: "by name", viewer: func(f *annotators) code.Viewer { return f.rae }, sha: sha(3), want: []string{"ai-review", "ci/build"}},
		{name: "a commit with none", viewer: func(f *annotators) code.Viewer { return f.rae }, sha: sha(2)},
		{name: "a commit the repository doesn't hold", viewer: func(f *annotators) code.Viewer { return f.rae }, sha: sha(9), wantErr: repos.ErrNotFound},
		{name: "a viewer who doesn't read the repository", viewer: func(f *annotators) code.Viewer { return f.nia }, sha: sha(3), wantErr: repos.ErrNotFound},
		{name: "an object name that isn't one", viewer: func(f *annotators) code.Viewer { return f.rae }, sha: "main", wantErr: &orgs.ValidationError{Code: "sha.invalid"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newAnnotators()
			f.set(f.ci, sha(3), build(code.AnnotationFailed))
			f.set(f.dev, sha(3), code.AnnotationInput{Name: "ai-review", Status: code.AnnotationPending})
			list, err := f.service.Annotations(t.Context(), tt.viewer(f), f.repo, tt.sha)
			if !sameError(err, tt.wantErr) {
				t.Fatalf("got error %v, want %v", err, tt.wantErr)
			}
			var names []string
			for _, a := range list {
				names = append(names, a.Name)
			}
			if strings.Join(names, ",") != strings.Join(tt.want, ",") {
				t.Errorf("got %v, want %v", names, tt.want)
			}
		})
	}
}
