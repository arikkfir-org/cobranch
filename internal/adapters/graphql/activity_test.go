package graphql

import (
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

// activityCursorOf is the cursor of website's activity numbered n, a minute after now for each.
func activityCursorOf(n int) string {
	return strconv.FormatInt(now.Add(time.Duration(n)*time.Minute).UnixMicro(), 10) + "." + activityID(n).String()
}

// websiteActivity is what happened in website, oldest first, a minute apart: Olivia pushed feature/login, then merged
// it, then a target delivered main, then Max reviewed feature's head.
func websiteActivity(f *orgFixture) []code.Activity {
	olivia := code.Actor{ID: f.olivia.User.ID, DisplayName: "Olivia", Kind: "user"}
	at := func(minutes int) time.Time { return now.Add(time.Duration(minutes) * time.Minute) }
	return []code.Activity{
		{ID: activityID(1), At: at(1), Kind: code.ActivityPushed, Actor: olivia, RepoID: f.website.ID, Repo: "website", Commits: 2,
			Refs: []git.RefUpdate{{Name: "refs/heads/feature/login", Old: git.ZeroSHA, New: shaOf("c")}}},
		{ID: activityID(2), At: at(2), Kind: code.ActivityMerged, Actor: olivia, RepoID: f.website.ID, Repo: "website",
			Refs:   []git.RefUpdate{{Name: "refs/heads/main", Old: shaOf("b"), New: shaOf("c")}},
			Merged: []git.Head{{Branch: "feature/login", SHA: shaOf("c")}}},
		{ID: activityID(3), At: at(3), Kind: code.ActivityDelivered, Actor: code.Actor{ID: uuid.New(), DisplayName: "production", Kind: "target"},
			RepoID: f.website.ID, Repo: "website", SHA: shaOf("c"),
			Delivery: &code.ActivityDelivery{Number: 4, Kind: code.DeliverCommit, Environment: "main", State: code.DeliverySucceeded}},
		{ID: activityID(4), At: at(4), Kind: code.ActivityReviewed, Actor: code.Actor{ID: f.max.User.ID, DisplayName: "Max", Kind: "user"},
			RepoID: f.website.ID, Repo: "website", SHA: shaOf("d"), Review: code.ReviewApproved, Branch: "feature"},
	}
}

func TestOrganizationActivity(t *testing.T) {
	fields := `activity(first: 4) { activity { kind actor { displayName kind } repository refs { name } commits merged { branch } sha review branch ` +
		`delivery { number kind environment state } } nextCursor }`
	runOrgCases(t, []orgCase{
		{name: "activity, newest first, with what each kind names", viewer: olivia, document: ofAcme(fields), want: []string{
			`{"kind":"REVIEWED","actor":{"displayName":"Max","kind":"user"},"repository":"website","refs":null,"commits":null,"merged":null,"sha":"` +
				shaOf("d") + `","review":"APPROVED","branch":"feature","delivery":null}`,
			`{"kind":"DELIVERED","actor":{"displayName":"production","kind":"target"},"repository":"website","refs":null,"commits":null,"merged":null,"sha":"` +
				shaOf("c") + `","review":null,"branch":null,"delivery":{"number":4,"kind":"DELIVER","environment":"main","state":"SUCCEEDED"}}`,
			`{"kind":"MERGED","actor":{"displayName":"Olivia","kind":"user"},"repository":"website","refs":[{"name":"refs/heads/main"}],"commits":0,"merged":[{"branch":"feature/login"}]`,
			`{"kind":"PUSHED","actor":{"displayName":"Olivia","kind":"user"},"repository":"website","refs":[{"name":"refs/heads/feature/login"}],"commits":2,"merged":null`,
			`"nextCursor":null`}},
		{name: "a page", viewer: olivia, document: ofAcme(`activity(first: 2) { activity { kind } nextCursor }`),
			want: []string{`"activity":[{"kind":"REVIEWED"},{"kind":"DELIVERED"}],"nextCursor":"` + activityCursorOf(3) + `"`}},
		{name: "the next page", viewer: olivia, document: ofAcme(`activity(first: 2, after: "` + activityCursorOf(3) + `") { activity { kind } nextCursor }`),
			want: []string{`"activity":[{"kind":"MERGED"},{"kind":"PUSHED"}],"nextCursor":null`}},
		{name: "activity in repositories the viewer reads", viewer: maxim, document: ofAcme(`activity { activity { kind } }`), want: []string{`"activity":[]`}},
		{name: "activity from a bad cursor", viewer: olivia, document: ofAcme(`activity(after: "x") { activity { kind } }`), wantCode: "BAD_USER_INPUT", wantReason: "cursor.invalid"},
		{name: "activity past a page", viewer: olivia, document: ofAcme(`activity(first: 101) { activity { kind } }`), wantCode: "BAD_USER_INPUT", wantReason: "limit.invalid"},
	})
}

func TestOrganizationAttention(t *testing.T) {
	runOrgCases(t, []orgCase{
		{name: "the branches that need the viewer", viewer: olivia, document: ofAcme(`attention { repository need branch { name status commit { sha } } }`),
			want: []string{`"attention":[{"repository":"website","need":"FIX","branch":{"name":"feature","status":"BLOCKED","commit":{"sha":"` + shaOf("d") + `"}}},` +
				`{"repository":"website","need":"WATCH","branch":{"name":"feature/login","status":"PROCESSING","commit":{"sha":"` + shaOf("c") + `"}}}]`}},
		{name: "none for one who reads no repository", viewer: maxim, document: ofAcme(`attention { need }`), want: []string{`"attention":[]`}},
	})
}
