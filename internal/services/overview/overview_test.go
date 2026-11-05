package overview_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/orgs"
	"cobranch.dev/internal/services/orgs/orgstest"
	"cobranch.dev/internal/services/overview"
	"cobranch.dev/internal/services/overview/overviewtest"
)

// TestPeriodIn checks which days a period covers, in which zone, and which periods are refused.
func TestPeriodIn(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 30, 23, 30, 0, 0, time.UTC)
	tests := []struct {
		name, from, to, zone string
		want                 [2]string
		wantDays             int
		wantErr              string
	}{
		{name: "this month so far, in UTC", want: [2]string{"2026-11-01", "2026-11-30"}, wantDays: 30},
		{name: "this month so far where it is already December", zone: "Asia/Jerusalem", want: [2]string{"2026-12-01", "2026-12-01"}, wantDays: 1},
		{name: "last month", from: "2026-10-01", to: "2026-10-31", zone: "Europe/Berlin", want: [2]string{"2026-10-01", "2026-10-31"}, wantDays: 31},
		{name: "a month by its last day", to: "2026-02-28", want: [2]string{"2026-02-01", "2026-02-28"}, wantDays: 28},
		{name: "the last 30 days", from: "2026-11-01", to: "2026-11-30", want: [2]string{"2026-11-01", "2026-11-30"}, wantDays: 30},
		{name: "one day", from: "2026-11-05", to: "2026-11-05", want: [2]string{"2026-11-05", "2026-11-05"}, wantDays: 1},
		{name: "a quarter", from: "2026-09-01", to: "2026-12-01", want: [2]string{"2026-09-01", "2026-12-01"}, wantDays: 92},
		{name: "longer than a quarter", from: "2026-08-31", to: "2026-12-01", wantErr: "overview.period_invalid"},
		{name: "ending before it starts", from: "2026-11-05", to: "2026-11-04", wantErr: "overview.period_invalid"},
		{name: "no such zone", zone: "Mars/Olympus", wantErr: "overview.zone_invalid"},
		{name: "the server's own zone", zone: "Local", wantErr: "overview.zone_invalid"},
		{name: "no such first day", from: "2026-11-31", wantErr: "overview.day_invalid"},
		{name: "no such last day", to: "05/11/2026", wantErr: "overview.day_invalid"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p, err := overview.PeriodIn(tt.from, tt.to, tt.zone, now)
			if tt.wantErr != "" {
				var verr *orgs.ValidationError
				if !errors.As(err, &verr) || verr.Code != tt.wantErr {
					t.Fatalf("PeriodIn() error = %v, want %s", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("PeriodIn() error = %v", err)
			}
			got := [2]string{p.From.Format("2006-01-02"), p.To.Format("2006-01-02")}
			if got != tt.want || len(p.Days()) != tt.wantDays {
				t.Errorf("PeriodIn() = %v over %d days, want %v over %d", got, len(p.Days()), tt.want, tt.wantDays)
			}
		})
	}
}

// TestPeriodUntil checks that a period ends at the first moment of the day after its last, however long its days are.
func TestPeriodUntil(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, from, to, zone string
		want                 time.Duration
	}{
		{name: "a day in UTC", from: "2026-10-25", to: "2026-10-25", zone: "UTC", want: 24 * time.Hour},
		{name: "the day Berlin's clocks go back", from: "2026-10-25", to: "2026-10-25", zone: "Europe/Berlin", want: 25 * time.Hour},
		{name: "the day Berlin's clocks go forward", from: "2026-03-29", to: "2026-03-29", zone: "Europe/Berlin", want: 23 * time.Hour},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p, err := overview.PeriodIn(tt.from, tt.to, tt.zone, time.Now())
			if err != nil {
				t.Fatalf("PeriodIn() error = %v", err)
			}
			if got := p.Until().Sub(p.Since()); got != tt.want {
				t.Errorf("Until() - Since() = %v, want %v", got, tt.want)
			}
		})
	}
}

// fixture is an organization with repositories website, which Max and Gus read, and secret, which neither does; Max
// is a Member, Gus a Guest, and Nia no member.
type fixture struct {
	store                *overviewtest.Store
	service              *overview.Service
	org, website, secret uuid.UUID
	max, gus, nia        overview.Viewer
	period               overview.Period
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	grants := orgstest.NewStore()
	viewer := func(name string) overview.Viewer {
		return overview.Viewer{Principal: auth.Principal{ID: uuid.New(), Kind: auth.KindUser, DisplayName: name}}
	}
	f := &fixture{store: overviewtest.NewStore(), org: uuid.New(), website: uuid.New(), secret: uuid.New(), max: viewer("Max"), gus: viewer("Gus"), nia: viewer("Nia")}
	grants.Repos[f.website], grants.Repos[f.secret] = f.org, f.org
	grants.Grant(f.max.ID, auth.RoleMember, auth.ScopeOrganization, f.org)
	grants.Grant(f.max.ID, auth.RoleReader, auth.ScopeRepository, f.website)
	grants.Grant(f.gus.ID, auth.RoleGuest, auth.ScopeOrganization, f.org)
	grants.Grant(f.gus.ID, auth.RoleReader, auth.ScopeRepository, f.website)
	f.service = overview.NewService(f.store, auth.NewAuthorizer(grants))
	p, err := overview.PeriodIn("2026-11-01", "2026-11-05", "Asia/Jerusalem", time.Now())
	if err != nil {
		t.Fatalf("PeriodIn() error = %v", err)
	}
	f.period = p
	day := func(d int) time.Time { return time.Date(2026, 11, d, 0, 0, 0, 0, time.UTC) }
	f.store.Overviews[f.org] = overview.Overview{Members: 4, Agents: 3, Issues: &overview.Issues{Open: 24, Opened: 3}, Ready: 3, Blocked: 2, Working: 2, Queued: 5,
		Days: []overview.Day{
			{Date: day(2), Figures: overview.Figures{Tasks: 2, Spent: 1_500_000, JobMinutes: 40, Jobs: 3}},
			{Date: day(4), Figures: overview.Figures{Tasks: 1, Spent: 250_000, JobMinutes: 7, Jobs: 1}},
		},
		ByRepository: []overview.RepositoryFigures{
			{RepoID: f.secret, Repo: "secret", Commits: 9, Figures: overview.Figures{Tasks: 2, Spent: 1_500_000}},
			{RepoID: f.website, Repo: "website", Commits: 3, Merged: 1, Figures: overview.Figures{Tasks: 1, Spent: 250_000, JobMinutes: 47, Jobs: 4}},
		}}
	return f
}

// TestOverview checks what a viewer sees of an organization's figures: its counts, every day of the period with the
// totals, and the repositories they read.
func TestOverview(t *testing.T) {
	t.Parallel()
	boom := errors.New("boom")
	tests := []overviewCase{
		{name: "a member sees the issues and the repositories they read", viewer: func(f *fixture) overview.Viewer { return f.max },
			wantIssues: &overview.Issues{Open: 24, Opened: 3}, wantRepos: []string{"website"}},
		{name: "a guest sees no issues", viewer: func(f *fixture) overview.Viewer { return f.gus }, wantRepos: []string{"website"}},
		{name: "no member", viewer: func(f *fixture) overview.Viewer { return f.nia }, wantErr: orgs.ErrNotFound},
		{name: "the store failing", viewer: func(f *fixture) overview.Viewer { return f.max }, fail: boom, wantErr: boom},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			f.store.Fail = tt.fail
			got, err := f.service.Overview(context.Background(), tt.viewer(f), f.org, f.period)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Overview() error = %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr == nil {
				tt.check(t, got)
			}
		})
	}
}

// overviewCase is a case of TestOverview: the viewer, how the store fails, and what Overview answers: the issues and
// the repositories, or the error.
type overviewCase struct {
	name       string
	viewer     func(f *fixture) overview.Viewer
	fail       error
	wantIssues *overview.Issues
	wantRepos  []string
	wantErr    error
}

// check checks got against the case and the fixture's counts.
func (tt overviewCase) check(t *testing.T, got overview.Overview) {
	t.Helper()
	if (got.Issues == nil) != (tt.wantIssues == nil) || got.Issues != nil && *got.Issues != *tt.wantIssues {
		t.Errorf("Overview().Issues = %+v, want %+v", got.Issues, tt.wantIssues)
	}
	var repos []string
	for _, r := range got.ByRepository {
		repos = append(repos, r.Repo)
	}
	if !slices.Equal(repos, tt.wantRepos) {
		t.Errorf("Overview().ByRepository = %v, want %v", repos, tt.wantRepos)
	}
	if got.Repositories != 1 || got.Members != 4 || got.Ready != 3 || got.Queued != 5 {
		t.Errorf("Overview() counts = %d repositories, %d members, %d ready, %d queued; want 1, 4, 3, 5", got.Repositories, got.Members, got.Ready, got.Queued)
	}
}

// TestOverviewDays checks that an overview has every day of its period, in its zone, and their totals.
func TestOverviewDays(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	got, err := f.service.Overview(context.Background(), f.max, f.org, f.period)
	if err != nil {
		t.Fatalf("Overview() error = %v", err)
	}
	wantTasks := []int64{0, 2, 0, 1, 0}
	if len(got.Days) != len(wantTasks) {
		t.Fatalf("Overview() has %d days, want %d", len(got.Days), len(wantTasks))
	}
	for i, d := range got.Days {
		if d.Tasks != wantTasks[i] || d.Date.Day() != i+1 || d.Date.Location().String() != "Asia/Jerusalem" {
			t.Errorf("day %d = %s with %d tasks, want 2026-11-%02d in Asia/Jerusalem with %d", i, d.Date, d.Tasks, i+1, wantTasks[i])
		}
	}
	want := overview.Figures{Tasks: 3, Spent: 1_750_000, JobMinutes: 47, Jobs: 4}
	if got.Totals != want || got.Period != f.period || f.store.Asked != f.period {
		t.Errorf("Overview() totals %+v over %v, asked %v; want %+v over %v", got.Totals, got.Period, f.store.Asked, want, f.period)
	}
}
