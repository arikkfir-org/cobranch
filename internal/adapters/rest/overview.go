package rest

import (
	"context"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/overview"
)

// Overview is the service of an organization's figures, as the API uses it.
type Overview interface {
	Overview(ctx context.Context, v overview.Viewer, orgID uuid.UUID, p overview.Period) (overview.Overview, error)
}

// GetOrganizationOverview answers how the organization's work stands and what it did over a span of days in a time
// zone, this month so far in UTC by default, or 304 while the caller holds that answer.
func (a api) GetOrganizationOverview(ctx context.Context, req GetOrganizationOverviewRequestObject) (GetOrganizationOverviewResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	org, err := a.org(ctx, v, req.Org)
	if err != nil {
		return nil, err
	}
	p, err := overview.PeriodIn(orEmpty(req.Params.From), orEmpty(req.Params.To), orEmpty(req.Params.Zone), time.Now())
	if err != nil {
		return nil, fail(err, "organization")
	}
	o, err := a.overview.Overview(ctx, v, org.ID, p)
	if err != nil {
		return nil, fail(err, "organization")
	}
	body := organizationOverview(o)
	tag, err := bodyTag("overview", body, nil)
	if err != nil {
		return nil, err
	}
	if matches(req.Params.IfNoneMatch, tag) {
		return GetOrganizationOverview304Response{Headers: GetOrganizationOverview304ResponseHeaders{ETag: tag}}, nil
	}
	return GetOrganizationOverview200JSONResponse{Body: body, Headers: GetOrganizationOverview200ResponseHeaders{ETag: tag}}, nil
}

// organizationOverview is o as the API shows it, its days written in the period's zone.
func organizationOverview(o overview.Overview) OrganizationOverview {
	out := OrganizationOverview{From: day(o.Period.From), To: day(o.Period.To), Zone: o.Period.Zone, Members: o.Members, Repositories: o.Repositories,
		Agents: o.Agents, ReadyBranches: o.Ready, BlockedBranches: o.Blocked, WorkingAgents: o.Working, QueuedTasks: o.Queued,
		Totals: OverviewFigures{Tasks: o.Totals.Tasks, Spent: usd(o.Totals.Spent), JobMinutes: o.Totals.JobMinutes, Jobs: o.Totals.Jobs},
		Days: mapAll(o.Days, func(d overview.Day) OverviewDay {
			return OverviewDay{Date: day(d.Date), Tasks: d.Tasks, Spent: usd(d.Spent), JobMinutes: d.JobMinutes, Jobs: d.Jobs}
		}),
		ByRepository: mapAll(o.ByRepository, func(r overview.RepositoryFigures) OverviewRepository {
			return OverviewRepository{Repository: r.Repo, Commits: r.Commits, Merged: r.Merged, Tasks: r.Tasks, Spent: usd(r.Spent),
				JobMinutes: r.JobMinutes, Jobs: r.Jobs}
		})}
	if o.Issues != nil {
		out.Issues = &OverviewIssues{Open: o.Issues.Open, Opened: o.Issues.Opened}
	}
	return out
}

// day is the date t falls on in its own zone, such as 2026-11-05.
func day(t time.Time) string { return t.Format("2006-01-02") }
