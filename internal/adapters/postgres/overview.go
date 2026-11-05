package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"cobranch.dev/internal/adapters/postgres/queries"
	"cobranch.dev/internal/services/agents"
	"cobranch.dev/internal/services/overview"
)

// OverviewStore reads an organization's figures from its tasks, their spend, its job runs, pushes, branches and issues.
type OverviewStore struct {
	q *queries.Queries
}

// NewOverviewStore returns the overview's store on pool's database.
func NewOverviewStore(pool *pgxpool.Pool) *OverviewStore {
	return &OverviewStore{q: queries.New(pool)}
}

var _ overview.Store = (*OverviewStore)(nil)

// Overview reads how the organization's work stands and what it did over p: its counts, the days of p that saw
// anything and the repositories of repoIDs that did, costliest first (overview.Store).
func (s *OverviewStore) Overview(ctx context.Context, orgID uuid.UUID, repoIDs []uuid.UUID, p overview.Period) (overview.Overview, error) {
	since, until := p.Since(), p.Until()
	c, err := s.q.OverviewCounts(ctx, queries.OverviewCountsParams{OrgID: orgID, Since: since, Until: until, RepoIds: repoIDs})
	if err != nil {
		return overview.Overview{}, fmt.Errorf("counting the figures of organization %s: %w", orgID, err)
	}
	out := overview.Overview{Members: c.Members, Agents: c.Agents, Issues: &overview.Issues{Open: c.OpenIssues, Opened: c.OpenedIssues},
		Ready: c.Ready, Blocked: c.Blocked, Working: c.Working, Queued: c.Queued}
	days, err := s.q.OverviewDays(ctx, queries.OverviewDaysParams{Zone: p.Zone, OrgID: orgID, Since: since, Until: until})
	if err != nil {
		return overview.Overview{}, fmt.Errorf("summing the days of organization %s: %w", orgID, err)
	}
	for _, d := range days {
		out.Days = append(out.Days, overview.Day{Date: time.Date(d.Day.Time.Year(), d.Day.Time.Month(), d.Day.Time.Day(), 0, 0, 0, 0, p.From.Location()),
			Figures: overview.Figures{Tasks: d.Tasks, Spent: agents.USD(d.Spent), JobMinutes: d.JobMinutes, Jobs: d.Jobs}})
	}
	repos, err := s.q.OverviewRepositories(ctx, queries.OverviewRepositoriesParams{OrgID: orgID, Since: since, Until: until, RepoIds: repoIDs})
	if err != nil {
		return overview.Overview{}, fmt.Errorf("summing the repositories of organization %s: %w", orgID, err)
	}
	for _, r := range repos {
		out.ByRepository = append(out.ByRepository, overview.RepositoryFigures{RepoID: r.ID, Repo: r.Slug, Commits: r.Commits, Merged: r.Merged,
			Figures: overview.Figures{Tasks: r.Tasks, Spent: agents.USD(r.Spent), JobMinutes: r.JobMinutes, Jobs: r.Jobs}})
	}
	return out, nil
}
