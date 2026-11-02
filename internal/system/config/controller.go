package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/kelseyhightower/envconfig"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// JSON is a variable whose value is a JSON document, as the chart renders a value of its own that is a map or a list.
type JSON[T any] struct {
	Value T
}

// Decode reads value as JSON; an empty value leaves T's zero.
func (j *JSON[T]) Decode(value string) error {
	if value == "" {
		return nil
	}
	if err := json.Unmarshal([]byte(value), &j.Value); err != nil {
		return fmt.Errorf("reading JSON: %w", err)
	}
	return nil
}

// Controller configures controller, which runs the jobs' runs in the cluster it runs in
// (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/jobs.md, "Where a run executes"), and
// delivers into it (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/delivery.md, "The chart").
type Controller struct {
	HTTPAddr        string        `envconfig:"HTTP_ADDR" default:":8080"`
	DatabaseURL     string        `envconfig:"DATABASE_URL"`
	ShutdownTimeout time.Duration `envconfig:"SHUTDOWN_TIMEOUT" default:"20s"`
	// BlobURL names the object store, as the Git server's: the controller keeps ended runs' logs there.
	BlobURL string `envconfig:"BLOB_URL"`
	// SecretsKey opens the secrets the runs' jobs receive; without one, a run of a job that names a secret fails.
	SecretsKey string `envconfig:"SECRETS_KEY"`
	// PollInterval is how often it starts queued runs and follows running ones; SweepInterval how often it cleans up
	// after runs that ended otherwise.
	PollInterval  time.Duration `envconfig:"JOBS_POLL_INTERVAL" default:"5s"`
	SweepInterval time.Duration `envconfig:"JOBS_SWEEP_INTERVAL" default:"5m"`
	Jobs
	Agents Agents
	Delivery
}

// Agents are the installation's settings for agents' tasks, from the chart's agents values, in the COBRANCH_AGENTS_
// variables: their pods run in the job namespaces, as runs' do.
type Agents struct {
	// Image runs the harness, with the tools agents use; empty for Cobranch's own, CheckoutImage.
	Image string `envconfig:"IMAGE"`
	// Resources are the harness's requests and limits.
	Resources JSON[corev1.ResourceRequirements] `envconfig:"RESOURCES"`
	// Timeout is the longest a task may run.
	Timeout time.Duration `envconfig:"TIMEOUT" default:"24h"`
	// Idle is how long a task that waits for an answer keeps its pod.
	Idle time.Duration `envconfig:"IDLE" default:"30m"`
	// MaxTurns, ShellTimeout and MaxTokens bound the harness: its model's turns, a shell command, and a turn's tokens.
	MaxTurns     int           `envconfig:"MAX_TURNS" default:"200"`
	ShellTimeout time.Duration `envconfig:"SHELL_TIMEOUT" default:"10m"`
	MaxTokens    int           `envconfig:"MODEL_MAX_TOKENS" default:"8192"`
	// ProviderURLs are where the model proxy sends a provider's requests, by provider, when not to its own address: a
	// gateway of the operator's that speaks its protocol.
	ProviderURLs JSON[map[string]string] `envconfig:"PROVIDER_URLS"`
}

// Delivery are the installation's settings for deliveries, from the chart's delivery values: each renders in a pod of
// its organization's job namespace, and those of targets without a cluster apply into this installation's, when
// InCluster, in namespaces fenced as the job namespaces are, with jobs' egress.
type Delivery struct {
	// InCluster is whether targets without a cluster deliver into this installation's cluster.
	InCluster bool `envconfig:"DELIVERY_IN_CLUSTER" default:"false"`
	// DeliveryPodSecurity is the Pod Security Standard level the environments' namespaces enforce, and
	// DeliveryLabels more labels they carry, the operator's.
	DeliveryPodSecurity string                  `envconfig:"DELIVERY_POD_SECURITY" default:"restricted"`
	DeliveryLabels      JSON[map[string]string] `envconfig:"DELIVERY_NAMESPACE_LABELS"`
	// Account is the ServiceAccount the controller applies as, in its own namespace; DeliveryRole the ClusterRole it
	// grants the account in each environment's namespace, and FenceRole the one it grants itself there.
	Account      string `envconfig:"DELIVERY_ACCOUNT"`
	DeliveryRole string `envconfig:"DELIVERY_ROLE"`
	FenceRole    string `envconfig:"DELIVERY_FENCE_ROLE"`
	// IngressFrom selects the namespaces whose pods may reach an environment's.
	IngressFrom JSON[[]metav1.LabelSelector] `envconfig:"DELIVERY_INGRESS_FROM"`
	// DeliveryResources are a container's defaults in an environment's namespace; DeliveryQuota the most one
	// environment takes.
	DeliveryResources JSON[corev1.ResourceRequirements] `envconfig:"DELIVERY_RESOURCES"`
	DeliveryQuota     JSON[corev1.ResourceList]         `envconfig:"DELIVERY_QUOTA"`
	// RenderTimeout is how long a render may take, and RenderResources its pod's container's requests and limits.
	RenderTimeout   time.Duration                     `envconfig:"DELIVERY_RENDER_TIMEOUT" default:"10m"`
	RenderResources JSON[corev1.ResourceRequirements] `envconfig:"DELIVERY_RENDER_RESOURCES"`
	// HealthInterval is how often a Progressing environment's health is read again.
	HealthInterval time.Duration `envconfig:"DELIVERY_HEALTH_INTERVAL" default:"15s"`
	// RendersKeep is how many renders each environment keeps; the controller's sweep deletes older ones.
	RendersKeep int `envconfig:"DELIVERY_RENDERS_KEEP" default:"10"`
}

// Jobs are the installation's settings for runs, from the chart's jobs values.
type Jobs struct {
	// Pods is whether runs execute pods at all.
	Pods bool `envconfig:"JOBS_PODS" default:"true"`
	// Lease is how long a claim holds a queued run; RoomRetry how long a run the quota refused waits.
	Lease     time.Duration `envconfig:"JOBS_LEASE" default:"2m"`
	RoomRetry time.Duration `envconfig:"JOBS_ROOM_RETRY" default:"30s"`
	// Timeout is a run's when its job names none; MaxTimeout the most any run's may be.
	Timeout    time.Duration `envconfig:"JOBS_TIMEOUT" default:"1h"`
	MaxTimeout time.Duration `envconfig:"JOBS_MAX_TIMEOUT" default:"24h"`
	// Vanished is how long a running run may show no pod before it fails.
	Vanished time.Duration `envconfig:"JOBS_VANISHED" default:"1m"`
	// ScriptImage runs a script whose job names no image; CheckoutImage, Cobranch's own, checks a run's commit out.
	ScriptImage   string `envconfig:"JOBS_SCRIPT_IMAGE" required:"true"`
	CheckoutImage string `envconfig:"JOBS_CHECKOUT_IMAGE" required:"true"`
	// RESTURL and GitURL are where runs reach the REST API and the Git server.
	RESTURL string `envconfig:"JOBS_REST_URL"`
	GitURL  string `envconfig:"JOBS_GIT_URL"`
	// LogsMaxBytes is the most of each container's log the controller keeps.
	LogsMaxBytes Size `envconfig:"JOBS_LOGS_MAX_BYTES" default:"10Mi"`
	// WebhooksAllowedCIDRs are ranges of EgressExcept that webhooks may reach all the same: a private receiver's.
	WebhooksAllowedCIDRs []string `envconfig:"JOBS_WEBHOOKS_ALLOWED_CIDRS"`
	Namespaces
}

// Namespaces are the settings of the organizations' job namespaces and the pods in them.
type Namespaces struct {
	// NamespacePrefix starts every job namespace's name; Installation labels them as this installation's.
	NamespacePrefix string `envconfig:"JOBS_NAMESPACE_PREFIX" required:"true"`
	Installation    string `envconfig:"JOBS_INSTALLATION" required:"true"`
	// PodSecurity is the Pod Security Standard level they enforce.
	PodSecurity     string                  `envconfig:"JOBS_POD_SECURITY" default:"restricted"`
	NamespaceLabels JSON[map[string]string] `envconfig:"JOBS_NAMESPACE_LABELS"`
	// Home is the installation's namespace, ServiceAccount the controller's there, Release the chart's release, whose
	// servers runs may reach, RunnerRole the ClusterRole the controller binds itself to in each namespace, and
	// OwnerRole its own ClusterRole, which owns them.
	Home           string `envconfig:"JOBS_HOME" required:"true"`
	ServiceAccount string `envconfig:"JOBS_SERVICE_ACCOUNT" required:"true"`
	Release        string `envconfig:"JOBS_RELEASE" required:"true"`
	RunnerRole     string `envconfig:"JOBS_RUNNER_ROLE" required:"true"`
	OwnerRole      string `envconfig:"JOBS_OWNER_ROLE" required:"true"`
	// LogsRole is the ClusterRole it binds the servers' ServiceAccount, LogsReader, to in each namespace, so that the
	// REST and GraphQL servers read the runs' logs; none without it.
	LogsRole   string `envconfig:"JOBS_LOGS_ROLE"`
	LogsReader string `envconfig:"JOBS_LOGS_READER"`
	// EgressExcept are the address ranges runs may not reach; EgressDNS the NetworkPolicy peers they may ask DNS, on
	// port 53, any address without them.
	EgressExcept []string                               `envconfig:"JOBS_EGRESS_EXCEPT"`
	EgressDNS    JSON[[]networkingv1.NetworkPolicyPeer] `envconfig:"JOBS_EGRESS_DNS"`
	// Resources are a container's defaults; Quota the most one organization's runs take at once.
	Resources JSON[corev1.ResourceRequirements] `envconfig:"JOBS_RESOURCES"`
	Quota     JSON[corev1.ResourceList]         `envconfig:"JOBS_QUOTA"`
	// NodeSelector, Tolerations and Affinity place the runs' pods.
	NodeSelector JSON[map[string]string]   `envconfig:"JOBS_NODE_SELECTOR"`
	Tolerations  JSON[[]corev1.Toleration] `envconfig:"JOBS_TOLERATIONS"`
	Affinity     JSON[*corev1.Affinity]    `envconfig:"JOBS_AFFINITY"`
	// ImagePullSecrets name the Secrets in Home that pull the images the agents' tasks run, which the controller copies
	// into every job namespace.
	ImagePullSecrets []string `envconfig:"JOBS_IMAGE_PULL_SECRETS"`
}

// LoadController reads controller's configuration from the environment.
func LoadController() (Controller, error) {
	var c Controller
	if err := envconfig.Process(Prefix, &c); err != nil {
		return Controller{}, fmt.Errorf("reading controller's configuration from the COBRANCH_ variables: %w", err)
	}
	switch {
	case c.PollInterval <= 0 || c.SweepInterval <= 0 || c.Lease <= 0 || c.RoomRetry <= 0:
		return Controller{}, errors.New("the controller's intervals, lease and retry must be positive")
	case c.Timeout <= 0 || c.MaxTimeout < c.Timeout:
		return Controller{}, fmt.Errorf("COBRANCH_JOBS_TIMEOUT is %s and COBRANCH_JOBS_MAX_TIMEOUT %s: a run's default must be positive and within the most", c.Timeout, c.MaxTimeout)
	case c.LogsMaxBytes <= 0:
		return Controller{}, errors.New("COBRANCH_JOBS_LOGS_MAX_BYTES must be positive")
	case c.Agents.Timeout <= 0 || c.Agents.Idle <= 0 || c.Agents.MaxTurns <= 0 || c.Agents.ShellTimeout <= 0 || c.Agents.MaxTokens <= 0:
		return Controller{}, errors.New("COBRANCH_AGENTS_TIMEOUT, COBRANCH_AGENTS_IDLE, COBRANCH_AGENTS_MAX_TURNS, COBRANCH_AGENTS_SHELL_TIMEOUT and COBRANCH_AGENTS_MODEL_MAX_TOKENS must be positive")
	case c.RenderTimeout <= 0 || c.HealthInterval <= 0:
		return Controller{}, errors.New("COBRANCH_DELIVERY_RENDER_TIMEOUT and COBRANCH_DELIVERY_HEALTH_INTERVAL must be positive")
	case c.RendersKeep < 1:
		return Controller{}, errors.New("COBRANCH_DELIVERY_RENDERS_KEEP must be positive: an environment keeps the render it delivered")
	case c.InCluster && (c.Account == "" || c.DeliveryRole == "" || c.FenceRole == ""):
		return Controller{}, errors.New("delivering into this installation's cluster needs COBRANCH_DELIVERY_ACCOUNT, COBRANCH_DELIVERY_ROLE and COBRANCH_DELIVERY_FENCE_ROLE")
	}
	if c.Agents.Image == "" {
		c.Agents.Image = c.CheckoutImage
	}
	for provider, raw := range c.Agents.ProviderURLs.Value {
		var u URL
		if err := u.Decode(raw); err != nil || u.URL == nil {
			return Controller{}, fmt.Errorf("COBRANCH_AGENTS_PROVIDER_URLS names %s at %q, not an absolute http or https URL", provider, raw)
		}
	}
	return c, nil
}

// Checkout configures checkout, which fetches a run's commit into its workspace, from the run's variables.
type Checkout struct {
	GitURL string `envconfig:"GIT_URL" required:"true"`
	Token  string `envconfig:"TOKEN" required:"true"`
	SHA    string `envconfig:"SHA" required:"true"`
	// Workspace is the directory the commit is checked out in.
	Workspace string `envconfig:"WORKSPACE" default:"/workspace"`
}

// LoadCheckout reads checkout's configuration from the environment, the variables every container of a run gets.
func LoadCheckout() (Checkout, error) {
	var c Checkout
	if err := envconfig.Process(Prefix, &c); err != nil {
		return Checkout{}, fmt.Errorf("reading checkout's configuration from the COBRANCH_ variables: %w", err)
	}
	return c, nil
}

// Render configures `cobranch render` in a delivery's render pod: where it uploads the render, and the delivery's
// token it uploads with. Without a URL, it prints the render.
type Render struct {
	URL   string `envconfig:"UPLOAD_URL"`
	Token string `envconfig:"TOKEN"`
}

// LoadRender reads render's configuration from the environment, the variables a render pod's containers get.
func LoadRender() (Render, error) {
	var r Render
	if err := envconfig.Process(Prefix, &r); err != nil {
		return Render{}, fmt.Errorf("reading render's configuration from the COBRANCH_ variables: %w", err)
	}
	if r.URL != "" && r.Token == "" {
		return Render{}, errors.New("COBRANCH_UPLOAD_URL needs COBRANCH_TOKEN, the delivery's token")
	}
	return r, nil
}
