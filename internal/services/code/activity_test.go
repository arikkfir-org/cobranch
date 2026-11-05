package code_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/orgs"
)

// TestActivity checks that a page of an organization's activity holds what happened in the repositories its viewer
// reads, newest first, and where the next page starts.
func TestActivity(t *testing.T) {
	t.Parallel()
	secret, boom := uuid.New(), errors.New("boom")
	// seed records activity in website, handbook and a repository Max doesn't read, a minute apart, as names say.
	seed := func(f *attentionFixture) {
		f.grants.Repos[secret] = f.org
		for i, r := range []uuid.UUID{f.website, secret, f.book, f.website, f.book} {
			f.store.ActivityList[f.org] = append(f.store.ActivityList[f.org], code.Activity{ID: uuid.New(), At: now.Add(time.Duration(i) * time.Minute),
				Kind: code.ActivityPushed, RepoID: r, Repo: map[uuid.UUID]string{f.website: "website", f.book: "handbook", secret: "secret"}[r]})
		}
	}
	tests := []struct {
		name     string
		viewer   func(f *attentionFixture) code.Viewer
		before   int
		limit    int
		fail     error
		want     []string
		wantNext int
		wantErr  error
	}{
		{name: "the repositories the viewer reads, newest first", limit: 0, want: []string{"handbook", "website", "handbook", "website"}, wantNext: -1},
		{name: "a page, and where the next starts", limit: 2, want: []string{"handbook", "website"}, wantNext: 2},
		{name: "the next page", before: 2, limit: 2, want: []string{"handbook", "website"}, wantNext: -1},
		{name: "one who reads no repository", viewer: func(f *attentionFixture) code.Viewer {
			v := code.Viewer{Principal: auth.Principal{ID: uuid.New(), Kind: auth.KindUser}}
			f.grants.Grant(v.ID, auth.RoleMember, auth.ScopeOrganization, f.org)
			return v
		}, wantNext: -1},
		{name: "no member", viewer: func(f *attentionFixture) code.Viewer { return f.nia }, wantErr: orgs.ErrNotFound},
		{name: "a page too long", limit: code.MaxActivityPage + 1, wantErr: invalid("limit.invalid")},
		{name: "the store failing", fail: boom, wantErr: boom},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newAttentionFixture()
			seed(f)
			f.store.Fail = tt.fail
			v := f.max
			if tt.viewer != nil {
				v = tt.viewer(f)
			}
			// before and wantNext count from the newest: the cursor of the activity at that place in Max's list.
			newest := slices.Clone(f.store.ActivityList[f.org])
			slices.Reverse(newest)
			newest = slices.DeleteFunc(newest, func(a code.Activity) bool { return a.RepoID == secret })
			got, err := f.service.Activity(context.Background(), v, f.org, cursorAt(newest, tt.before), tt.limit)
			if !sameError(err, tt.wantErr) {
				t.Fatalf("Activity() = %v, want %v", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			if repos := activityRepos(got.Activity); !slices.Equal(repos, tt.want) {
				t.Errorf("Activity() = %q, want %q", repos, tt.want)
			}
			if want := cursorAt(newest, tt.wantNext); got.Next != want {
				t.Errorf("Activity().Next = %+v, want %+v", got.Next, want)
			}
		})
	}
}

// cursorAt is the cursor of the nth of list, counted from 1, or none for n below 1.
func cursorAt(list []code.Activity, n int) code.ActivityCursor {
	if n < 1 {
		return code.ActivityCursor{}
	}
	return code.ActivityCursor{At: list[n-1].At, ID: list[n-1].ID}
}

// activityRepos are the repositories of list, in its order.
func activityRepos(list []code.Activity) []string {
	var out []string
	for _, a := range list {
		out = append(out, a.Repo)
	}
	return out
}
