package code

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/orgs"
)

// PermRunDelivery redelivers, pauses and resumes a repository's environments, and starts its waiting deliveries.
const PermRunDelivery = "delivery.run"

// MaxDeliveriesPage is the most deliveries a page lists.
const MaxDeliveriesPage = 100

// DeliveryAnnotation is the name of the annotation a target sets on each commit delivered to it: delivery/<target>,
// which no job may take.
func DeliveryAnnotation(target string) string {
	return "delivery/" + target
}

// Sync is whether an environment's cluster holds its branch's newest commit.
type Sync string

// The syncs: Synced once a delivery of the branch's head succeeded, Out of sync while a newer commit waits or a
// delivery failed.
const (
	Synced    Sync = "synced"
	OutOfSync Sync = "out_of_sync"
)

// Health is an environment's health: the worst of its objects', by the kstatus rules
// (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/delivery.md, "Health and revision").
// Unknown until a delivery reads it.
type Health string

// The healths, best first.
const (
	Healthy     Health = "healthy"
	Progressing Health = "progressing"
	Unknown     Health = "unknown"
	Missing     Health = "missing"
	Degraded    Health = "degraded"
)

// DeliveryKind is what a delivery does: deliver a commit, apply the last render again, or delete what the environment
// holds.
type DeliveryKind string

// The kinds of deliveries.
const (
	DeliverCommit DeliveryKind = "deliver"
	Resync        DeliveryKind = "resync"
	Teardown      DeliveryKind = "teardown"
)

// DeliveryTrigger is what queued a delivery: a push to its branch, a member's redelivery, or resuming its environment.
type DeliveryTrigger string

// The triggers of deliveries.
const (
	TriggeredByPush      DeliveryTrigger = "push"
	TriggeredByRedeliver DeliveryTrigger = "redeliver"
	TriggeredByResume    DeliveryTrigger = "resume"
)

// DeliveryState is where a delivery stands (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/
// delivery.md, "A delivery's life").
type DeliveryState string

// The states of a delivery. A superseded one is Cancelled, saying by which.
const (
	DeliveryWaiting   DeliveryState = "waiting"
	DeliveryQueued    DeliveryState = "queued"
	DeliveryRendering DeliveryState = "rendering"
	DeliveryApplying  DeliveryState = "applying"
	DeliverySucceeded DeliveryState = "succeeded"
	DeliveryFailed    DeliveryState = "failed"
	DeliveryCancelled DeliveryState = "cancelled"
)

// Active reports whether a delivery in state s hasn't ended.
func (s DeliveryState) Active() bool {
	return s == DeliveryWaiting || s == DeliveryQueued || s == DeliveryRendering || s == DeliveryApplying
}

// Environment is a target and a live branch: where the branch delivers, and what the target's cluster holds of it.
type Environment struct {
	ID uuid.UUID
	// Target is the target's principal, named as the target.
	Target Principal
	Branch string
	// Cluster is the ID of the secret that holds the cluster's kubeconfig; nil for the installation's own cluster.
	Cluster   *uuid.UUID
	Namespace string
	// Revision is the commit of its last successful delivery; empty before one.
	Revision string
	SyncedAt *time.Time
	Sync     Sync
	Health   Health
	// Paused environments take no delivery until resumed.
	Paused    bool
	CreatedAt time.Time
	// Version counts its changes from 1: with its newest delivery's number and version, its ETag.
	Version int64
	// Latest is its newest delivery, nil before one.
	Latest *Delivery
}

// Delivery is one delivery of an environment.
type Delivery struct {
	ID uuid.UUID
	// Number counts the repository's deliveries from 1.
	Number        int64
	EnvironmentID uuid.UUID
	// Target and Branch name its environment.
	Target string
	Branch string
	Kind   DeliveryKind
	// SHA is the commit it delivers; empty for a teardown.
	SHA     string
	Trigger DeliveryTrigger
	// PushID is the push that queued it, for one a push queued.
	PushID *uuid.UUID
	State  DeliveryState
	// Reason says why it waits, failed or was cancelled.
	Reason string
	// StartedBy is who redelivered it, resumed its environment or started it from Waiting; nil for one a push queued
	// that never waited.
	StartedBy *Principal
	QueuedAt  time.Time
	StartedAt *time.Time
	EndedAt   *time.Time
	// Version counts its changes from 1: its ETag.
	Version int64
}

// DeliveryPage is a page of an environment's deliveries, newest first, and the number the next page lists deliveries
// before; 0 after the last.
type DeliveryPage struct {
	Deliveries []Delivery
	Next       int64
}

// Errors of environments and deliveries.
var (
	// ErrEnvironmentPaused is an environment asked to take a delivery, or to pause, while it is paused.
	ErrEnvironmentPaused = errors.New("the environment is paused")
	// ErrEnvironmentNotPaused is an environment asked to resume that isn't paused.
	ErrEnvironmentNotPaused = errors.New("the environment isn't paused")
	// ErrDeliveryNotWaiting is a delivery asked to start that doesn't wait for a member.
	ErrDeliveryNotWaiting = errors.New("the delivery doesn't wait for a member to start it")
)

// EnvironmentStore keeps a repository's environments and their deliveries.
type EnvironmentStore interface {
	// Environments lists the repository's live environments, by target and then branch, of branch unless it is
	// empty, each with its newest delivery.
	Environments(ctx context.Context, repoID uuid.UUID, branch string) ([]Environment, error)
	// Environment finds one of the repository's live environments, with its newest delivery, or repos.ErrNotFound.
	Environment(ctx context.Context, repoID, id uuid.UUID) (Environment, error)
	// Deliveries lists at most limit of a live environment's deliveries, newest first, numbered before before (0 for
	// the newest): repos.ErrNotFound without the environment.
	Deliveries(ctx context.Context, repoID, environmentID uuid.UUID, before int64, limit int) ([]Delivery, error)
	// Delivery finds one of the repository's deliveries by number, or repos.ErrNotFound.
	Delivery(ctx context.Context, repoID uuid.UUID, number int64) (Delivery, error)
	// Redeliver queues, for actor, a delivery of the head of a live environment's branch, superseding its deliveries
	// that haven't started applying, in one transaction that appends event delivery.redelivered: repos.ErrNotFound
	// without it, ErrEnvironmentPaused while it is paused.
	Redeliver(ctx context.Context, actor uuid.UUID, repoID, environmentID uuid.UUID) (Delivery, error)
	// PauseEnvironment pauses a live environment for actor, cancelling its deliveries that haven't started applying,
	// in one transaction that appends event environment.paused: repos.ErrNotFound without it, ErrEnvironmentPaused
	// when it is paused already.
	PauseEnvironment(ctx context.Context, actor uuid.UUID, repoID, environmentID uuid.UUID) (Environment, error)
	// ResumeEnvironment resumes a paused environment for actor and queues a delivery of its branch's head, in one
	// transaction that appends event environment.resumed: repos.ErrNotFound without it, ErrEnvironmentNotPaused when
	// it isn't paused.
	ResumeEnvironment(ctx context.Context, actor uuid.UUID, repoID, environmentID uuid.UUID) (Environment, error)
	// StartDelivery queues a waiting delivery for actor, in one transaction that appends event delivery.started:
	// repos.ErrNotFound without it, ErrDeliveryNotWaiting when it doesn't wait.
	StartDelivery(ctx context.Context, actor uuid.UUID, repoID uuid.UUID, number int64) (Delivery, error)
}

// Environments lists the repository's live environments, by target and then branch, those of branch alone when it is
// given, each with its newest delivery; it takes repo.read.
func (s *Service) Environments(ctx context.Context, v Viewer, repoID uuid.UUID, branch string) ([]Environment, error) {
	ctx, span := tracer.Start(ctx, "code.Service.Environments")
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.repository.id", repoID.String()), attribute.String("cobranch.branch", branch))
	if err := s.read(ctx, v, repoID); err != nil {
		return nil, failed(span, "reading the environments", err)
	}
	list, err := s.store.Environments(ctx, repoID, branch)
	if err != nil {
		return nil, failed(span, "reading the environments", err)
	}
	return list, nil
}

// Environment finds one of the repository's live environments, with its newest delivery; it takes repo.read. One
// that isn't there is repos.ErrNotFound.
func (s *Service) Environment(ctx context.Context, v Viewer, repoID, id uuid.UUID) (Environment, error) {
	ctx, span := tracer.Start(ctx, "code.Service.Environment")
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.repository.id", repoID.String()), attribute.String("cobranch.environment.id", id.String()))
	if err := s.read(ctx, v, repoID); err != nil {
		return Environment{}, failed(span, "reading the environment", err)
	}
	e, err := s.store.Environment(ctx, repoID, id)
	if err != nil {
		return Environment{}, failed(span, "reading the environment", err)
	}
	return e, nil
}

// Deliveries lists a page of one of the repository's environments' deliveries, newest first, before the delivery
// numbered before (0 for the newest); it takes repo.read. limit 0 asks for DefaultPageSize.
func (s *Service) Deliveries(ctx context.Context, v Viewer, repoID, environmentID uuid.UUID, before int64, limit int) (DeliveryPage, error) {
	ctx, span := tracer.Start(ctx, "code.Service.Deliveries")
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.repository.id", repoID.String()), attribute.String("cobranch.environment.id", environmentID.String()))
	if err := s.read(ctx, v, repoID); err != nil {
		return DeliveryPage{}, failed(span, "reading the deliveries", err)
	}
	if limit < 0 || limit > MaxDeliveriesPage || before < 0 {
		return DeliveryPage{}, &orgs.ValidationError{Field: "limit", Code: "limit.invalid", Message: fmt.Sprintf("at most %d deliveries a page", MaxDeliveriesPage)}
	}
	if limit == 0 {
		limit = DefaultPageSize
	}
	list, err := s.store.Deliveries(ctx, repoID, environmentID, before, limit+1)
	if err != nil {
		return DeliveryPage{}, failed(span, "reading the deliveries", err)
	}
	page := DeliveryPage{Deliveries: list}
	if len(list) > limit {
		page.Deliveries = list[:limit]
		page.Next = list[limit-1].Number
	}
	return page, nil
}

// Delivery finds one of the repository's deliveries by its number; it takes repo.read. One that isn't there is
// repos.ErrNotFound.
func (s *Service) Delivery(ctx context.Context, v Viewer, repoID uuid.UUID, number int64) (Delivery, error) {
	ctx, span := tracer.Start(ctx, "code.Service.Delivery")
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.repository.id", repoID.String()), attribute.Int64("cobranch.delivery.number", number))
	if err := s.read(ctx, v, repoID); err != nil {
		return Delivery{}, failed(span, "reading the delivery", err)
	}
	d, err := s.store.Delivery(ctx, repoID, number)
	if err != nil {
		return Delivery{}, failed(span, "reading the delivery", err)
	}
	return d, nil
}

// Redeliver queues a delivery of the head of one of the repository's environments' branch for v, superseding its
// deliveries that haven't started applying; it takes delivery.run. A paused environment is ErrEnvironmentPaused.
func (s *Service) Redeliver(ctx context.Context, v Viewer, repoID, environmentID uuid.UUID) (Delivery, error) {
	ctx, span := tracer.Start(ctx, "code.Service.Redeliver")
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.repository.id", repoID.String()), attribute.String("cobranch.environment.id", environmentID.String()))
	if err := s.runDelivery(ctx, v, repoID); err != nil {
		return Delivery{}, failed(span, "redelivering", err)
	}
	d, err := s.store.Redeliver(ctx, v.ID, repoID, environmentID)
	if err != nil {
		return Delivery{}, failed(span, "redelivering", err)
	}
	return d, nil
}

// PauseEnvironment pauses one of the repository's environments for v, which then takes no delivery until resumed,
// cancelling those that haven't started applying; it takes delivery.run. A paused one is ErrEnvironmentPaused.
func (s *Service) PauseEnvironment(ctx context.Context, v Viewer, repoID, environmentID uuid.UUID) (Environment, error) {
	ctx, span := tracer.Start(ctx, "code.Service.PauseEnvironment")
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.repository.id", repoID.String()), attribute.String("cobranch.environment.id", environmentID.String()))
	if err := s.runDelivery(ctx, v, repoID); err != nil {
		return Environment{}, failed(span, "pausing the environment", err)
	}
	e, err := s.store.PauseEnvironment(ctx, v.ID, repoID, environmentID)
	if err != nil {
		return Environment{}, failed(span, "pausing the environment", err)
	}
	return e, nil
}

// ResumeEnvironment resumes one of the repository's paused environments for v and queues a delivery of its branch's
// head; it takes delivery.run. One that isn't paused is ErrEnvironmentNotPaused.
func (s *Service) ResumeEnvironment(ctx context.Context, v Viewer, repoID, environmentID uuid.UUID) (Environment, error) {
	ctx, span := tracer.Start(ctx, "code.Service.ResumeEnvironment")
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.repository.id", repoID.String()), attribute.String("cobranch.environment.id", environmentID.String()))
	if err := s.runDelivery(ctx, v, repoID); err != nil {
		return Environment{}, failed(span, "resuming the environment", err)
	}
	e, err := s.store.ResumeEnvironment(ctx, v.ID, repoID, environmentID)
	if err != nil {
		return Environment{}, failed(span, "resuming the environment", err)
	}
	return e, nil
}

// StartDelivery queues one of the repository's deliveries that waits for a member, its tip pushed from outside the
// organization, for v; it takes delivery.run. One that doesn't wait is ErrDeliveryNotWaiting.
func (s *Service) StartDelivery(ctx context.Context, v Viewer, repoID uuid.UUID, number int64) (Delivery, error) {
	ctx, span := tracer.Start(ctx, "code.Service.StartDelivery")
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.repository.id", repoID.String()), attribute.Int64("cobranch.delivery.number", number))
	if err := s.runDelivery(ctx, v, repoID); err != nil {
		return Delivery{}, failed(span, "starting the delivery", err)
	}
	d, err := s.store.StartDelivery(ctx, v.ID, repoID, number)
	if err != nil {
		return Delivery{}, failed(span, "starting the delivery", err)
	}
	return d, nil
}

// runDelivery checks that v holds delivery.run on the repository: repos.ErrNotFound without repo.read.
func (s *Service) runDelivery(ctx context.Context, v Viewer, repoID uuid.UUID) error {
	held, err := s.holds(ctx, v, repoID)
	if err != nil {
		return err
	}
	if !slices.Contains(held, PermRunDelivery) {
		return fmt.Errorf("%w: %s on the repository", auth.ErrForbidden, PermRunDelivery)
	}
	return nil
}
