package rest

import (
	"net/http"
	"testing"
	"time"

	"cobranch.dev/internal/services/overview"
)

// onOverview is a path with Acme's figures: 3 tasks and $1.75 on 2 November, website's share of them, and its counts.
func onOverview(path string) func(*orgFixture) string {
	return func(f *orgFixture) string {
		f.overviewStore.Overviews[f.acme.ID] = overview.Overview{Members: 2, Agents: 2, Issues: &overview.Issues{Open: 5, Opened: 1}, Ready: 1, Blocked: 1,
			Working: 1, Queued: 2,
			Days: []overview.Day{{Date: time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC), Figures: overview.Figures{Tasks: 3, Spent: 1_750_000, JobMinutes: 12, Jobs: 2}}},
			ByRepository: []overview.RepositoryFigures{{RepoID: f.website.ID, Repo: "website", Commits: 4, Merged: 1,
				Figures: overview.Figures{Tasks: 1, Spent: 1_500_000, JobMinutes: 12, Jobs: 2}}}}
		return path
	}
}

func TestOrganizationOverview(t *testing.T) {
	const days = "/v1/orgs/acme/overview?from=2026-11-01&to=2026-11-03&zone=Asia/Jerusalem"
	runOrgCases(t, []orgCase{
		{name: "every day of the period, in its zone", viewer: olivia, method: "GET", path: onOverview(days), wantStatus: 200,
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				got := decode[OrganizationOverview](t, body)
				if got.From != "2026-11-01" || got.To != "2026-11-03" || got.Zone != "Asia/Jerusalem" || len(got.Days) != 3 ||
					got.Days[1] != (OverviewDay{Date: "2026-11-02", Tasks: 3, Spent: "1.75", JobMinutes: 12, Jobs: 2}) || got.Days[0].Spent != "0.00" {
					t.Fatalf("overview = %s", body)
				}
				if got.Totals != (OverviewFigures{Tasks: 3, Spent: "1.75", JobMinutes: 12, Jobs: 2}) || got.Members != 2 || got.Repositories != 1 ||
					got.ReadyBranches != 1 || got.BlockedBranches != 1 || got.WorkingAgents != 1 || got.QueuedTasks != 2 ||
					got.Issues == nil || *got.Issues != (OverviewIssues{Open: 5, Opened: 1}) {
					t.Errorf("overview = %s", body)
				}
				want := OverviewRepository{Repository: "website", Commits: 4, Merged: 1, Tasks: 1, Spent: "1.50", JobMinutes: 12, Jobs: 2}
				if len(got.ByRepository) != 1 || got.ByRepository[0] != want {
					t.Errorf("by repository = %+v, want %+v", got.ByRepository, want)
				}
			}},
		{name: "this month so far in UTC by default", viewer: maxim, method: "GET", path: onOverview("/v1/orgs/acme/overview"), wantStatus: 200,
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				today := time.Now().UTC()
				if got := decode[OrganizationOverview](t, body); got.Zone != "UTC" || got.From != today.Format("2006-01")+"-01" ||
					got.To != today.Format("2006-01-02") || len(got.Days) != today.Day() || got.ByRepository == nil {
					t.Errorf("overview = %s", body)
				}
			}},
		{name: "what the caller holds", viewer: olivia, method: "GET", path: onOverview(days), wantStatus: 304,
			headerFor: func(f *orgFixture) http.Header { return held(olivia, onOverview(days))(f) }},
		{name: "what the caller held before the figures changed", viewer: olivia, method: "GET", path: fixed(days), wantStatus: 200,
			headerFor: func(f *orgFixture) http.Header {
				h := held(olivia, fixed(days))(f)
				onOverview(days)(f)
				return h
			}},
		{name: "a period longer than a quarter", viewer: olivia, method: "GET", path: fixed("/v1/orgs/acme/overview?from=2026-08-01&to=2026-12-01"),
			wantStatus: 400, wantCode: "overview.period_invalid"},
		{name: "a zone that isn't one", viewer: olivia, method: "GET", path: fixed(days + "/Mars"), wantStatus: 400, wantCode: "overview.zone_invalid"},
		{name: "as an outsider", viewer: nia, method: "GET", path: fixed(days), wantStatus: 404, wantCode: "organization.not_found"},
	})
}
