// Package deliverer is the controller's delivery domain: it takes the queued deliveries, renders each commit in a pod
// in its organization's job namespace with a token of its own, applies the render that pod uploads to the environment's
// namespace, in this installation's cluster or in the one a target's kubeconfig reaches, prunes what the render stopped
// holding, and follows the environment's health. It applies an environment's last render again on a schedule, and
// tears environments down, deleting what they hold. It reaches the deliveries, the pods, the clusters, the secrets and
// the logs only through its ports (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/delivery.md,
// "A delivery's life").
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

// Delivery is a queued delivery the controller claimed: what names it, what it does, its environment and its target.
type Delivery struct {
	ID, OrgID, RepoID, EnvironmentID, TargetID uuid.UUID
	Number                                     int64
	Kind                                       code.DeliveryKind
	// Target is the target's name, Branch and SHA what it delivers (no commit for a teardown), and Namespace the
	// environment's.
	Target, Branch, SHA string
	OrgSlug, RepoSlug   string
	// Cluster is the secret holding the kubeconfig of the environment's cluster; nil for this installation's own.
	Cluster   *uuid.UUID
	Namespace string
}

// environment is the environment d delivers to.
func (d Delivery) environment() Environment {
	return Environment{ID: d.EnvironmentID, OrgID: d.OrgID, RepoID: d.RepoID, TargetID: d.TargetID, Target: d.Target,
		Cluster: d.Cluster, Namespace: d.Namespace}
}

// Working is a delivery the controller follows: one that renders, one whose apply a controller left, or a teardown that
// deletes.
type Working struct {
	ID, OrgID, RepoID uuid.UUID
	Number            int64
	Kind              code.DeliveryKind
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

// Apply is a delivery the controller applies, a commit's or a resync: its render, and what its environment held before
// it.
type Apply struct {
	ID, OrgID, RepoID, EnvironmentID, TargetID uuid.UUID
	Number                                     int64
	Kind                                       code.DeliveryKind
	Target, SHA, Namespace                     string
	// Cluster is the secret holding the kubeconfig of the environment's cluster; nil for this installation's own.
	Cluster *uuid.UUID
	// Revision is the environment's commit before it, empty before its first, and Health its health then.
	Revision string
	Health   code.Health
	// Manifests is the render: the one its pod uploaded, or for a resync the environment's last delivery of a commit
	// to succeed. Rendered is false when a resync finds none.
	Manifests []byte
	Rendered  bool
	// Inventory is what the environment holds: its last inventory.
	Inventory []Object
}

// environment is the environment a applies to.
func (a Apply) environment() Environment {
	return Environment{ID: a.EnvironmentID, OrgID: a.OrgID, RepoID: a.RepoID, TargetID: a.TargetID, Target: a.Target,
		Cluster: a.Cluster, Namespace: a.Namespace}
}

// Teardown is a teardown the controller holds while it deletes: its environment, what that holds, and when it started
// deleting.
type Teardown struct {
	ID, OrgID, RepoID uuid.UUID
	Number            int64
	Environment       Environment
	Inventory         []Object
	StartedAt         time.Time
}

// Outcome is how an apply ended: the environment's new inventory, its health, and how the delivery ended, with the
// annotation its target sets on the commit, which a resync doesn't.
type Outcome struct {
	Apply     Apply
	Inventory []Object
	Health    code.Health
	State     code.DeliveryState
	Reason    string
	Report    code.AnnotationInput
}

// Environment is a live environment: where it is, and whose.
type Environment struct {
	ID, OrgID, RepoID, TargetID uuid.UUID
	// Target is its target's name.
	Target string
	// Cluster is the secret holding the kubeconfig of its cluster; nil for this installation's own.
	Cluster   *uuid.UUID
	Namespace string
}

// Standing is where a delivery stands, and its repository.
type Standing struct {
	RepoID uuid.UUID
	State  code.DeliveryState
}

// Store keeps the deliveries: postgres's.
type Store interface {
	// Claim claims the queued delivery that waited longest, that no controller holds and whose environment isn't
	// applying or deleting another, until leaseUntil, or answers false when none waits.
	Claim(ctx context.Context, leaseUntil time.Time) (Delivery, bool, error)
	// Token records the delivery's token by its hash and hint, until expiresAt, replacing one made before.
	Token(ctx context.Context, deliveryID uuid.UUID, hash []byte, hint string, expiresAt time.Time) error
	// Defer puts a queued delivery off until until, with why.
	Defer(ctx context.Context, deliveryID uuid.UUID, until time.Time, reason string) error
	// MarkRendering marks a queued delivery rendering and its target's annotation In progress; false when it is no
	// longer queued.
	MarkRendering(ctx context.Context, deliveryID uuid.UUID) (bool, error)
	// MarkDeleting marks a queued teardown deleting; false when it is no longer queued.
	MarkDeleting(ctx context.Context, deliveryID uuid.UUID) (bool, error)
	// Working lists the deliveries that render, those whose apply a controller left, and the teardowns no controller
	// holds.
	Working(ctx context.Context) ([]Working, error)
	// TakeApply marks a delivery that rendered, a queued resync, or a delivery whose apply a controller left, applying
	// until leaseUntil, and answers what its apply needs; false when it is none of those.
	TakeApply(ctx context.Context, deliveryID uuid.UUID, leaseUntil time.Time) (Apply, bool, error)
	// Record records how an apply ended: the delivery's end, and, unless it had ended already, the environment's
	// inventory, health, revision and sync and, for a commit's, its target's annotation.
	Record(ctx context.Context, o Outcome) error
	// TakeTeardown holds a teardown that deletes until leaseUntil, once no controller holds it, and answers what its
	// environment holds; false when none may take it.
	TakeTeardown(ctx context.Context, deliveryID uuid.UUID, leaseUntil time.Time) (Teardown, bool, error)
	// LookAgain holds a teardown that deletes until at, when the controller looks at what remains again.
	LookAgain(ctx context.Context, deliveryID uuid.UUID, at time.Time) error
	// EndTeardown ends a teardown in state with reason: one that succeeded ends its environment, unless a newer delivery
	// of it waits, which then holds nothing; one that failed leaves it holding remaining, Out of sync.
	EndTeardown(ctx context.Context, t Teardown, remaining []Object, state code.DeliveryState, reason string) error
	// Schedule queues, for each live environment none of whose deliveries works or waits and whose last ended every
	// ago, its teardown when its branch is gone or the default branch's targets no longer give it one, and otherwise
	// its resync, if it holds a revision and isn't paused. It answers how many it queued.
	Schedule(ctx context.Context, every time.Duration) (int, error)
	// Finish ends a delivery that hasn't ended, in state with reason, sets its target's annotation to match and deletes
	// its token; false when it had ended already.
	Finish(ctx context.Context, deliveryID uuid.UUID, state code.DeliveryState, reason string) (bool, error)
	// Deliveries finds those of ids that are deliveries: where each stands, and its repository.
	Deliveries(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]Standing, error)
	// DropTokens deletes the tokens of deliveries that no longer render, and those that expired, and answers how many.
	DropTokens(ctx context.Context) (int64, error)
	// DropRenders deletes each environment's renders past its newest keep, but those of deliveries that haven't ended
	// and its last to succeed, and answers how many.
	DropRenders(ctx context.Context, keep int) (int64, error)
	// Progressing lists the live environments whose health is Progressing.
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

// Target is a cluster, as deliveries apply to it and teardowns delete from it: this installation's own, the in-cluster
// adapter's, or another, which Clusters opens.
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
	// Teardown deletes those of objects that are still the environment's, CRDs never, and answers which of them
	// remain, as finalizers hold them; one gone counts as deleted.
	Teardown(ctx context.Context, env Environment, objects []Object) ([]Object, error)
	// Release deletes the environment's namespace if Cobranch made it, and answers whether it is gone, or was never
	// Cobranch's.
	Release(ctx context.Context, env Environment) (bool, error)
}

// Clusters opens the other clusters, each through a kubeconfig: the remote adapter's.
type Clusters interface {
	// Open is the cluster kubeconfig reaches; ErrRefused, saying why, for one it won't use.
	Open(ctx context.Context, kubeconfig []byte) (Target, error)
}

// Secrets hands a principal the values of the secrets it may read: secrets.Service's Receive.
type Secrets interface {
	Receive(ctx context.Context, principalID uuid.UUID, ids []uuid.UUID) (map[uuid.UUID][]byte, error)
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
	// KeepRenders is how many renders each environment keeps; a sweep deletes older ones.
	KeepRenders int
	// Resync is how often each environment's last render is applied again; TeardownTimeout how long a teardown waits
	// for what it deleted to go.
	Resync, TeardownTimeout time.Duration
}

// Service delivers the deliveries.
type Service struct {
	store    Store
	pods     Pods
	target   Target
	clusters Clusters
	secrets  Secrets
	archive  Archive
	settings Settings
	now      func() time.Time

	mu sync.Mutex
	// read is when each Progressing environment's health was last read.
	read map[uuid.UUID]time.Time
}

// Ports are what a Service works through. An installation that delivers into no cluster of its own has no Target:
// nil, and deliveries to it fail, saying so; one that runs no pods has NoPods.
type Ports struct {
	Store    Store
	Pods     Pods
	Target   Target
	Clusters Clusters
	Secrets  Secrets
	Archive  Archive
}

// NewService returns a Service that renders the store's deliveries in pods and applies them to this installation's
// cluster, or to the one a target's kubeconfig, a secret it reads, reaches, keeping the renders' logs in the archive.
func NewService(p Ports, settings Settings) *Service {
	return &Service{store: p.Store, pods: p.Pods, target: p.Target, clusters: p.Clusters, secrets: p.Secrets, archive: p.Archive,
		settings: settings, now: time.Now, read: map[uuid.UUID]time.Time{}}
}

// SetClock replaces the Service's clock, for tests.
func (s *Service) SetClock(now func() time.Time) { s.now = now }
