package gitserver_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"

	"cobranch.dev/internal/adapters/gitinternal"
	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/code"
)

// apiMergeCase sets up main, perhaps with a /cobranch.yaml, and branch feature a commit ahead of it, changing
// featureFile, perhaps annotated ci/build Successful; then perhaps a commit on main that changes mainFile; and asks the
// Git server, on a token of Olivia's that holds perms (repo.read, repo.merge, and repo.bypass when bypass, if none) or
// on none when anonymous, for a merge of feature at the head head names by method, for Olivia or for someone else,
// past the requirements when bypass. It expects the merge refused with wantErr, or main at a merge commit of
// wantParents parents, or at feature's head for none, and feature Merged by Olivia once the push worker processed it.
type apiMergeCase struct {
	name        string
	settings    string
	annotate    bool
	featureFile string
	mainFile    string
	method      code.MergeMethod
	head        func(feature string) string
	bypass      bool
	perms       []string
	anonymous   bool
	someoneElse bool
	wantErr     error
	wantParents int
}

func apiMergeCases() []apiMergeCase {
	refused := func(c string) error { return &code.MergeError{Code: c} }
	return []apiMergeCase{
		{name: "a merge commit", mainFile: "main.txt", method: code.MethodMerge, wantParents: 2},
		{name: "a merge commit of a branch main is in", method: code.MethodMerge, wantParents: 2},
		{name: "a fast-forward", method: code.MethodFastForward},
		{name: "a fast-forward of a branch behind main", mainFile: "main.txt", method: code.MethodFastForward, wantErr: refused(code.MergeNotFastForward)},
		{name: "a merge commit of changes Git can't merge", featureFile: "README.md", mainFile: "README.md", method: code.MethodMerge,
			wantErr: refused(code.MergeConflict)},
		{name: "a head the branch moved from", method: code.MethodMerge, head: func(string) string { return strings.Repeat("a", 40) },
			wantErr: code.ErrBranchMoved},
		{name: "a branch main requires to be Ready", settings: requireAnnotation, method: code.MethodMerge, wantErr: refused(code.MergeRefused)},
		{name: "a Ready branch main has requirements of", settings: requireAnnotation, annotate: true, method: code.MethodFastForward},
		{name: "repo.bypass past main's requirements", settings: requireAnnotation, method: code.MethodMerge, bypass: true, wantParents: 2},
	}
}

func TestMergeByAPI(t *testing.T) {
	for _, tt := range apiMergeCases() {
		t.Run(tt.name, func(t *testing.T) {
			s, client, feature, before := apiMergeRepo(t, tt)
			head := feature
			if tt.head != nil {
				head = tt.head(feature)
			}
			by := code.Merger{ID: olivia, Name: "Olivia", Email: "olivia@example.com"}
			if tt.someoneElse {
				by.ID = uuid.New()
			}
			got, err := client.Merge(tt.asker(t, s), website, code.MergeRequest{Branch: "feature", Head: head, Base: "main", Method: tt.method, By: by, Bypass: tt.bypass})
			if !sameRefusal(err, tt.wantErr) {
				t.Fatalf("Merge = %v, want %v", err, tt.wantErr)
			}
			main := query[string](t, s.pool, "SELECT sha FROM refs WHERE repo_id = $1 AND name = 'refs/heads/main'", website)
			if tt.wantErr != nil {
				if main != before {
					t.Errorf("main = %s, want it left at %s", main, before)
				}
				return
			}
			checkMerge(t, s, tt, got, main, before, feature)
		})
	}
}

// asker is the context of tt's merge: one that carries a token of Olivia's holding tt's permissions, or none.
func (tt apiMergeCase) asker(t *testing.T, s *stack) context.Context {
	t.Helper()
	if tt.anonymous {
		return t.Context()
	}
	perms := tt.perms
	if perms == nil {
		perms = []string{"repo.read", "repo.merge"}
		if tt.bypass {
			perms = append(perms, "repo.bypass")
		}
	}
	return auth.WithToken(t.Context(), s.token(olivia, perms...))
}

// checkMerge checks that main is at the commit the merge answered, the merge or feature, that a fresh clone gets it,
// that a push over transport api recorded it, and that feature is Merged by Olivia once the push worker processed the
// push.
func checkMerge(t *testing.T, s *stack, tt apiMergeCase, answered code.Merged, main, before, feature string) {
	t.Helper()
	if want := (code.Merged{Base: "main", Head: feature, Commit: main}); answered != want {
		t.Errorf("Merge = %+v, want %+v", answered, want)
	}
	if tt.wantParents == 0 && main != feature {
		t.Errorf("main = %s, want feature's head %s", main, feature)
	}
	if tt.wantParents > 0 {
		var parents []string
		var message, author string
		if err := s.pool.QueryRow(context.Background(), "SELECT parents, message, author_name || ' <' || author_email || '>' FROM commits WHERE repo_id = $1 AND sha = $2",
			website, main).Scan(&parents, &message, &author); err != nil {
			t.Fatalf("reading the merge commit: %v", err)
		}
		if !reflect.DeepEqual(parents, []string{before, feature}) || message != "Merge branch 'feature'\n" || author != "Olivia <olivia@example.com>" {
			t.Errorf("the merge commit has parents %v, message %q, author %s; want %v, the merge's message, Olivia", parents, message, author, []string{before, feature})
		}
	}
	// A Git server with a cache of its own clones main from the bucket, the merge's pack included.
	fresh := clone(t, s.websiteURL(s.serve(t.TempDir()), olivia))
	if got := strings.TrimSpace(mustGit(t, fresh, "rev-parse", "origin/main")); got != main {
		t.Errorf("a fresh clone's main is at %s, want %s", got, main)
	}
	if got := query[int](t, s.pool, "SELECT count(*) FROM pushes WHERE repo_id = $1 AND transport = 'api'", website); got != 1 {
		t.Errorf("%d pushes over api, want the merge's", got)
	}
	s.process()
	if got, want := branchMerge(t, s, "feature"), merged(feature); got != want {
		t.Errorf("feature = %+v, want %+v", got, want)
	}
}

// apiMergeRepo sets up tt's repository, and returns the stack, a client of a Git server's internal call over it,
// feature's head and main's tip.
func apiMergeRepo(t *testing.T, tt apiMergeCase) (*stack, *gitinternal.Client, string, string) {
	t.Helper()
	s, dir, feature := branchToMerge(t, mergeCase{settings: tt.settings, annotate: tt.annotate})
	if tt.featureFile != "" {
		mustGit(t, dir, "reset", "-q", "--hard", "HEAD~1")
		feature = commit(t, dir, tt.featureFile, "the feature's\n")
		mustGit(t, dir, "push", "-q", "--force", "origin", "feature")
	}
	if tt.mainFile != "" {
		mustGit(t, dir, "checkout", "-q", "main")
		commit(t, dir, tt.mainFile, "main's\n")
		mustGit(t, dir, "push", "-q", s.url(s.base, s.token(olivia, "repo.read", "repo.push", "repo.bypass"), "acme/website.git"), "main")
	}
	s.process()
	internal := httptest.NewServer(s.server(t.TempDir()).InternalHandler())
	t.Cleanup(internal.Close)
	base, err := url.Parse(internal.URL)
	if err != nil {
		t.Fatal(err)
	}
	before := query[string](t, s.pool, "SELECT sha FROM refs WHERE repo_id = $1 AND name = 'refs/heads/main'", website)
	return s, gitinternal.NewClient(base), feature, before
}

// sameRefusal reports whether err is want: a *code.MergeError of the same code, or the same error.
func sameRefusal(err, want error) bool {
	var wantRefused, gotRefused *code.MergeError
	switch {
	case want == nil || err == nil:
		return err == nil && want == nil
	case errors.As(want, &wantRefused):
		return errors.As(err, &gotRefused) && gotRefused.Code == wantRefused.Code
	}
	return errors.Is(err, want)
}
