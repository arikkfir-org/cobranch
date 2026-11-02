// Package deliverer is the controller's delivery domain: it takes the queued deliveries of commits to this
// installation's cluster, renders each in a pod in its organization's job namespace with a token of its own, applies the
// render that pod uploads to the environment's namespace, prunes what the render stopped holding, and follows the
// environment's health. It reaches the deliveries, the pods, the cluster and the logs only through its ports
// (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/delivery.md, "A delivery's life").
package deliverer

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"

	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/runner"
)

var tracer = otel.Tracer("cobranch.dev/internal/services/deliverer")

// Delivery is a queued delivery of a commit the controller claimed: what names it, its environment and its target.
type Delivery struct {
	ID, OrgID, RepoID, EnvironmentID, TargetID uuid.UUID
	Number                                     int64
	// Target is the target's name, Branch and SHA what it delivers, and Namespace the environment's.
	Target, Branch, SHA string
	OrgSlug, RepoSlug   string
	Namespace           string
}

// Working is a delivery the controller follows: one that renders, or one whose apply a controller left.
type Working struct {
	ID, OrgID, RepoID uuid.UUID
	Number            int64
	State             code.DeliveryState
	StartedAt         time.Time
	// Rendered is whether its render pod uploaded its render.
	Rendered bool
}

// Object is an object an environment holds, as its inventory records it, with its health as last read.
type Object struct {
	Group, Version, Kind, Namespace, Name, UID string
	Health                                     code.Health
	Message                                    string
}

// key is what makes an object the same one across renders: its group, kind, namespace and name.
func (o Object) key() string { return o.Group + "/" + o.Kind + "/" + o.Namespace + "/" + o.Name }

// String names the object as kubectl does, kind/name.
func (o Object) String() string { return o.Kind + "/" + o.Name }

// Apply is a delivery the controller applies: its render, and what its environment held before it.
type Apply struct {
	ID, OrgID, RepoID, EnvironmentID, TargetID uuid.UUID
	Number                                     int64
	Target, SHA, Namespace                     string
	// Revision is the environment's commit before it, empty before its first, and Health its health then.
	Revision string
	Health   code.Health
	// Manifests is the render its pod uploaded.
	Manifests []byte
	// Inventory is what the environment holds: its last inventory.
	Inventory []Object
}

// Outcome is how an apply ended: the environment's new inventory, its health, and how the delivery ended, with the
// annotation its target sets on the commit.
type Outcome struct {
	Apply     Apply
	Inventory []Object
	Health    code.Health
	State     code.DeliveryState
	Reason    string
	Report    code.AnnotationInput
}

// Environment is a live environment of this installation's cluster whose health the controller follows.
type Environment struct {
	ID, OrgID, RepoID uuid.UUID
	Namespace         string
}

// Standing is where a delivery stands, and its repository.
type Standing struct {
	RepoID uuid.UUID
	State  code.DeliveryState
}

// Store keeps the deliveries: postgres's.
type Store interface {
	// Claim claims the queued delivery of a commit to this installation's cluster that waited longest, that no
	// controller holds and whose environment isn't applying another, until leaseUntil, or answers false when none
	// waits.
	Claim(ctx context.Context, leaseUntil time.Time) (Delivery, bool, error)
	// Token records the delivery's token by its hash and hint, until expiresAt, replacing one made before.
	Token(ctx context.Context, deliveryID uuid.UUID, hash []byte, hint string, expiresAt time.Time) error
	// Defer puts a queued delivery off until until, with why.
	Defer(ctx context.Context, deliveryID uuid.UUID, until time.Time, reason string) error
	// MarkRendering marks a queued delivery rendering and its target's annotation In progress; false when it is no
	// longer queued.
	MarkRendering(ctx context.Context, deliveryID uuid.UUID) (bool, error)
	// Working lists the deliveries that render, and those whose apply a controller left.
	Working(ctx context.Context) ([]Working, error)
	// TakeApply marks a delivery that rendered, or whose apply a controller left, applying until leaseUntil, and
	// answers what its apply needs; false when it neither rendered nor was left.
	TakeApply(ctx context.Context, deliveryID uuid.UUID, leaseUntil time.Time) (Apply, bool, error)
	// Record records how an apply ended: the environment's inventory, health, revision and sync, the delivery's end,
	// and its target's annotation.
	Record(ctx context.Context, o Outcome) error
	// Finish ends a delivery that hasn't ended, in state with reason, sets its target's annotation to match and deletes
	// its token; false when it had ended already.
	Finish(ctx context.Context, deliveryID uuid.UUID, state code.DeliveryState, reason string) (bool, error)
	// Deliveries finds those of ids that are deliveries: where each stands, and its repository.
	Deliveries(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]Standing, error)
	// DropTokens deletes the tokens of deliveries that no longer render, and those that expired, and answers how many.
	DropTokens(ctx context.Context) (int64, error)
	// Progressing lists the live environments of this installation's cluster whose health is Progressing.
	Progressing(ctx context.Context) ([]Environment, error)
	// Objects lists what an environment holds, in order.
	Objects(ctx context.Context, environmentID uuid.UUID) ([]Object, error)
	// SetHealth records the health of an environment's objects and its own, and, when its own changed, sets the
	// annotation of its revision's delivery to report.
	SetHealth(ctx context.Context, environmentID uuid.UUID, objects []Object, health code.Health, report code.AnnotationInput) error
}

// Render is a delivery's render pod, as its organization's job namespace starts it: `cobranch render` with Args, its
// commit checked out first.
type Render struct {
	Delivery Delivery
	// Args are `cobranch render`'s flags.
	Args []string
	// Env is what the pod's containers get: the token among them. The cluster keeps it in the pod's Secret.
	Env map[string]string
	// Masked are the values its logs never show: the token.
	Masked  []string
	Timeout time.Duration
}

// Pods runs the render pods in the organizations' job namespaces: the job cluster's.
type Pods interface {
	// Prepare makes the organization's job namespace ready, creating or updating it and what it holds.
	Prepare(ctx context.Context, orgID uuid.UUID) error
	// StartRender creates the delivery's render pod and its Secret, unless they are there already; runner.ErrNoRoom
	// when the quota refuses it, runner.ErrRefused when the cluster refuses its spec.
	StartRender(ctx context.Context, r Render) error
	// Renders reports the organization's render pods, by delivery ID.
	Renders(ctx context.Context, orgID uuid.UUID) (map[uuid.UUID]runner.Pod, error)
	// RenderLogs reads each of the delivery's render pod's containers' logs, at most limit bytes each, its masked
	// values hidden, by container; none for a pod that is gone.
	RenderLogs(ctx context.Context, orgID, deliveryID uuid.UUID, limit int64) (map[string][]byte, error)
	// RemoveRender deletes the delivery's render pod, and its Secret with it.
	RemoveRender(ctx context.Context, orgID, deliveryID uuid.UUID) error
	// Namespaces lists the organizations that have a job namespace.
	Namespaces(ctx context.Context) ([]uuid.UUID, error)
}

// ErrRefused is a render, or an environment's namespace, the cluster won't take: Apply and Prepare wrap it with why,
// and the delivery fails, saying so.
var ErrRefused = errors.New("refused")

// Target is this installation's cluster, as deliveries apply to it: the in-cluster adapter's.
type Target interface {
	// Capabilities are the cluster's Kubernetes version and the API versions it serves, which a render renders for.
	Capabilities(ctx context.Context) (string, []string, error)
	// Prepare makes the environment's namespace, labels it and fences it, or refuses one that exists without its
	// labels (ErrRefused).
	Prepare(ctx context.Context, env Environment) error
	// Apply applies the render to the environment's namespace, all of it or, when an object is refused (ErrRefused),
	// none of it, and answers what it applied, in order: on an error partway too.
	Apply(ctx context.Context, env Environment, manifests []byte) ([]Object, error)
	// Prune deletes what previous holds and current doesn't, when it is still the environment's.
	Prune(ctx context.Context, env Environment, previous, current []Object) error
	// Health reads the objects' healths, in order, and the worst of them.
	Health(ctx context.Context, objects []Object) ([]Object, code.Health, error)
}

// Archive keeps ended renders' logs: the object store.
type Archive interface {
	// Put writes a delivery's render pod's container's log.
	Put(ctx context.Context, repoID, deliveryID uuid.UUID, container string, log []byte) error
}

// Settings are the installation's for deliveries.
type Settings struct {
	// Pods is whether the installation runs pods: a delivery renders in one.
	Pods bool
	// Lease is how long a claim holds a queued delivery, or an apply a delivery, before another controller may take
	// it; RoomRetry how long a delivery the quota refused waits before it is tried again.
	Lease, RoomRetry time.Duration
	// Timeout is how long a render may take.
	Timeout time.Duration
	// RESTURL is the REST API's URL, as render pods reach it; GitURL the Git server's.
	RESTURL, GitURL string
	// LogLimit is the most of each render container's log the archive keeps, in bytes.
	LogLimit int64
	// Vanished is how long a rendering delivery may show no pod before it fails.
	Vanished time.Duration
	// HealthEvery is how often a Progressing environment's health is read again.
	HealthEvery time.Duration
}

// Service delivers the deliveries.
type Service struct {
	store    Store
	pods     Pods
	target   Target
	archive  Archive
	settings Settings
	now      func() time.Time

	mu sync.Mutex
	// read is when each Progressing environment's health was last read.
	read map[uuid.UUID]time.Time
}

// NewService returns a Service that renders store's deliveries in pods and applies them to target, keeping the
// renders' logs in archive. An installation that delivers into no cluster of its own has no target: nil, and its
// deliveries fail, saying so; one that runs no pods has NoPods.
func NewService(store Store, pods Pods, target Target, archive Archive, settings Settings) *Service {
	return &Service{store: store, pods: pods, target: target, archive: archive, settings: settings, now: time.Now,
		read: map[uuid.UUID]time.Time{}}
}

// SetClock replaces the Service's clock, for tests.
func (s *Service) SetClock(now func() time.Time) { s.now = now }
