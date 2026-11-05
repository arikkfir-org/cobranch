package graphql

import (
	"testing"
	"time"

	"cobranch.dev/internal/services/overview"
)

// onOverview is a document with Acme's figures: 3 tasks and $1.75 on 2 November, website's share of them, and its
// counts.
func onOverview(document func(*orgFixture) string) func(*orgFixture) string {
	return func(f *orgFixture) string {
		f.overviewStore.Overviews[f.acme.ID] = overview.Overview{Members: 2, Agents: 2, Issues: &overview.Issues{Open: 5, Opened: 1}, Ready: 1, Blocked: 1,
			Working: 1, Queued: 2,
			Days: []overview.Day{{Date: time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC), Figures: overview.Figures{Tasks: 3, Spent: 1_750_000, JobMinutes: 12, Jobs: 2}}},
			ByRepository: []overview.RepositoryFigures{{RepoID: f.website.ID, Repo: "website", Commits: 4, Merged: 1,
				Figures: overview.Figures{Tasks: 1, Spent: 1_500_000, JobMinutes: 12, Jobs: 2}}}}
		return document(f)
	}
}

func TestOverviewQueries(t *testing.T) {
	figures := `tasks spent jobMinutes jobs`
	ask := func(args string) func(*orgFixture) string {
		return ofAcme(`overview` + args + ` { from to zone members repositories agents issues { open opened } readyBranches blockedBranches workingAgents ` +
			`queuedTasks totals { ` + figures + ` } days { date ` + figures + ` } byRepository { repository commits merged ` + figures + ` } }`)
	}
	days := `(from: "2026-11-01", to: "2026-11-03", zone: "Asia/Jerusalem")`
	runOrgCases(t, []orgCase{
		{name: "every day of the period, in its zone", viewer: olivia, document: onOverview(ask(days)),
			want: []string{`"from":"2026-11-01","to":"2026-11-03","zone":"Asia/Jerusalem","members":2,"repositories":1,"agents":2,"issues":{"open":5,"opened":1}`,
				`"readyBranches":1,"blockedBranches":1,"workingAgents":1,"queuedTasks":2,"totals":{"tasks":3,"spent":"1.75","jobMinutes":12,"jobs":2}`,
				`"days":[{"date":"2026-11-01","tasks":0,"spent":"0.00","jobMinutes":0,"jobs":0},{"date":"2026-11-02","tasks":3,"spent":"1.75","jobMinutes":12,"jobs":2},`,
				`"byRepository":[{"repository":"website","commits":4,"merged":1,"tasks":1,"spent":"1.50","jobMinutes":12,"jobs":2}]`}},
		{name: "a period longer than a quarter", viewer: olivia, document: ask(`(from: "2026-08-01", to: "2026-12-01")`), wantCode: "BAD_USER_INPUT",
			wantReason: "overview.period_invalid"},
		{name: "a zone that isn't one", viewer: olivia, document: ask(`(zone: "Mars/Olympus")`), wantCode: "BAD_USER_INPUT", wantReason: "overview.zone_invalid"},
		{name: "as an outsider", viewer: nia, document: ask(``), want: []string{`"organization":null`}},
	})
}
