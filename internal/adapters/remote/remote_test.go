package remote_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	"cobranch.dev/internal/adapters/cluster"
	"cobranch.dev/internal/adapters/remote"
	"cobranch.dev/internal/services/deliverer"
)

// assetsEnv names the variable that holds the directory with the etcd and kube-apiserver binaries the tests start a
// throwaway API server from. CI sets it; without it, the tests that need one are skipped, except in CI, where they
// fail.
const assetsEnv = "COBRANCH_TEST_KUBE_ASSETS"

// server is the throwaway API server's client configuration, nil without assetsEnv, and stranger a user's it grants
// nothing.
var server, stranger *rest.Config

func TestMain(m *testing.M) {
	os.Exit(run(m))
}

// run starts the throwaway API server when assetsEnv names its binaries, and runs the tests.
func run(m *testing.M) int {
	assets := os.Getenv(assetsEnv)
	if assets == "" {
		return m.Run()
	}
	env := &envtest.Environment{BinaryAssetsDirectory: assets}
	cfg, err := env.Start()
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "starting the API server:", err) // stderr is gone; nothing to tell
		return 1
	}
	defer func() {
		if err := env.Stop(); err != nil {
			_, _ = fmt.Fprintln(os.Stderr, "stopping the API server:", err) // the run is over; nothing to tell
		}
	}()
	user, err := env.AddUser(envtest.User{Name: "stranger"}, nil)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "adding a user:", err) // stderr is gone; nothing to tell
		return 1
	}
	server, stranger = cfg, user.Config()
	return m.Run()
}

// needServer skips the test without an API server, except in CI.
func needServer(t *testing.T) {
	t.Helper()
	if server == nil {
		if os.Getenv("CI") != "" {
			t.Fatal(assetsEnv + " is not set, and CI must run the tests that need an API server")
		}
		t.Skip(assetsEnv + " is not set")
	}
}

// kubeconfigOf is the kubeconfig of cfg, one context with its server, CA and client certificate, given inline.
func kubeconfigOf(t *testing.T, cfg *rest.Config) []byte {
	t.Helper()
	out, err := clientcmd.Write(clientcmdapi.Config{
		Clusters:       map[string]*clientcmdapi.Cluster{"c": {Server: cfg.Host, CertificateAuthorityData: cfg.CAData}},
		AuthInfos:      map[string]*clientcmdapi.AuthInfo{"u": {ClientCertificateData: cfg.CertData, ClientKeyData: cfg.KeyData, Token: cfg.BearerToken}},
		Contexts:       map[string]*clientcmdapi.Context{"x": {Cluster: "c", AuthInfo: "u"}},
		CurrentContext: "x",
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// testCA is a self-signed CA's certificate, base64 as a kubeconfig holds it.
func testCA(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test"}, NotBefore: time.Now(),
		NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

// kubeconfig is a kubeconfig of one context, x, whose cluster c and user u hold the fields given, YAML indented under
// them.
func kubeconfig(clusterFields, userFields string) []byte {
	return []byte(`apiVersion: v1
kind: Config
clusters:
- name: c
  cluster:
` + clusterFields + `
contexts:
- name: x
  context: {cluster: c, user: u}
current-context: x
users:
- name: u
  user:
` + userFields + "\n")
}

func TestOpenRefuses(t *testing.T) {
	t.Parallel()
	ca := "    certificate-authority-data: " + testCA(t)
	server := "    server: https://192.0.2.1:6443\n" + ca
	token := "    token: secret"
	tests := []struct {
		name       string
		kubeconfig []byte
		wantErr    string
	}{
		{name: "not YAML", kubeconfig: []byte("{"), wantErr: "it doesn't parse"},
		{name: "two contexts", kubeconfig: []byte(strings.Replace(string(kubeconfig(server, token)), "current-context",
			"- name: second\n  context: {cluster: c, user: u}\ncurrent-context", 1)), wantErr: "it has 2 contexts"},
		{name: "a context without its cluster", kubeconfig: []byte(strings.Replace(string(kubeconfig(server, token)), "- name: c\n", "- name: d\n", 1)),
			wantErr: `names cluster "c", which it doesn't have`},
		{name: "a context without its user", kubeconfig: []byte(strings.Replace(string(kubeconfig(server, token)), "- name: u\n", "- name: v\n", 1)),
			wantErr: `names user "u", which it doesn't have`},
		{name: "plain HTTP", kubeconfig: kubeconfig("    server: http://192.0.2.1:6443", token), wantErr: "must be an https URL"},
		{name: "no server", kubeconfig: kubeconfig(ca, token), wantErr: "must be an https URL"},
		{name: "insecure-skip-tls-verify", kubeconfig: kubeconfig("    server: https://192.0.2.1\n    insecure-skip-tls-verify: true", token),
			wantErr: "insecure-skip-tls-verify is refused"},
		{name: "a CA file", kubeconfig: kubeconfig("    server: https://192.0.2.1\n    certificate-authority: /etc/ca.crt", token),
			wantErr: "certificate-authority names a file"},
		{name: "a proxy", kubeconfig: kubeconfig(server+"\n    proxy-url: http://proxy:3128", token), wantErr: "proxy-url is refused"},
		{name: "an exec plugin", kubeconfig: kubeconfig(server, "    exec: {apiVersion: client.authentication.k8s.io/v1, command: sh, interactiveMode: Never}"),
			wantErr: "exec plugins are refused"},
		{name: "an auth provider", kubeconfig: kubeconfig(server, "    auth-provider: {name: oidc}"), wantErr: "auth providers are refused"},
		{name: "a token file", kubeconfig: kubeconfig(server, "    tokenFile: /var/run/token"), wantErr: "files (tokenFile"},
		{name: "a client certificate file", kubeconfig: kubeconfig(server, "    client-certificate: /etc/tls.crt\n    client-key: /etc/tls.key"),
			wantErr: "files (tokenFile"},
		{name: "basic authentication", kubeconfig: kubeconfig(server, "    username: admin\n    password: secret"),
			wantErr: "basic authentication are refused"},
		{name: "no credentials", kubeconfig: kubeconfig(server, "    {}"), wantErr: "neither a token nor client-certificate-data"},
		{name: "a certificate without its key", kubeconfig: kubeconfig(server, "    client-certificate-data: "+testCA(t)),
			wantErr: "neither a token nor client-certificate-data"},
	}
	clusters, err := remote.New("acme", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := clusters.Open(context.Background(), tt.kubeconfig)
			if !errors.Is(err, deliverer.ErrRefused) || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("Open() = %v, want refused: %q", err, tt.wantErr)
			}
		})
	}
}

func TestOpenKeepsWhatItOpened(t *testing.T) {
	t.Parallel()
	clusters, err := remote.New("acme", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	server := "    server: https://192.0.2.1:6443\n    certificate-authority-data: " + testCA(t)
	first, err := clusters.Open(context.Background(), kubeconfig(server, "    token: one"))
	if err != nil {
		t.Fatalf("Open() = %v", err)
	}
	again, err := clusters.Open(context.Background(), kubeconfig(server, "    token: one"))
	if err != nil || again != first {
		t.Errorf("Open() of the same kubeconfig = %p, %v, want %p", again, err, first)
	}
	other, err := clusters.Open(context.Background(), kubeconfig(server, "    token: two"))
	if err != nil || other == first {
		t.Errorf("Open() of another kubeconfig = %p, %v, want a cluster of its own", other, err)
	}
}

func TestNewRefusesBadRanges(t *testing.T) {
	t.Parallel()
	if _, err := remote.New("acme", []string{"10.0.0.0"}, nil); err == nil {
		t.Error("New(an address as a denied range) = nil, want an error")
	}
	if _, err := remote.New("acme", nil, []string{"nonsense"}); err == nil {
		t.Error("New(nonsense as an allowed range) = nil, want an error")
	}
}

// TestOpenGuardsTheServer reaches the API server, on loopback, only once the operator allows its address.
func TestOpenGuardsTheServer(t *testing.T) {
	t.Parallel()
	needServer(t)
	ctx := context.Background()
	loopback := []string{"127.0.0.0/8", "::1/128"}
	for _, tt := range []struct {
		name        string
		allowed     []string
		unreachable bool
	}{
		{name: "in a range runs may not reach", unreachable: true},
		{name: "a range the operator allowed", allowed: loopback},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			clusters, err := remote.New("acme", loopback, tt.allowed)
			if err != nil {
				t.Fatal(err)
			}
			target, err := clusters.Open(ctx, kubeconfigOf(t, server))
			if err != nil {
				t.Fatalf("Open() = %v", err)
			}
			version, _, err := target.Capabilities(ctx)
			if tt.unreachable {
				if err == nil || !strings.Contains(err.Error(), remote.ErrUnreachable.Error()) {
					t.Errorf("Capabilities() = %q, %v, want the address refused", version, err)
				}
				return
			}
			if err != nil || version == "" {
				t.Errorf("Capabilities() = %q, %v", version, err)
			}
		})
	}
}

// environment is an environment of a namespace of its own.
func environment() deliverer.Environment {
	return deliverer.Environment{ID: uuid.New(), OrgID: uuid.New(), Namespace: "remote-" + uuid.NewString()[:8]}
}

// open is the API server, as cfg's user, through a kubeconfig.
func open(t *testing.T, cfg *rest.Config) deliverer.Target {
	t.Helper()
	clusters, err := remote.New("acme", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	target, err := clusters.Open(context.Background(), kubeconfigOf(t, cfg))
	if err != nil {
		t.Fatalf("Open() = %v", err)
	}
	return target
}

func TestPrepareAndRelease(t *testing.T) {
	t.Parallel()
	needServer(t)
	ctx := context.Background()
	target, env := open(t, server), environment()
	for range 2 {
		if err := target.Prepare(ctx, env); err != nil {
			t.Fatalf("Prepare() = %v", err)
		}
	}
	client, err := kubernetes.NewForConfig(server)
	if err != nil {
		t.Fatal(err)
	}
	ns, err := client.CoreV1().Namespaces().Get(ctx, env.Namespace, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if l := ns.Labels; l[cluster.EnvironmentLabel] != env.ID.String() || l[cluster.OrganizationLabel] != env.OrgID.String() ||
		l[cluster.InstallationLabel] != "acme" || l[cluster.ManagedByLabel] != "cobranch" {
		t.Errorf("the namespace's labels = %v", l)
	}
	another := env
	another.ID = uuid.New()
	if err := target.Prepare(ctx, another); !errors.Is(err, deliverer.ErrRefused) || !strings.Contains(err.Error(), "another environment's") {
		t.Errorf("Prepare(another environment in the namespace) = %v, want refused", err)
	}
	if gone, err := target.Release(ctx, another); !gone || err != nil {
		t.Errorf("Release(another environment) = %t, %v, want the namespace left, counted gone", gone, err)
	}
	// The test's API server runs no namespace controller, so the namespace stays as it is deleting.
	if gone, err := target.Release(ctx, env); gone || err != nil {
		t.Errorf("Release() = %t, %v, want the namespace deleting", gone, err)
	}
}

func TestPrepareWithoutLeaveToMakeNamespaces(t *testing.T) {
	t.Parallel()
	needServer(t)
	err := open(t, stranger).Prepare(context.Background(), environment())
	if !errors.Is(err, deliverer.ErrRefused) || !strings.Contains(err.Error(), "the kubeconfig may not make it") {
		t.Errorf("Prepare() as a stranger = %v, want refused", err)
	}
}
