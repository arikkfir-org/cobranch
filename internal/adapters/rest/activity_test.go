package rest

import (
	"net/http"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/git"
)

// activityID is the ID of website's activity numbered n.
func activityID(n int) uuid.UUID {
	return uuid.MustParse("00000000-0000-7000-8000-00000000a00" + strconv.Itoa(n))
}

// websiteActivity is what happened in website, oldest first, a minute apart: Olivia pushed feature/login, then merged
// it, then a target delivered main, then Max reviewed feature's head.
func websiteActivity(f *orgFixture) []code.Activity {
	olivia := code.Actor{ID: f.olivia.User.ID, DisplayName: "Olivia", Kind: "user"}
	at := func(minutes int) time.Time { return now.Add(time.Duration(minutes) * time.Minute) }
	return []code.Activity{
		{ID: activityID(1), At: at(1), Kind: code.ActivityPushed, Actor: olivia, RepoID: f.website.ID, Repo: "website", Commits: 2,
			Refs: []git.RefUpdate{{Name: "refs/heads/feature/login", Old: git.ZeroSHA, New: shaOf("c")}}},
		{ID: activityID(2), At: at(2), Kind: code.ActivityMerged, Actor: olivia, RepoID: f.website.ID, Repo: "website", Commits: 0,
			Refs:   []git.RefUpdate{{Name: "refs/heads/main", Old: shaOf("b"), New: shaOf("c")}},
			Merged: []git.Head{{Branch: "feature/login", SHA: shaOf("c")}}},
		{ID: activityID(3), At: at(3), Kind: code.ActivityDelivered, Actor: code.Actor{ID: uuid.New(), DisplayName: "production", Kind: "target"},
			RepoID: f.website.ID, Repo: "website", SHA: shaOf("c"),
			Delivery: &code.ActivityDelivery{Number: 4, Kind: code.DeliverCommit, Environment: "main", State: code.DeliverySucceeded}},
		{ID: activityID(4), At: at(4), Kind: code.ActivityReviewed, Actor: code.Actor{ID: f.max.User.ID, DisplayName: "Max", Kind: "user"},
			RepoID: f.website.ID, Repo: "website", SHA: shaOf("d"), Review: code.ReviewApproved, Branch: "feature"},
	}
}

func wantActivity(want ...string) func(*testing.T, *orgFixture, []byte) {
	return func(t *testing.T, _ *orgFixture, body []byte) {
		var got []string
		for _, a := range decode[struct{ Activity []Activity }](t, body).Activity {
			got = append(got, string(a.Kind))
		}
		if !slices.Equal(got, want) {
			t.Errorf("activity = %v, want %v", got, want)
		}
	}
}

func TestOrganizationActivity(t *testing.T) {
	runOrgCases(t, []orgCase{
		{name: "list, newest first, with what each kind names", viewer: olivia, method: "GET", path: fixed("/v1/orgs/acme/activity"), wantStatus: 200,
			check: func(t *testing.T, f *orgFixture, body []byte) {
				wantActivity("reviewed", "delivered", "merged", "pushed")(t, f, body)
				list := decode[struct{ Activity []Activity }](t, body).Activity
				reviewed, delivered, merged, pushed := list[0], list[1], list[2], list[3]
				if reviewed.Actor.Id != f.max.User.ID || reviewed.Actor.Kind != "user" || reviewed.Repository != "website" || reviewed.Sha == nil ||
					*reviewed.Sha != shaOf("d") || reviewed.Review == nil || *reviewed.Review != "approved" || reviewed.Branch == nil ||
					*reviewed.Branch != "feature" || reviewed.Refs != nil || reviewed.Delivery != nil {
					t.Errorf("a review = %+v", reviewed)
				}
				if d := delivered.Delivery; d == nil || d.Number != 4 || d.Kind != "deliver" || d.Environment != "main" || d.State != "succeeded" ||
					delivered.Actor.DisplayName != "production" || delivered.Actor.Kind != "target" || delivered.Sha == nil || *delivered.Sha != shaOf("c") {
					t.Errorf("a delivery = %+v", delivered)
				}
				if merged.Merged == nil || len(*merged.Merged) != 1 || (*merged.Merged)[0].Branch != "feature/login" || merged.Refs == nil ||
					(*merged.Refs)[0].Name != "refs/heads/main" {
					t.Errorf("a merge = %+v", merged)
				}
				if pushed.Refs == nil || len(*pushed.Refs) != 1 || (*pushed.Refs)[0].Old != git.ZeroSHA || pushed.Commits == nil || *pushed.Commits != 2 ||
					pushed.Merged != nil || pushed.Review != nil || !pushed.At.Equal(now.Add(time.Minute)) {
					t.Errorf("a push = %+v", pushed)
				}
			}},
		{name: "list a page", viewer: olivia, method: "GET", path: fixed("/v1/orgs/acme/activity?limit=2"), wantStatus: 200,
			check:      wantActivity("reviewed", "delivered"),
			wantHeader: http.Header{"Link": {`<?cursor=` + strconv.FormatInt(now.Add(3*time.Minute).UnixMicro(), 10) + "." + activityID(3).String() + `&limit=2>; rel="next"`}}},
		{name: "list the next page", viewer: olivia, method: "GET", wantStatus: 200, check: wantActivity("merged", "pushed"),
			path: fixed("/v1/orgs/acme/activity?limit=2&cursor=" + strconv.FormatInt(now.Add(3*time.Minute).UnixMicro(), 10) + "." + activityID(3).String())},
		{name: "list leaves out repositories the viewer doesn't read", viewer: maxim, method: "GET", path: fixed("/v1/orgs/acme/activity"), wantStatus: 200,
			check: wantActivity()},
		{name: "list from a bad cursor", viewer: olivia, method: "GET", path: fixed("/v1/orgs/acme/activity?cursor=x"), wantStatus: 400, wantCode: "cursor.invalid"},
		{name: "list from a cursor without an ID", viewer: olivia, method: "GET", path: fixed("/v1/orgs/acme/activity?cursor=12.x"), wantStatus: 400, wantCode: "cursor.invalid"},
		{name: "list too long a page", viewer: olivia, method: "GET", path: fixed("/v1/orgs/acme/activity?limit=101"), wantStatus: 400},
		{name: "list as an outsider", viewer: nia, method: "GET", path: fixed("/v1/orgs/acme/activity"), wantStatus: 404, wantCode: "organization.not_found"},
	})
}

func TestOrganizationAttention(t *testing.T) {
	runOrgCases(t, []orgCase{
		{name: "list the branches that need the viewer", viewer: olivia, method: "GET", path: fixed("/v1/orgs/acme/attention"), wantStatus: 200,
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				var got []string
				for _, b := range decode[struct{ Branches []AttentionBranch }](t, body).Branches {
					got = append(got, string(b.Need)+" "+b.Repository+" "+b.Branch.Name)
					if b.Branch.Status == nil || b.Branch.Commit == nil {
						t.Errorf("branch %s = %+v, want its status and head commit", b.Branch.Name, b.Branch)
					}
				}
				if want := []string{"fix website feature", "watch website feature/login"}; !slices.Equal(got, want) {
					t.Errorf("attention = %q, want %q", got, want)
				}
			}},
		{name: "list as one who reads no repository", viewer: maxim, method: "GET", path: fixed("/v1/orgs/acme/attention"), wantStatus: 200,
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				if got := decode[struct{ Branches []AttentionBranch }](t, body).Branches; len(got) != 0 {
					t.Errorf("attention = %+v, want none", got)
				}
			}},
		{name: "list as an outsider", viewer: nia, method: "GET", path: fixed("/v1/orgs/acme/attention"), wantStatus: 404, wantCode: "organization.not_found"},
	})
}
