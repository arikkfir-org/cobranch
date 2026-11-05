package graphql

import (
	"context"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/overview"
)

// Overview is the service of an organization's figures, as GraphQL uses it.
type Overview interface {
	Overview(ctx context.Context, v overview.Viewer, orgID uuid.UUID, p overview.Period) (overview.Overview, error)
}

// organizationOverview is o as the API shows it, its days written in the period's zone.
func organizationOverview(o overview.Overview) *OrganizationOverview {
	out := &OrganizationOverview{From: day(o.Period.From), To: day(o.Period.To), Zone: o.Period.Zone, Members: o.Members, Repositories: o.Repositories,
		Agents: o.Agents, ReadyBranches: o.Ready, BlockedBranches: o.Blocked, WorkingAgents: o.Working, QueuedTasks: o.Queued,
		Totals: &OverviewFigures{Tasks: o.Totals.Tasks, Spent: o.Totals.Spent.String(), JobMinutes: o.Totals.JobMinutes, Jobs: o.Totals.Jobs},
		Days: mapAll(o.Days, func(d overview.Day) *OverviewDay {
			return &OverviewDay{Date: day(d.Date), Tasks: d.Tasks, Spent: d.Spent.String(), JobMinutes: d.JobMinutes, Jobs: d.Jobs}
		}),
		ByRepository: mapAll(o.ByRepository, func(r overview.RepositoryFigures) *OverviewRepository {
			return &OverviewRepository{Repository: r.Repo, Commits: r.Commits, Merged: r.Merged, Tasks: r.Tasks, Spent: r.Spent.String(),
				JobMinutes: r.JobMinutes, Jobs: r.Jobs}
		})}
	if o.Issues != nil {
		out.Issues = &OverviewIssues{Open: o.Issues.Open, Opened: o.Issues.Opened}
	}
	return out
}

// day is the date t falls on in its own zone, such as 2026-11-05.
func day(t time.Time) string { return t.Format("2006-01-02") }
