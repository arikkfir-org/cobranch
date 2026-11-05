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
	"cobranch.dev/internal/services/secrets"
)

// PermRunDelivery redelivers, pauses and resumes a repository's environments, and starts its waiting deliveries.
const PermRunDelivery = "delivery.run"

// MaxDeliveriesPage is the most deliveries a page lists.
const MaxDeliveriesPage = 100

// MaxRender is the largest render a render pod may upload, in bytes: 16 MiB.
const MaxRender = 16 << 20

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

// DeliveryTrigger is what queued a delivery: a push to its branch or into the default branch, a member's redelivery,
// resuming its environment, or the controller's schedule, which resyncs environments and tries failed teardowns again.
type DeliveryTrigger string

// The triggers of deliveries.
const (
	TriggeredByPush      DeliveryTrigger = "push"
	TriggeredByRedeliver DeliveryTrigger = "redeliver"
	TriggeredByResume    DeliveryTrigger = "resume"
	TriggeredBySchedule  DeliveryTrigger = "schedule"
)

// DeliveryState is where a delivery stands (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/
// delivery.md, "A delivery's life").
type DeliveryState string

// The states of a delivery. A superseded one is Cancelled, saying by which; a teardown is Deleting while it waits for
// what it deleted to go.
const (
	DeliveryWaiting   DeliveryState = "waiting"
	DeliveryQueued    DeliveryState = "queued"
	DeliveryRendering DeliveryState = "rendering"
	DeliveryApplying  DeliveryState = "applying"
	DeliveryDeleting  DeliveryState = "deleting"
	DeliverySucceeded DeliveryState = "succeeded"
	DeliveryFailed    DeliveryState = "failed"
	DeliveryCancelled DeliveryState = "cancelled"
)

// Active reports whether a delivery in state s hasn't ended.
func (s DeliveryState) Active() bool {
	return s == DeliveryWaiting || s == DeliveryQueued || s == DeliveryRendering || s == DeliveryApplying || s == DeliveryDeleting
}

// Environment is a target and a live branch: where the branch delivers, and what the target's cluster holds of it.
type Environment struct {
	ID uuid.UUID
	// Target is the target's principal, named as the target.
	Target Principal
	Branch string
	// Cluster is the ID of the secret that holds the cluster's kubeconfig; nil for the installation's own cluster.
	Cluster *uuid.UUID
	// ClusterName is that secret's name, for a viewer who may manage it; empty otherwise.
	ClusterName string
	Namespace   string
	// URL is where it answers, its target's URL in the default branch's /cobranch.yaml expanded; empty without one.
	URL string
	// Renders are the roots its branch's head renders for it, in order; none while its target or its file is gone, or
	// its roots can't be planned.
	Renders []Render
	// Revision is the commit of its last successful delivery; empty before one.
	Revision string
	SyncedAt *time.Time
	Sync     Sync
	Health   Health
	// Paused environments take no delivery until resumed.
	Paused    bool
	CreatedAt time.Time
	// Version counts its changes from 1: with a digest of how it reads, its ETag.
	Version int64
	// Latest is its newest delivery, nil before one.
	Latest *Delivery
}

// Render is a root a delivery renders: how, helm, kustomize or manifests, and the directory, its variables expanded.
type Render struct {
	Kind string
	Path string
}

// EnvironmentObject is an object an environment holds, in the order its last apply applied it, with its health as
// last read.
type EnvironmentObject struct {
	Group, Version, Kind, Namespace, Name string
	Health                                Health
	// Message says why it has its health, as kstatus words it.
	Message string
	// Ready is how many of its replicas, pods or completions are ready, as 2/2; empty for a kind that counts none.
	Ready string
	// CreatedAt is when the cluster created it; nil before a health read found it.
	CreatedAt *time.Time
	// Delivery is the number of the delivery whose apply recorded it; 0 once that delivery is gone.
	Delivery int64
}

// DeliveryTarget is a target the default branch's /cobranch.yaml declares, with the name of its cluster's secret for a
// viewer who may manage it.
type DeliveryTarget struct {
	Target
	ClusterName string
}

// ClusterSecret is a secret a target names as its cluster: its organization's, or one of its repositories' when RepoID
// is set.
type ClusterSecret struct {
	ID     uuid.UUID
	OrgID  uuid.UUID
	RepoID *uuid.UUID
	Name   string
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
	SHA string
	// Subject is the first line of that commit's message; empty for a teardown.
	Subject string
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
	// Version counts its changes from 1: with a digest of how it reads, its ETag.
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
	// ErrDeliveryNotRendering is a render uploaded for a delivery that doesn't render, or has its render already.
	ErrDeliveryNotRendering = errors.New("the delivery doesn't render, or has its render already")
	// ErrRenderTooLarge is a render larger than MaxRender.
	ErrRenderTooLarge = errors.New("the render is larger than 16 MiB")
)

// EnvironmentStore keeps a repository's environments and their deliveries.
type EnvironmentStore interface {
	// Environments lists the repository's live environments, by target and then branch, of branch unless it is
	// empty, each with its newest delivery, its URL and what it renders, but no ClusterName.
	Environments(ctx context.Context, repoID uuid.UUID, branch string) ([]Environment, error)
	// Environment finds one of the repository's live environments, as Environments has them, or repos.ErrNotFound.
	Environment(ctx context.Context, repoID, id uuid.UUID) (Environment, error)
	// EnvironmentObjects lists what a live environment holds, in order: repos.ErrNotFound without it.
	EnvironmentObjects(ctx context.Context, repoID, environmentID uuid.UUID) ([]EnvironmentObject, error)
	// Deliveries lists at most limit of a live environment's deliveries of kinds (every kind when it is empty), newest
	// first, numbered before before (0 for the newest): repos.ErrNotFound without the environment.
	Deliveries(ctx context.Context, repoID, environmentID uuid.UUID, kinds []DeliveryKind, before int64, limit int) ([]Delivery, error)
	// DeliveryTargets lists the targets of the default branch's /cobranch.yaml, in its order; none without the file.
	DeliveryTargets(ctx context.Context, repoID uuid.UUID) ([]Target, error)
	// ClusterSecrets finds those of ids that are secrets of the repository's organization.
	ClusterSecrets(ctx context.Context, repoID uuid.UUID, ids []uuid.UUID) ([]ClusterSecret, error)
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
	// KeepRender keeps a rendering delivery's render, once: ErrDeliveryNotRendering when it doesn't render, or has
	// one.
	KeepRender(ctx context.Context, deliveryID uuid.UUID, manifests []byte) error
}

// Environments lists the repository's live environments, by target and then branch, those of branch alone when it is
// given, each with its newest delivery, its URL, what it renders and, for a viewer who may manage its cluster's
// secret, the secret's name; it takes repo.read.
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
	if err := s.nameEnvironmentClusters(ctx, v, repoID, list); err != nil {
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
	if e, err = s.nameEnvironmentCluster(ctx, v, repoID, e); err != nil {
		return Environment{}, failed(span, "reading the environment", err)
	}
	return e, nil
}

// EnvironmentObjects lists what one of the repository's environments holds, in the order its last apply applied it,
// each with its health as last read; it takes repo.read. One that isn't there is repos.ErrNotFound.
func (s *Service) EnvironmentObjects(ctx context.Context, v Viewer, repoID, environmentID uuid.UUID) ([]EnvironmentObject, error) {
	ctx, span := tracer.Start(ctx, "code.Service.EnvironmentObjects")
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.repository.id", repoID.String()), attribute.String("cobranch.environment.id", environmentID.String()))
	if err := s.read(ctx, v, repoID); err != nil {
		return nil, failed(span, "reading the environment's objects", err)
	}
	list, err := s.store.EnvironmentObjects(ctx, repoID, environmentID)
	if err != nil {
		return nil, failed(span, "reading the environment's objects", err)
	}
	return list, nil
}

// DeliveryTargets lists the targets the repository's default branch's /cobranch.yaml declares, in its order, each with
// its cluster's secret's name for a viewer who may manage it; it takes repo.read.
func (s *Service) DeliveryTargets(ctx context.Context, v Viewer, repoID uuid.UUID) ([]DeliveryTarget, error) {
	ctx, span := tracer.Start(ctx, "code.Service.DeliveryTargets")
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.repository.id", repoID.String()))
	if err := s.read(ctx, v, repoID); err != nil {
		return nil, failed(span, "reading the targets", err)
	}
	targets, err := s.store.DeliveryTargets(ctx, repoID)
	if err != nil {
		return nil, failed(span, "reading the targets", err)
	}
	var ids []uuid.UUID
	for _, t := range targets {
		if t.Cluster != nil {
			ids = append(ids, *t.Cluster)
		}
	}
	names, err := s.clusterNames(ctx, v, repoID, ids)
	if err != nil {
		return nil, failed(span, "reading the targets", err)
	}
	out := make([]DeliveryTarget, 0, len(targets))
	for _, t := range targets {
		d := DeliveryTarget{Target: t}
		if t.Cluster != nil {
			d.ClusterName = names[*t.Cluster]
		}
		out = append(out, d)
	}
	return out, nil
}

// nameEnvironmentCluster is e with its cluster's secret named for v, as nameEnvironmentClusters names a list's: the
// environment a read, a pause or a resume answers reads as the list does.
func (s *Service) nameEnvironmentCluster(ctx context.Context, v Viewer, repoID uuid.UUID, e Environment) (Environment, error) {
	list := []Environment{e}
	if err := s.nameEnvironmentClusters(ctx, v, repoID, list); err != nil {
		return Environment{}, err
	}
	return list[0], nil
}

// nameEnvironmentClusters sets the ClusterName of each of envs whose cluster's secret v may manage.
func (s *Service) nameEnvironmentClusters(ctx context.Context, v Viewer, repoID uuid.UUID, envs []Environment) error {
	var ids []uuid.UUID
	for _, e := range envs {
		if e.Cluster != nil {
			ids = append(ids, *e.Cluster)
		}
	}
	names, err := s.clusterNames(ctx, v, repoID, ids)
	if err != nil {
		return err
	}
	for i, e := range envs {
		if e.Cluster != nil {
			envs[i].ClusterName = names[*e.Cluster]
		}
	}
	return nil
}

// clusterNames names those of the secrets of ids, which targets name as their clusters, that v may manage where they
// live: a viewer who may not doesn't see their names among the secrets either.
func (s *Service) clusterNames(ctx context.Context, v Viewer, repoID uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]string, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	list, err := s.store.ClusterSecrets(ctx, repoID, ids)
	if err != nil {
		return nil, fmt.Errorf("finding the clusters' secrets: %w", err)
	}
	names := make(map[uuid.UUID]string, len(list))
	for _, c := range list {
		scope, scopeID := auth.ScopeOrganization, c.OrgID
		if c.RepoID != nil {
			scope, scopeID = auth.ScopeRepository, *c.RepoID
		}
		held, err := s.authz.Holds(ctx, v.ID, scope, scopeID)
		if err != nil {
			return nil, fmt.Errorf("reading the viewer's permissions: %w", err)
		}
		if slices.Contains(held, secrets.PermManage) {
			names[c.ID] = c.Name
		}
	}
	return names, nil
}

// Deliveries lists a page of one of the repository's environments' deliveries of kinds, or of every kind when it is
// empty, newest first, before the delivery numbered before (0 for the newest); it takes repo.read. limit 0 asks for
// DefaultPageSize.
func (s *Service) Deliveries(ctx context.Context, v Viewer, repoID, environmentID uuid.UUID, kinds []DeliveryKind, before int64,
	limit int) (DeliveryPage, error) {
	ctx, span := tracer.Start(ctx, "code.Service.Deliveries")
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.repository.id", repoID.String()), attribute.String("cobranch.environment.id", environmentID.String()))
	if err := s.read(ctx, v, repoID); err != nil {
		return DeliveryPage{}, failed(span, "reading the deliveries", err)
	}
	if limit < 0 || limit > MaxDeliveriesPage || before < 0 {
		return DeliveryPage{}, &orgs.ValidationError{Field: "limit", Code: "limit.invalid", Message: fmt.Sprintf("at most %d deliveries a page", MaxDeliveriesPage)}
	}
	for _, k := range kinds {
		if k != DeliverCommit && k != Resync && k != Teardown {
			return DeliveryPage{}, &orgs.ValidationError{Field: "kind", Code: "kind.invalid", Message: "a delivery's kind is deliver, resync or teardown"}
		}
	}
	if limit == 0 {
		limit = DefaultPageSize
	}
	list, err := s.store.Deliveries(ctx, repoID, environmentID, kinds, before, limit+1)
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
	if e, err = s.nameEnvironmentCluster(ctx, v, repoID, e); err != nil {
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
	if e, err = s.nameEnvironmentCluster(ctx, v, repoID, e); err != nil {
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

// UploadRender keeps the render of one of the repository's deliveries, which its render pod uploads with the
// delivery's own token: ErrForbidden under any other credential, ErrRenderTooLarge past MaxRender, and
// ErrDeliveryNotRendering when the delivery doesn't render, or has its render already. The controller applies it.
func (s *Service) UploadRender(ctx context.Context, v Viewer, repoID uuid.UUID, number int64, manifests []byte) error {
	ctx, span := tracer.Start(ctx, "code.Service.UploadRender")
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.repository.id", repoID.String()), attribute.Int64("cobranch.delivery.number", number),
		attribute.Int("cobranch.render.bytes", len(manifests)))
	if err := s.read(ctx, v, repoID); err != nil {
		return failed(span, "uploading the render", err)
	}
	d, err := s.store.Delivery(ctx, repoID, number)
	if err != nil {
		return failed(span, "uploading the render", err)
	}
	if err := s.authz.Renders(ctx, d.ID); err != nil {
		return failed(span, "uploading the render", err)
	}
	if len(manifests) > MaxRender {
		return failed(span, "uploading the render", ErrRenderTooLarge)
	}
	if err := s.store.KeepRender(ctx, d.ID, manifests); err != nil {
		return failed(span, "uploading the render", err)
	}
	return nil
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
