package agents

import (
	"context"
	"time"
	// The zones a month is reported in come with the binary, since the image it runs in may have none.
	_ "time/tzdata"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"

	"cobranch.dev/internal/services/repos"
)

// MaxSpan is the longest span a spend report covers: a year and a day.
const MaxSpan = 366 * 24 * time.Hour

// Month is a calendar month in a time zone, as a spend report covers one: its name, such as 2026-11, the zone's IANA
// name, and the span from its first moment until the next month's.
type Month struct {
	Name  string
	Zone  string
	Since time.Time
	Until time.Time
}

// MonthIn reads the month named month, such as 2026-11, in the IANA time zone zone: the month now holds in that zone
// when month is empty, and UTC when zone is. A month or zone that isn't one is refused.
func MonthIn(month, zone string, now time.Time) (Month, error) {
	if zone == "" {
		zone = "UTC"
	}
	loc, err := time.LoadLocation(zone)
	if err != nil || zone == "Local" {
		return Month{}, invalid("zone", "spend.zone_invalid", "a zone is an IANA time zone, such as Asia/Jerusalem or UTC")
	}
	if month == "" {
		month = now.In(loc).Format("2006-01")
	}
	start, err := time.ParseInLocation("2006-01", month, loc)
	if err != nil || start.Format("2006-01") != month {
		return Month{}, invalid("month", "spend.month_invalid", "a month is written as its year and number, such as 2026-11")
	}
	return Month{Name: month, Zone: zone, Since: start, Until: start.AddDate(0, 1, 0)}, nil
}

// Report is what an organization's agents spent from Since until Until, such as a month: each agent's tasks queued then
// and what its calls then cost, with the costliest of those tasks that the viewer sees, and what the calls of each model
// then read, wrote and cost. Its counts and amounts count every task of the organization, those the viewer doesn't see
// too, as an organization's usage counts every repository: they are the organization's money.
type Report struct {
	Since  time.Time
	Until  time.Time
	Agents []AgentSpend
	Models []ModelSpend
}

// AgentSpend is what one of the organization's agents spent in a report's span, by name, with the model it runs on and
// the cap it puts on each task now.
type AgentSpend struct {
	Agent    Principal
	Provider string
	Model    string
	Cap      USD
	// Tasks counts its tasks queued in the span, and FailedOnCap and Active those of them that failed on reaching its
	// cap then, whatever it is now, and that are queued, running or waiting.
	Tasks       int64
	FailedOnCap int64
	Active      int64
	// Spent is what its calls in the span cost.
	Spent USD
	// Costliest is the task of those queued in the span that spent most in all, of those the viewer sees; nil for none.
	Costliest *TaskCost
}

// TaskCost names one of the organization's tasks and what it spent in all.
type TaskCost struct {
	Number int64
	Title  string
	Spent  USD
}

// ModelSpend is what the calls of one model cost in a report's span: one of the organization's models, which shows
// though none called it, or another its tasks asked; how many tasks called it, and the tokens it read and wrote.
type ModelSpend struct {
	Provider string
	Model    string
	Tasks    int64
	Input    int64
	Output   int64
	Spent    USD
}

// Spend reports what the organization's agents spent from since until until, as Report says; it takes org.read. An
// agent's costliest task is one the viewer sees: it names no repository or one they read, and its kind's guard passes.
// A span that doesn't end after it starts, or that runs past MaxSpan, is refused.
func (s *Service) Spend(ctx context.Context, v Viewer, orgID uuid.UUID, since, until time.Time) (Report, error) {
	ctx, span := tracer.Start(ctx, "agents.Service.Spend", onOrg(orgID,
		attribute.String("cobranch.spend.since", since.UTC().Format(time.RFC3339)),
		attribute.String("cobranch.spend.until", until.UTC().Format(time.RFC3339))))
	defer span.End()
	if !until.After(since) || until.Sub(since) > MaxSpan {
		return Report{}, failed(span, "reporting the spend", invalid("since", "spend.span_invalid", "a report covers at most a year, and ends after it starts"))
	}
	held, err := s.held(ctx, v, orgID)
	if err != nil {
		return Report{}, failed(span, "reporting the spend", err)
	}
	readable, err := s.authz.Repositories(ctx, v.ID, orgID, repos.PermRead)
	if err != nil {
		return Report{}, failed(span, "listing the repositories the viewer reads", err)
	}
	r, err := s.store.Spend(ctx, orgID, since, until, readable, s.hidden(held))
	return r, failed(span, "reporting the spend", err)
}
