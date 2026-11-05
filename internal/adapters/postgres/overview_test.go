package postgres

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/overview"
)

// overviewDay is the day of 2026 the overview's figures fall on, in UTC: far from the fixture's own now, so that only
// what seedOverview dates lands in the periods asked.
var overviewDay = time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC)

// seedOverview gives Acme, on overviewDay: a task of Scout's on website, running since 10:00, which spent $1.50 at
// 11:00; a job run on website from 12:00 for 90 seconds; website's pushes, at 09:00, one of which merged a branch; an
// issue opened at 08:00; feature Ready and stray Blocked. The evening before, at 23:30, it queued a task off any
// repository, which spent $0.25 at 23:40, and two days before closed an issue.
func seedOverview(t *testing.T, f *agentFixture) {
	t.Helper()
	ctx := context.Background()
	at := func(hours float64) time.Time { return overviewDay.Add(time.Duration(hours * float64(time.Hour))) }
	running := f.assigned(t, "Look around", true, uuid.Nil)
	exec(ctx, t, f.pool, "UPDATE agent_tasks SET state = 'running', created_at = $2 WHERE id = $1", running.ID, at(10))
	queued := f.assigned(t, "Look elsewhere", false, uuid.Nil)
	exec(ctx, t, f.pool, "UPDATE agent_tasks SET created_at = $2 WHERE id = $1", queued.ID, at(-0.5))
	for _, s := range []struct {
		task uuid.UUID
		cost int64
		at   time.Time
	}{{running.ID, 1_500_000, at(11)}, {queued.ID, 250_000, at(-1.0 / 3)}} {
		exec(ctx, t, f.pool, `INSERT INTO task_spend (task_id, id, org_id, agent_id, provider, model, input_tokens, output_tokens, cost, created_at)
			VALUES ($1, $2, $3, $4, 'anthropic', 'test-model', 1, 1, $5, $6)`, s.task, uuid.New(), f.acme.ID, f.scout.ID, s.cost, s.at)
	}
	job := uuid.New()
	exec(ctx, t, f.pool, "INSERT INTO principals (id, kind, display_name, created_at, org_id) VALUES ($1, 'job', 'build', now(), $2)", job, f.acme.ID)
	exec(ctx, t, f.pool, "INSERT INTO jobs (principal_id, org_id, repo_id, name) VALUES ($1, $2, $3, 'build')", job, f.acme.ID, f.website.ID)
	exec(ctx, t, f.pool, `INSERT INTO job_runs (id, org_id, repo_id, number, job_id, sha, ref, trigger, definition, state, started_at, ended_at)
		VALUES ($1, $2, $3, 1, $4, $5, 'refs/heads/main', '{}', '{}', 'succeeded', $6, $7)`,
		uuid.New(), f.acme.ID, f.website.ID, job, shaA, at(12), at(12).Add(90*time.Second))
	exec(ctx, t, f.pool, "UPDATE pushes SET committed_at = $2, merged = '[]' WHERE repo_id = $1", f.website.ID, at(9))
	exec(ctx, t, f.pool, `UPDATE pushes SET merged = '[{"branch": "feature"}]' WHERE id = $1`, f.second.ID)
	for name, status := range map[string]string{"feature": "ready", "stray": "blocked"} {
		exec(ctx, t, f.pool, "UPDATE branches SET status = $1 WHERE repo_id = $2 AND name = $3 AND deleted_at IS NULL", status, f.website.ID, name)
	}
	exec(ctx, t, f.pool, `INSERT INTO issues (id, org_id, number, title, state, created_at, updated_at, closed_at)
		VALUES ($1, $2, 1, 'Opened', 'todo', $3, $3, NULL), ($4, $2, 2, 'Closed', 'done', $5, $5, $5)`,
		uuid.New(), f.acme.ID, at(8), uuid.New(), at(-48))
}

// dayLine says what d is as "date: tasks, spent, minutes, jobs".
func dayLine(d overview.Day) string {
	return fmt.Sprintf("%s: %d tasks, %d spent, %d min, %d jobs", d.Date.Format("2006-01-02 MST"), d.Tasks, d.Spent, d.JobMinutes, d.Jobs)
}

// repoLine says what r is as "repo: commits, merged, tasks, spent, minutes, jobs".
func repoLine(r overview.RepositoryFigures) string {
	return fmt.Sprintf("%s: %d commits, %d merged, %d tasks, %d spent, %d min, %d jobs", r.Repo, r.Commits, r.Merged, r.Tasks, r.Spent, r.JobMinutes, r.Jobs)
}

// TestOverviewStore checks that an organization's overview counts what stands and sums, by day in the period's zone
// and by each repository asked for, what happened in the period alone.
func TestOverviewStore(t *testing.T) {
	t.Parallel()
	f := newAgentFixture(t)
	seedOverview(t, f)
	s := NewOverviewStore(f.pool)
	ctx := context.Background()
	period := func(from, to, zone string) overview.Period {
		p, err := overview.PeriodIn(from, to, zone, overviewDay)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	tests := []struct {
		name               string
		repos              []uuid.UUID
		period             overview.Period
		wantDays, wantRepo []string
		wantReady          [2]int64
		wantOpened         int64
	}{
		{name: "two days in UTC", repos: []uuid.UUID{f.website.ID, f.secret.ID}, period: period("2026-06-09", "2026-06-10", "UTC"),
			wantDays: []string{"2026-06-09 UTC: 1 tasks, 250000 spent, 0 min, 0 jobs", "2026-06-10 UTC: 1 tasks, 1500000 spent, 2 min, 1 jobs"},
			wantRepo: []string{"website: 4 commits, 1 merged, 1 tasks, 1500000 spent, 2 min, 1 jobs"}, wantReady: [2]int64{1, 1}, wantOpened: 1},
		{name: "the evening before is the day itself in Jerusalem", repos: []uuid.UUID{f.website.ID}, period: period("2026-06-10", "2026-06-10", "Asia/Jerusalem"),
			wantDays: []string{"2026-06-10 IDT: 2 tasks, 1750000 spent, 2 min, 1 jobs"},
			wantRepo: []string{"website: 4 commits, 1 merged, 1 tasks, 1500000 spent, 2 min, 1 jobs"}, wantReady: [2]int64{1, 1}, wantOpened: 1},
		{name: "only the evening before", repos: []uuid.UUID{f.website.ID}, period: period("2026-06-09", "2026-06-09", "UTC"),
			wantDays: []string{"2026-06-09 UTC: 1 tasks, 250000 spent, 0 min, 0 jobs"}, wantReady: [2]int64{1, 1}},
		{name: "a repository nothing happened in", repos: []uuid.UUID{f.secret.ID}, period: period("2026-06-10", "2026-06-10", "UTC"),
			wantDays: []string{"2026-06-10 UTC: 1 tasks, 1500000 spent, 2 min, 1 jobs"}, wantOpened: 1},
		{name: "a month before", repos: []uuid.UUID{f.website.ID}, period: period("", "2026-05-31", "UTC"), wantReady: [2]int64{1, 1}},
	}
	// Acme's built-in agents count beside Scout.
	members := int64(count(t, f.pool, "SELECT count(*) FROM memberships WHERE org_id = $1", f.acme.ID))
	agentCount := int64(count(t, f.pool, "SELECT count(*) FROM agents WHERE org_id = $1", f.acme.ID))
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := s.Overview(ctx, f.acme.ID, tt.repos, tt.period)
			if err != nil {
				t.Fatalf("Overview() error = %v", err)
			}
			var days, repos []string
			for _, d := range got.Days {
				days = append(days, dayLine(d))
			}
			for _, r := range got.ByRepository {
				repos = append(repos, repoLine(r))
			}
			if !slices.Equal(days, tt.wantDays) || !slices.Equal(repos, tt.wantRepo) {
				t.Errorf("Overview() = days %q, repositories %q; want %q, %q", days, repos, tt.wantDays, tt.wantRepo)
			}
			if got.Members != members || got.Agents != agentCount || got.Working != 1 || got.Queued != 1 ||
				[2]int64{got.Ready, got.Blocked} != tt.wantReady || got.Issues == nil || *got.Issues != (overview.Issues{Open: 1, Opened: tt.wantOpened}) {
				t.Errorf("Overview() counts = %d members, %d agents, %d working, %d queued, %d ready, %d blocked, issues %+v; want %d, %d, 1, 1, %v, {1 %d}",
					got.Members, got.Agents, got.Working, got.Queued, got.Ready, got.Blocked, got.Issues, members, agentCount, tt.wantReady, tt.wantOpened)
			}
		})
	}
}
