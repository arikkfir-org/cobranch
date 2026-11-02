package limits

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

var tracer = otel.Tracer("cobranch.dev/internal/services/limits")

// Store is what the database keeps of the limits, and the usage it counts.
type Store interface {
	// InstallationLimits reads the limits the installation's settings set.
	InstallationLimits(ctx context.Context) (Settings, error)
	// OrganizationLimits reads the organization's own limits; none for an organization without them.
	OrganizationLimits(ctx context.Context, orgID uuid.UUID) (Overrides, error)
	// GitData sums the bytes of the organization's repositories' active packs.
	GitData(ctx context.Context, orgID uuid.UUID) (int64, error)
}

// Service works out which limits apply, for the services that check them in their own transactions. It checks no
// permission: it answers only those services, never a viewer.
type Service struct {
	store  Store
	values Settings
}

// NewService returns a Service over store, under the limits the chart's values set, which win over the database's.
func NewService(store Store, values Settings) *Service {
	return &Service{store: store, values: values}
}

// Installation returns the installation's limits: each the values', else the database's, else the default.
func (s *Service) Installation(ctx context.Context) (Limits, error) {
	ctx, span := tracer.Start(ctx, "limits.Service.Installation")
	defer span.End()
	db, err := s.store.InstallationLimits(ctx)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "reading the installation's limits")
		return Limits{}, fmt.Errorf("reading the installation's limits: %w", err)
	}
	return Resolve(s.values, db), nil
}

// Organization returns the limits that apply to the organization: its own where an administrator set them, else the
// installation's.
func (s *Service) Organization(ctx context.Context, orgID uuid.UUID) (Limits, error) {
	ctx, span := tracer.Start(ctx, "limits.Service.Organization")
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.organization.id", orgID.String()))
	installation, err := s.Installation(ctx)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "reading the installation's limits")
		return Limits{}, err
	}
	own, err := s.store.OrganizationLimits(ctx, orgID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "reading the organization's limits")
		return Limits{}, fmt.Errorf("reading the limits of organization %s: %w", orgID, err)
	}
	return installation.For(own), nil
}

// WeighPush refuses, with an *ExceededError, a push that would bring size bytes of Git data into the organization
// over its limit, and returns the limit otherwise. The store weighs it again in the push's transaction, where two
// pushes at once can't both pass on the same reading.
func (s *Service) WeighPush(ctx context.Context, orgID uuid.UUID, size int64) (int64, error) {
	ctx, span := tracer.Start(ctx, "limits.Service.WeighPush")
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.organization.id", orgID.String()), attribute.Int64("cobranch.push.size", size))
	limits, err := s.Organization(ctx, orgID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "reading the limits")
		return 0, err
	}
	limit := limits.GitDataPerOrganization
	if limit == 0 {
		return 0, nil
	}
	used, err := s.store.GitData(ctx, orgID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "summing the Git data")
		return 0, fmt.Errorf("summing the Git data of organization %s: %w", orgID, err)
	}
	if Exceeds(limit, used, size) {
		return limit, &ExceededError{Kind: KindGitData, Limit: limit, Used: used, Adding: size}
	}
	return limit, nil
}
