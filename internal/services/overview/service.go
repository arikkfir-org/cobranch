package overview

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/issues"
	"cobranch.dev/internal/services/orgs"
	"cobranch.dev/internal/services/repos"
)

var tracer = otel.Tracer("cobranch.dev/internal/services/overview")

// Store reads an organization's figures.
type Store interface {
	// Overview reads how the organization's work stands and what it did from p's first moment until the moment after
	// its last: its counts, beside Ready and Blocked over the repositories of repoIDs; the days of p that saw anything,
	// in p's zone, first to last; and those of the repositories of repoIDs that saw anything, each with its share. It
	// leaves Period, Repositories and Totals unset, and every day of p's without a figure out.
	Overview(ctx context.Context, orgID uuid.UUID, repoIDs []uuid.UUID, p Period) (Overview, error)
}

// Service answers an organization's figures.
type Service struct {
	store Store
	authz *auth.Authorizer
}

// NewService returns a Service over store, checking permissions with authz.
func NewService(store Store, authz *auth.Authorizer) *Service {
	return &Service{store: store, authz: authz}
}

// Viewer is who an operation runs for: a signed-in user, or the user or API key a credential acts as.
type Viewer = auth.User

// Overview answers how the organization's work stands, and what it did over p, as v sees it. It takes org.read, and
// answers orgs.ErrNotFound without it. Its branches and repositories are those v reads, and its issues are counted
// only for a viewer holding issues.read; its days count every task and job run, as Overview says. Every day of p
// has its entry, and ByRepository is costliest first.
func (s *Service) Overview(ctx context.Context, v Viewer, orgID uuid.UUID, p Period) (Overview, error) {
	ctx, span := tracer.Start(ctx, "overview.Service.Overview", trace.WithAttributes(
		attribute.String("cobranch.organization.id", orgID.String()),
		attribute.String("cobranch.overview.from", p.From.Format(dateLayout)),
		attribute.String("cobranch.overview.to", p.To.Format(dateLayout)),
		attribute.String("cobranch.overview.zone", p.Zone)))
	defer span.End()
	held, err := s.authz.Holds(ctx, v.ID, auth.ScopeOrganization, orgID)
	if err != nil {
		return Overview{}, failed(span, "reading the overview", fmt.Errorf("reading the viewer's permissions: %w", err))
	}
	if !slices.Contains(held, orgs.PermRead) {
		return Overview{}, failed(span, "reading the overview", orgs.ErrNotFound)
	}
	readable, err := s.authz.Repositories(ctx, v.ID, orgID, repos.PermRead)
	if err != nil {
		return Overview{}, failed(span, "reading the overview", fmt.Errorf("listing the repositories the viewer reads: %w", err))
	}
	o, err := s.store.Overview(ctx, orgID, readable, p)
	if err != nil {
		return Overview{}, failed(span, "reading the overview", err)
	}
	o.Period, o.Repositories = p, int64(len(readable))
	if !slices.Contains(held, issues.PermRead) {
		o.Issues = nil
	}
	o.Days = everyDay(p, o.Days)
	o.Totals = Figures{}
	for _, d := range o.Days {
		o.Totals = o.Totals.add(d.Figures)
	}
	return o, nil
}

// everyDay is days with an entry for each of p's days, first to last: those days names, and none for the rest.
func everyDay(p Period, days []Day) []Day {
	byDate := make(map[string]Figures, len(days))
	for _, d := range days {
		byDate[d.Date.Format(dateLayout)] = d.Figures
	}
	all := p.Days()
	out := make([]Day, len(all))
	for i, d := range all {
		out[i] = Day{Date: d, Figures: byDate[d.Format(dateLayout)]}
	}
	return out
}

// failed records err on span as the failure of what, and wraps it, unless it is an answer the viewer's request earns.
func failed(span trace.Span, what string, err error) error {
	var verr *orgs.ValidationError
	if errors.Is(err, orgs.ErrNotFound) || errors.As(err, &verr) {
		return err
	}
	span.RecordError(err)
	span.SetStatus(codes.Error, what)
	return fmt.Errorf("%s: %w", what, err)
}
