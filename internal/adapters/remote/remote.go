// Package remote delivers into other clusters, each through the kubeconfig a target's secret holds: deliverer.Clusters
// on client-go. A kubeconfig is one context with a token or a client certificate; the controller refuses exec plugins
// and auth providers, which it would run, and files on its own disk, which it would read. It connects to the cluster's
// API server only outside the ranges runs may not reach, unless the operator allowed them, and through no proxy. The
// other cluster's RBAC is the boundary: the controller applies, prunes, reads and deletes as the kubeconfig's identity
// (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/delivery.md, "Other clusters").
package remote

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"cobranch.dev/internal/adapters/cluster"
	"cobranch.dev/internal/adapters/egress"
	"cobranch.dev/internal/services/deliverer"
)

var tracer = otel.Tracer("cobranch.dev/internal/adapters/remote")

const (
	// timeout is how long a connection to another cluster's API server, and one request, may take.
	timeout = 30 * time.Second
	// kept is the most clusters Clusters keeps open; it forgets them all past it.
	kept = 64
)

// ErrUnreachable is an address of another cluster's API server that the controller may not connect to.
var ErrUnreachable = errors.New("the controller may not connect to this address")

// Clusters opens other clusters through their kubeconfigs (deliverer.Clusters), keeping each it opened for the next
// delivery to the same kubeconfig.
type Clusters struct {
	installation string
	guard        egress.Guard
	mu           sync.Mutex
	open         map[[sha256.Size]byte]*Target
}

var _ deliverer.Clusters = (*Clusters)(nil)

// New returns the Clusters that label the namespaces they make as installation's, and connect to no address in except,
// the ranges runs may not reach, unless allowed holds it too.
func New(installation string, except, allowed []string) (*Clusters, error) {
	deny, err := egress.Prefixes(except)
	if err != nil {
		return nil, fmt.Errorf("reading the ranges other clusters may not be in: %w", err)
	}
	allow, err := egress.Prefixes(allowed)
	if err != nil {
		return nil, fmt.Errorf("reading the ranges other clusters may be in: %w", err)
	}
	return &Clusters{installation: installation, guard: egress.Guard{Deny: deny, Allow: allow, Refused: ErrUnreachable},
		open: map[[sha256.Size]byte]*Target{}}, nil
}

// Open is the cluster kubeconfig reaches, refused (deliverer.ErrRefused) when the controller won't use it
// (deliverer.Clusters).
func (c *Clusters) Open(ctx context.Context, kubeconfig []byte) (deliverer.Target, error) {
	_, span := tracer.Start(ctx, "remote.Clusters.Open")
	defer span.End()
	key := sha256.Sum256(kubeconfig)
	c.mu.Lock()
	defer c.mu.Unlock()
	if t, ok := c.open[key]; ok {
		return t, nil
	}
	cfg, err := c.config(kubeconfig)
	if err != nil {
		return nil, err
	}
	t, err := newTarget(cfg, c.installation)
	if err != nil {
		return nil, err
	}
	if len(c.open) >= kept {
		clear(c.open)
	}
	c.open[key] = t
	return t, nil
}

// config is the client configuration of kubeconfig, which connects only where the guard lets it, through no proxy.
func (c *Clusters) config(kubeconfig []byte) (*rest.Config, error) {
	parsed, err := clientcmd.Load(kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("%w: it doesn't parse: %w", deliverer.ErrRefused, err)
	}
	name, err := checked(parsed)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", deliverer.ErrRefused, err)
	}
	cfg, err := clientcmd.NewDefaultClientConfig(*parsed, &clientcmd.ConfigOverrides{CurrentContext: name}).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("%w: %w", deliverer.ErrRefused, err)
	}
	cfg.Dial = c.guard.Dialer(timeout).DialContext
	cfg.Proxy = func(*http.Request) (*url.URL, error) { return nil, nil } // a proxy would connect for it, past the guard
	cfg.Timeout = timeout
	cfg.UserAgent = "cobranch-controller"
	return cfg, nil
}

// checked refuses a kubeconfig the controller won't use, saying why, and answers its one context's name: it has one
// context, with a cluster at an https server that checks the server's certificate, and a user with a token or a
// client certificate and key, all given inline. Exec plugins and auth providers would run on the controller, and files
// would be read from its disk.
func checked(cfg *clientcmdapi.Config) (string, error) {
	if len(cfg.Contexts) != 1 {
		return "", fmt.Errorf("it has %d contexts, and must have one", len(cfg.Contexts))
	}
	var name string
	var kctx *clientcmdapi.Context
	for n, c := range cfg.Contexts {
		name, kctx = n, c
	}
	server, ok := cfg.Clusters[kctx.Cluster]
	if !ok {
		return "", fmt.Errorf("context %s names cluster %q, which it doesn't have", name, kctx.Cluster)
	}
	if err := checkServer(server); err != nil {
		return "", fmt.Errorf("cluster %s: %w", kctx.Cluster, err)
	}
	user, ok := cfg.AuthInfos[kctx.AuthInfo]
	if !ok {
		return "", fmt.Errorf("context %s names user %q, which it doesn't have", name, kctx.AuthInfo)
	}
	if err := checkUser(user); err != nil {
		return "", fmt.Errorf("user %s: %w", kctx.AuthInfo, err)
	}
	return name, nil
}

// checkServer refuses a cluster the controller would reach other than over https, checking its certificate, directly.
func checkServer(c *clientcmdapi.Cluster) error {
	u, err := url.Parse(c.Server)
	switch {
	case err != nil:
		return fmt.Errorf("its server doesn't parse: %w", err)
	case u.Scheme != "https" || u.Host == "":
		return errors.New("its server must be an https URL")
	case c.InsecureSkipTLSVerify:
		return errors.New("insecure-skip-tls-verify is refused: give certificate-authority-data instead")
	case c.CertificateAuthority != "":
		return errors.New("certificate-authority names a file: give certificate-authority-data instead")
	case c.ProxyURL != "":
		return errors.New("proxy-url is refused: the controller connects to the server directly")
	}
	return nil
}

// checkUser refuses a user other than one with a token or a client certificate and key, given inline.
func checkUser(u *clientcmdapi.AuthInfo) error {
	var refused []string
	if u.Exec != nil {
		refused = append(refused, "exec plugins")
	}
	if u.AuthProvider != nil {
		refused = append(refused, "auth providers")
	}
	if u.TokenFile != "" || u.ClientCertificate != "" || u.ClientKey != "" {
		refused = append(refused, "files (tokenFile, client-certificate, client-key)")
	}
	if u.Username != "" || u.Password != "" {
		refused = append(refused, "basic authentication")
	}
	switch {
	case len(refused) > 0:
		return fmt.Errorf("%s are refused: give a token or client-certificate-data and client-key-data", strings.Join(refused, ", "))
	case u.Token == "" && (len(u.ClientCertificateData) == 0 || len(u.ClientKeyData) == 0):
		return errors.New("it has neither a token nor client-certificate-data and client-key-data")
	}
	return nil
}

// Target is deliverer.Target in another cluster: what it applies, prunes, reads and tears down, the embedded
// cluster.Target does as the kubeconfig's identity.
type Target struct {
	*cluster.Target
	client       kubernetes.Interface
	installation string
}

var _ deliverer.Target = (*Target)(nil)

// newTarget is the cluster cfg reaches.
func newTarget(cfg *rest.Config, installation string) (*Target, error) {
	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("making the cluster's client: %w", err)
	}
	c, err := cluster.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("reaching the cluster: %w", err)
	}
	return &Target{Target: &cluster.Target{Cluster: c}, client: client, installation: installation}, nil
}
