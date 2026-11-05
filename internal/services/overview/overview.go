// Package overview is the domain of an organization's figures: how its work stands and what its people, agents and
// jobs did over a span of days, as its Overview shows them. It knows nothing of HTTP or PostgreSQL: adapters bring
// those through its port, Store, and every permission check goes through sign-in's authorizer
// (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/quotas.md, "An organization's figures").
package overview

import (
	"time"
	// The zones a period is read in come with the binary, since the image it runs in may have none.
	_ "time/tzdata"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/agents"
	"cobranch.dev/internal/services/orgs"
)

// MaxDays is the most days an overview covers: a quarter, since it charts each of them.
const MaxDays = 92

// dateLayout is how a period's days are written: 2026-11-05.
const dateLayout = "2006-01-02"

// Period is the days an overview covers in a time zone, From to To, both included, each as its first moment there.
type Period struct {
	From time.Time
	To   time.Time
	// Zone is the IANA name of the time zone.
	Zone string
}

// Since is the period's first moment.
func (p Period) Since() time.Time { return p.From }

// Until is the moment after the period's last: the first moment of the day after To, however long its days are.
func (p Period) Until() time.Time { return p.To.AddDate(0, 0, 1) }

// Days lists the period's days, first to last, each as its first moment.
func (p Period) Days() []time.Time {
	var out []time.Time
	for d := p.From; !d.After(p.To); d = d.AddDate(0, 0, 1) {
		out = append(out, d)
	}
	return out
}

// PeriodIn reads the days from and to, written as 2026-11-01, in the IANA time zone zone, UTC when it is empty: to is
// today there when empty, and from the first day of to's month, so that neither asks for this month so far. A day or
// zone that isn't one, a period that ends before it starts, or one longer than MaxDays, is refused.
func PeriodIn(from, to, zone string, now time.Time) (Period, error) {
	if zone == "" {
		zone = "UTC"
	}
	loc, err := time.LoadLocation(zone)
	if err != nil || zone == "Local" {
		return Period{}, invalid("zone", "overview.zone_invalid", "a zone is an IANA time zone, such as Asia/Jerusalem or UTC")
	}
	if to == "" {
		to = now.In(loc).Format(dateLayout)
	}
	last, err := time.ParseInLocation(dateLayout, to, loc)
	if err != nil {
		return Period{}, invalid("to", "overview.day_invalid", "a day is written as its year, month and day, such as 2026-11-05")
	}
	first := time.Date(last.Year(), last.Month(), 1, 0, 0, 0, 0, loc)
	if from != "" {
		if first, err = time.ParseInLocation(dateLayout, from, loc); err != nil {
			return Period{}, invalid("from", "overview.day_invalid", "a day is written as its year, month and day, such as 2026-11-01")
		}
	}
	if last.Before(first) || first.AddDate(0, 0, MaxDays-1).Before(last) {
		return Period{}, invalid("from", "overview.period_invalid", "a period ends on or after its first day and covers at most 92 days")
	}
	return Period{From: first, To: last, Zone: zone}, nil
}

// invalid is the refusal of a field's value.
func invalid(field, code, message string) error {
	return &orgs.ValidationError{Field: field, Code: code, Message: message}
}

// Overview is how an organization's work stands and what it did over a period, as a viewer sees it: counts of what
// stands now, the period's totals and days, and its repositories' share of them.
type Overview struct {
	Period Period
	// Members counts the organization's members, Repositories the repositories the viewer reads, and Agents its
	// agents.
	Members      int64
	Repositories int64
	Agents       int64
	// Issues counts its open issues and those opened in the period; nil for a viewer who may not read its issues.
	Issues *Issues
	// Ready and Blocked count the live branches, but main, of the repositories the viewer reads that are Ready and
	// Blocked.
	Ready   int64
	Blocked int64
	// Working counts the agents with a task running, and Queued the tasks waiting for one.
	Working int64
	Queued  int64
	// Totals sum up the period's days.
	Totals Figures
	// Days are the period's days, first to last, each with what the organization did then. They count every task and
	// job run, those the viewer doesn't see too, as an organization's usage counts every repository: they are the
	// organization's money.
	Days []Day
	// ByRepository lists the repositories the viewer reads that saw anything in the period, costliest first, each
	// with its share.
	ByRepository []RepositoryFigures
}

// Issues counts an organization's open issues, and the issues opened in a period, whether open now or not.
type Issues struct {
	Open   int64
	Opened int64
}

// Figures is what an organization, or one of its repositories, did over a span: the tasks queued, what its tasks'
// calls of their models cost, and its job runs that ended, with the minutes they ran, each rounded up to a minute.
type Figures struct {
	Tasks      int64
	Spent      agents.USD
	JobMinutes int64
	Jobs       int64
}

// Day is what an organization did on one day of a period, as its first moment in the period's zone.
type Day struct {
	Date time.Time
	Figures
}

// RepositoryFigures is what happened in one of an organization's repositories over a period: the commits first pushed
// to it, the branches merged into its main, and its tasks' and job runs' figures.
type RepositoryFigures struct {
	RepoID  uuid.UUID
	Repo    string
	Commits int64
	Merged  int64
	Figures
}

// add sums f and g.
func (f Figures) add(g Figures) Figures {
	return Figures{Tasks: f.Tasks + g.Tasks, Spent: f.Spent + g.Spent, JobMinutes: f.JobMinutes + g.JobMinutes, Jobs: f.Jobs + g.Jobs}
}
