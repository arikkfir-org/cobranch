package code_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/code/codetest"
	"cobranch.dev/internal/services/git"
	"cobranch.dev/internal/services/orgs"
	"cobranch.dev/internal/services/orgs/orgstest"
)

// attentionFixture is an organization with two repositories: website, where Max is a Developer (who merges and
// reviews), and handbook, which Max only reads. Ann writes the other heads; Nia is no member.
type attentionFixture struct {
	store              *codetest.Store
	grants             *orgstest.Store
	service            *code.Service
	org, website, book uuid.UUID
	max, nia           code.Viewer
	ann                uuid.UUID
	n                  int
}

func newAttentionFixture() *attentionFixture {
	f := &attentionFixture{store: codetest.NewStore(), grants: orgstest.NewStore(), org: uuid.New(), website: uuid.New(), book: uuid.New(),
		max: code.Viewer{Principal: auth.Principal{ID: uuid.New(), Kind: auth.KindUser, DisplayName: "Max"}},
		nia: code.Viewer{Principal: auth.Principal{ID: uuid.New(), Kind: auth.KindUser, DisplayName: "Nia"}}, ann: uuid.New()}
	f.grants.Repos[f.website], f.grants.Repos[f.book] = f.org, f.org
	f.store.SlugList[f.website], f.store.SlugList[f.book] = [2]string{"acme", "website"}, [2]string{"acme", "handbook"}
	f.grants.Grant(f.max.ID, auth.RoleMember, auth.ScopeOrganization, f.org)
	f.grants.Grant(f.max.ID, auth.RoleDeveloper, auth.ScopeRepository, f.website)
	f.grants.Grant(f.max.ID, auth.RoleReader, auth.ScopeRepository, f.book)
	f.service = code.NewService(f.store, codetest.NewObjects(), &codetest.Renderer{}, auth.NewAuthorizer(f.grants))
	return f
}

// branch adds a branch to the repository at a commit by author, standing as status with checks, moved minutes ago.
func (f *attentionFixture) branch(repoID uuid.UUID, name string, author uuid.UUID, status code.BranchStatus, minutes int, checks ...code.Check) string {
	f.n++
	head := fmt.Sprintf("%040x", f.n)
	push := code.Push{ID: uuid.New(), Pusher: &code.Principal{ID: author}, Transport: git.TransportHTTPS, At: now}
	f.store.Add(repoID, push, code.Commit{SHA: head, Author: code.Person{Name: "A", Principal: &code.Principal{ID: author}}})
	f.store.SetBranch(repoID, name, head)
	f.store.UpdateBranch(repoID, name, func(b *code.Branch) {
		b.Status, b.Checks, b.Base, b.UpdatedAt = status, checks, git.DefaultBranch, now.Add(-time.Duration(minutes)*time.Minute)
	})
	return head
}

// unapproved is an approvals check that waits for one more approval.
var unapproved = code.Check{Kind: code.CheckApprovals, Count: 0, Required: 1}

// attentionCase is a case of TestAttention: the branches setup makes, the viewer, how the store fails, and what
// Attention answers: each branch as its need, repository and name.
type attentionCase struct {
	name    string
	setup   func(f *attentionFixture)
	viewer  func(f *attentionFixture) code.Viewer
	fail    error
	want    []string
	wantErr error
}

// TestAttention checks which branches need a viewer, why, and in what order.
func TestAttention(t *testing.T) {
	t.Parallel()
	tests := []attentionCase{
		{name: "a Blocked head of one's own, then a review one may give, then a merge, then one's own Processing head",
			setup: func(f *attentionFixture) {
				f.branch(f.website, "wip/mine", f.max.ID, code.StatusProcessing, 1)
				f.branch(f.website, "agent/ready", f.ann, code.StatusReady, 2)
				f.branch(f.website, "feature/theirs", f.ann, code.StatusBlocked, 3, unapproved)
				f.branch(f.website, "fix/old", f.max.ID, code.StatusBlocked, 30)
				f.branch(f.book, "fix/notes", f.max.ID, code.StatusBlocked, 5)
			},
			want: []string{"fix handbook fix/notes", "fix website fix/old", "review website feature/theirs", "merge website agent/ready", "watch website wip/mine"}},
		{name: "others' Processing and Blocked heads need no one but their author",
			setup: func(f *attentionFixture) {
				f.branch(f.website, "wip/theirs", f.ann, code.StatusProcessing, 1)
				f.branch(f.website, "broken/theirs", f.ann, code.StatusBlocked, 2, code.Check{Kind: code.CheckAnnotation, Name: "ci/test", Status: code.AnnotationFailed})
			}},
		{name: "a repository one only reads asks for no merge and no review",
			setup: func(f *attentionFixture) {
				f.branch(f.book, "ready/theirs", f.ann, code.StatusReady, 1)
				f.branch(f.book, "blocked/theirs", f.ann, code.StatusBlocked, 2, unapproved)
			}},
		{name: "a head one reviewed, or wrote, waits for someone else's approval",
			setup: func(f *attentionFixture) {
				head := f.branch(f.website, "feature/reviewed", f.ann, code.StatusBlocked, 1, unapproved)
				f.store.ReviewList[f.website] = append(f.store.ReviewList[f.website],
					code.Review{ID: uuid.New(), SHA: head, State: code.ReviewChangesRequested, Reviewer: &code.Principal{ID: f.max.ID}, Person: true})
				f.branch(f.website, "feature/mine", f.max.ID, code.StatusProcessing, 2, unapproved)
			},
			want: []string{"watch website feature/mine"}},
		{name: "merged and empty branches need no one",
			setup: func(f *attentionFixture) {
				f.branch(f.website, "done", f.ann, code.StatusMerged, 1)
				f.branch(f.website, "nothing", f.max.ID, code.StatusEmpty, 2)
			}},
		{name: "one who doesn't read the organization finds none", viewer: func(f *attentionFixture) code.Viewer { return f.nia }, wantErr: orgs.ErrNotFound},
		{name: "the store's failure", fail: errors.New("down"), wantErr: errors.New("down")},
	}
	for _, tt := range tests {
		t.Run(tt.name, tt.run)
	}
}

// run checks the case, as Max unless it names another viewer.
func (tt attentionCase) run(t *testing.T) {
	t.Parallel()
	f := newAttentionFixture()
	if tt.setup != nil {
		tt.setup(f)
	}
	f.store.Fail = tt.fail
	v := f.max
	if tt.viewer != nil {
		v = tt.viewer(f)
	}
	got, err := f.service.Attention(context.Background(), v, f.org)
	if !sameError(err, tt.wantErr) {
		t.Fatalf("Attention() error = %v, want %v", err, tt.wantErr)
	}
	var names []string
	for _, a := range got {
		names = append(names, string(a.Need)+" "+a.Repo+" "+a.Name)
	}
	if fmt.Sprint(names) != fmt.Sprint(tt.want) {
		t.Errorf("Attention() = %q, want %q", names, tt.want)
	}
}
