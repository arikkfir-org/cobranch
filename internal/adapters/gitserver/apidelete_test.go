package gitserver_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"cobranch.dev/internal/adapters/gitinternal"
	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/repos"
)

// requireFeature is a /cobranch.yaml that names feature, so deleting it breaks its requirements.
const requireFeature = "branches:\n  feature:\n    requires:\n      annotations: [ci/build]\n"

// apiDeleteCase sets up main and branch feature a commit ahead of it, perhaps merged into main through the internal
// call first (and processed, unless mergeLate), perhaps named by main's /cobranch.yaml; and asks the Git server, on a
// token of Olivia's that holds perms (repo.read and repo.push, and repo.bypass when bypass, if none) or on none when
// anonymous, to delete feature at the head head names, for Olivia or for someone else, past its requirements when
// bypass. It expects the deletion refused with wantErr, or feature deleted by Olivia, its merge kept when it merged.
type apiDeleteCase struct {
	name        string
	settings    string
	merge       bool
	mergeLate   bool
	head        func(feature string) string
	bypass      bool
	perms       []string
	anonymous   bool
	someoneElse bool
	wantErr     error
}

func apiDeleteCases() []apiDeleteCase {
	return []apiDeleteCase{
		{name: "a merged branch", merge: true},
		{name: "a branch merged and deleted before the push worker processed the merge", merge: true, mergeLate: true},
		{name: "a branch with commits main lacks", merge: false},
		{name: "a head the branch moved from", head: func(string) string { return strings.Repeat("a", 40) }, wantErr: code.ErrBranchMoved},
		{name: "a branch /cobranch.yaml names, past its requirements", settings: requireFeature, bypass: true},
		{name: "a branch /cobranch.yaml names, without bypassing them", settings: requireFeature,
			perms: []string{"repo.read", "repo.push", "repo.bypass"}, wantErr: &code.DeleteError{Code: code.DeleteRefused}},
		{name: "past its requirements without repo.bypass", settings: requireFeature, bypass: true, perms: []string{"repo.read", "repo.push"},
			wantErr: auth.ErrForbidden},
		{name: "a viewer without repo.push", perms: []string{"repo.read", "repo.merge"}, wantErr: auth.ErrForbidden},
		{name: "a deletion for someone besides the viewer", someoneElse: true, wantErr: auth.ErrForbidden},
		{name: "no viewer finds no repository", anonymous: true, wantErr: repos.ErrNotFound},
	}
}

func TestDeleteBranchByAPI(t *testing.T) {
	for _, tt := range apiDeleteCases() {
		t.Run(tt.name, func(t *testing.T) {
			s, client, feature := apiDeleteRepo(t, tt)
			head := feature
			if tt.head != nil {
				head = tt.head(feature)
			}
			by := olivia
			if tt.someoneElse {
				by = uuid.New()
			}
			err := client.DeleteBranch(tt.asker(t, s), website, code.DeleteRequest{Branch: "feature", Head: head, By: by, Bypass: tt.bypass})
			if !sameDeleteRefusal(err, tt.wantErr) {
				t.Fatalf("DeleteBranch = %v, want %v", err, tt.wantErr)
			}
			refs := query[int](t, s.pool, "SELECT count(*) FROM refs WHERE repo_id = $1 AND name = 'refs/heads/feature'", website)
			if tt.wantErr != nil {
				if refs != 1 {
					t.Errorf("feature's ref is gone, want it kept")
				}
				return
			}
			checkDeleted(t, s, tt, refs, feature)
		})
	}
}

// asker is the context of tt's deletion: one that carries a token of Olivia's holding tt's permissions, or none.
func (tt apiDeleteCase) asker(t *testing.T, s *stack) context.Context {
	t.Helper()
	if tt.anonymous {
		return t.Context()
	}
	perms := tt.perms
	if perms == nil {
		perms = []string{"repo.read", "repo.push"}
		if tt.bypass {
			perms = append(perms, "repo.bypass")
		}
	}
	return auth.WithToken(t.Context(), s.token(olivia, perms...))
}

// checkDeleted checks that feature's ref is gone, that a push over transport api recorded the deletion, that its row
// names Olivia as its deleter once the push worker processed the push, keeping its merge when it merged, and that a
// bypass of its requirements was recorded.
func checkDeleted(t *testing.T, s *stack, tt apiDeleteCase, refs int, feature string) {
	t.Helper()
	if refs != 0 {
		t.Errorf("feature's ref is still there")
	}
	wantPushes := 1
	if tt.merge {
		wantPushes = 2
	}
	if got := query[int](t, s.pool, "SELECT count(*) FROM pushes WHERE repo_id = $1 AND transport = 'api'", website); got != wantPushes {
		t.Errorf("%d pushes over api, want %d", got, wantPushes)
	}
	s.process()
	var deletedBy string
	var mergedHead *string
	if err := s.pool.QueryRow(t.Context(), "SELECT deleted_by::text, merged_head FROM branches WHERE repo_id = $1 AND name = 'feature' AND deleted_at IS NOT NULL",
		website).Scan(&deletedBy, &mergedHead); err != nil {
		t.Fatalf("reading the deleted branch: %v", err)
	}
	if deletedBy != olivia.String() {
		t.Errorf("feature was deleted by %s, want Olivia", deletedBy)
	}
	switch {
	case tt.merge && (mergedHead == nil || *mergedHead != feature):
		t.Errorf("feature's merge = %v, want it kept at %s", mergedHead, feature)
	case !tt.merge && mergedHead != nil:
		t.Errorf("feature's merge = %s, want none", *mergedHead)
	}
	bypassed := query[int](t, s.pool, "SELECT count(*) FROM events WHERE kind = 'branch.bypassed' AND subject_id = $1", website)
	if want := map[bool]int{true: 1, false: 0}[tt.settings != ""]; bypassed != want {
		t.Errorf("%d bypasses recorded, want %d", bypassed, want)
	}
}

// apiDeleteRepo sets up tt's repository, and returns the stack, a client of a Git server's internal call over it and
// feature's head.
func apiDeleteRepo(t *testing.T, tt apiDeleteCase) (*stack, *gitinternal.Client, string) {
	t.Helper()
	s, client, feature, _ := apiMergeRepo(t, apiMergeCase{})
	if tt.settings != "" {
		dir := clone(t, s.websiteURL(s.base, olivia))
		mustGit(t, dir, "checkout", "-q", "main")
		commit(t, dir, "cobranch.yaml", tt.settings)
		mustGit(t, dir, "push", "-q", "origin", "main")
		s.process()
	}
	if tt.merge {
		asker := auth.WithToken(t.Context(), s.token(olivia, "repo.read", "repo.merge"))
		if _, err := client.Merge(asker, website, code.MergeRequest{Branch: "feature", Head: feature, Base: "main", Method: code.MethodFastForward,
			By: code.Merger{ID: olivia, Name: "Olivia", Email: "olivia@example.com"}}); err != nil {
			t.Fatalf("merging feature: %v", err)
		}
		if !tt.mergeLate {
			s.process()
		}
	}
	return s, client, feature
}

// sameDeleteRefusal reports whether err is want: a *code.DeleteError of the same code, or the same error.
func sameDeleteRefusal(err, want error) bool {
	var wantRefused, gotRefused *code.DeleteError
	switch {
	case want == nil || err == nil:
		return err == nil && want == nil
	case errors.As(want, &wantRefused):
		return errors.As(err, &gotRefused) && gotRefused.Code == wantRefused.Code
	}
	return errors.Is(err, want)
}
