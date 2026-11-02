package incluster_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	"cobranch.dev/internal/adapters/incluster"
	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/deliverer"
)

// assetsEnv names the variable that holds the directory with the etcd and kube-apiserver binaries the tests start a
// throwaway API server from. CI sets it; without it, the tests are skipped, except in CI, where they fail.
const assetsEnv = "COBRANCH_TEST_KUBE_ASSETS"

// The installation the tests deliver for: its namespace, its controller's and delivery account's ServiceAccounts, and
// its roles, as the chart names them.
const (
	home         = "cobranch"
	controller   = "cobranch-controller"
	account      = "cobranch-delivery"
	deliveryRole = "cobranch-cobranch-delivery"
	fenceRole    = "cobranch-cobranch-environments"
)

// admin is the throwaway API server's administrator, and asController its configuration as the controller, granted
// what the chart grants it; both nil without assetsEnv.
var admin, asController *rest.Config

func TestMain(m *testing.M) {
	os.Exit(run(m))
}

// run starts the throwaway API server when assetsEnv names its binaries, grants the controller what the chart does,
// and runs the tests.
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
	user, err := env.AddUser(envtest.User{Name: "system:serviceaccount:" + home + ":" + controller}, nil)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "adding the controller:", err) // stderr is gone; nothing to tell
		return 1
	}
	if err := grant(context.Background(), cfg); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "granting the controller its roles:", err) // stderr is gone; nothing to tell
		return 1
	}
	admin, asController = cfg, user.Config()
	return m.Run()
}

// grant makes the installation's namespace and the chart's roles: the controller's, cluster-wide, which makes
// namespaces and binds the delivery and fence roles; its impersonation of the delivery account; the delivery role, a
// namespace's admin's rules but its guards; and the fence role.
func grant(ctx context.Context, cfg *rest.Config) error {
	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return fmt.Errorf("making a client: %w", err)
	}
	all := []string{"get", "list", "watch", "create", "update", "patch", "delete"}
	roles := []*rbacv1.ClusterRole{
		{ObjectMeta: metav1.ObjectMeta{Name: "controller"}, Rules: []rbacv1.PolicyRule{
			{APIGroups: []string{""}, Resources: []string{"namespaces"}, Verbs: all},
			{APIGroups: []string{"rbac.authorization.k8s.io"}, Resources: []string{"rolebindings"}, Verbs: all},
			{APIGroups: []string{"rbac.authorization.k8s.io"}, Resources: []string{"clusterroles"}, Verbs: []string{"get", "bind"},
				ResourceNames: []string{deliveryRole, fenceRole}},
		}},
		{ObjectMeta: metav1.ObjectMeta{Name: deliveryRole}, Rules: []rbacv1.PolicyRule{
			{APIGroups: []string{""}, Resources: []string{"configmaps", "services", "serviceaccounts"}, Verbs: all},
			{APIGroups: []string{"apps"}, Resources: []string{"deployments"}, Verbs: all},
			{APIGroups: []string{"networking.k8s.io"}, Resources: []string{"networkpolicies"}, Verbs: []string{"get", "list", "watch"}},
		}},
		{ObjectMeta: metav1.ObjectMeta{Name: fenceRole}, Rules: []rbacv1.PolicyRule{
			{APIGroups: []string{""}, Resources: []string{"limitranges", "resourcequotas"}, Verbs: all},
			{APIGroups: []string{"networking.k8s.io"}, Resources: []string{"networkpolicies"}, Verbs: all},
		}},
	}
	for _, r := range roles {
		if _, err := client.RbacV1().ClusterRoles().Create(ctx, r, metav1.CreateOptions{}); err != nil {
			return fmt.Errorf("creating ClusterRole %s: %w", r.Name, err)
		}
	}
	subject := []rbacv1.Subject{{Kind: "ServiceAccount", Namespace: home, Name: controller}}
	if _, err := client.RbacV1().ClusterRoleBindings().Create(ctx, &rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: "controller"},
		RoleRef: rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "ClusterRole", Name: "controller"}, Subjects: subject}, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("binding the controller: %w", err)
	}
	if _, err := client.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: home}}, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("creating namespace %s: %w", home, err)
	}
	if _, err := client.RbacV1().Roles(home).Create(ctx, &rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Name: "impersonate"}, Rules: []rbacv1.PolicyRule{
		{APIGroups: []string{""}, Resources: []string{"serviceaccounts"}, Verbs: []string{"impersonate"}, ResourceNames: []string{account}},
	}}, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("creating the impersonation role: %w", err)
	}
	if _, err := client.RbacV1().RoleBindings(home).Create(ctx, &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: "impersonate"},
		RoleRef: rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "Role", Name: "impersonate"}, Subjects: subject}, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("binding the impersonation role: %w", err)
	}
	return nil
}

// fixture is the throwaway API server, the Target as the controller, an administrator's client, and an environment
// of the test's own.
type fixture struct {
	target *incluster.Target
	admin  kubernetes.Interface
	env    deliverer.Environment
}

// newFixture is a fixture whose Target has cfg's settings over the tests' installation, or skips the test without an
// API server, except in CI.
func newFixture(t *testing.T, cfg incluster.Config) *fixture {
	t.Helper()
	if admin == nil {
		if os.Getenv("CI") != "" {
			t.Fatal(assetsEnv + " is not set, and CI must run the tests that need an API server")
		}
		t.Skip(assetsEnv + " is not set")
	}
	cfg.Installation, cfg.PodSecurity, cfg.Home, cfg.Controller, cfg.Account = "cobranch-cobranch", "restricted", home, controller, account
	cfg.DeliveryRole, cfg.FenceRole = deliveryRole, fenceRole
	target, err := incluster.New(asController, cfg)
	if err != nil {
		t.Fatal(err)
	}
	client, err := kubernetes.NewForConfig(admin)
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{target: target, admin: client, env: deliverer.Environment{ID: uuid.New(), OrgID: uuid.New(), RepoID: uuid.New(),
		Namespace: "website-" + uuid.NewString()[:8]}}
}

// TestPrepare makes an environment's namespace labelled as the installation's and the environment's, binds the
// controller and the delivery account there, and fences it; again, it changes nothing.
func TestPrepare(t *testing.T) {
	f := newFixture(t, incluster.Config{EgressExcept: []string{"10.0.0.0/8", "fc00::/7"},
		IngressFrom: []metav1.LabelSelector{{MatchLabels: map[string]string{"kubernetes.io/metadata.name": "gateway"}}},
		Labels:      map[string]string{"team": "web"},
		Quota:       corev1.ResourceList{corev1.ResourcePods: resource.MustParse("10")}})
	ctx := context.Background()
	for range 2 {
		if err := f.target.Prepare(ctx, f.env); err != nil {
			t.Fatalf("Prepare() = %v", err)
		}
	}
	ns, err := f.admin.CoreV1().Namespaces().Get(ctx, f.env.Namespace, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]string{"cobranch.dev/installation": "cobranch-cobranch", "cobranch.dev/environment": f.env.ID.String(),
		"cobranch.dev/organization": f.env.OrgID.String(), "pod-security.kubernetes.io/enforce": "restricted", "team": "web"} {
		if ns.Labels[k] != v {
			t.Errorf("namespace label %s = %q, want %q", k, ns.Labels[k], v)
		}
	}
	f.checkBindings(t)
	f.checkFence(t)
}

// checkBindings checks that the environment's namespace binds the delivery account to the delivery role, and the
// controller to the fence role, and nothing else.
func (f *fixture) checkBindings(t *testing.T) {
	t.Helper()
	bindings, err := f.admin.RbacV1().RoleBindings(f.env.Namespace).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var bound []string
	for _, b := range bindings.Items {
		bound = append(bound, b.RoleRef.Name+" to "+b.Subjects[0].Name)
	}
	slices.Sort(bound)
	if want := []string{deliveryRole + " to " + account, fenceRole + " to " + controller}; !slices.Equal(bound, want) {
		t.Errorf("bindings = %v, want %v", bound, want)
	}
}

// checkFence checks the environment's namespace's NetworkPolicies, which admit its own pods and the gateway's
// namespace and let out all but 10.0.0.0/8, and its quota of 10 pods.
func (f *fixture) checkFence(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	policies, err := f.admin.NetworkingV1().NetworkPolicies(f.env.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(policies.Items) != 2 {
		t.Fatalf("NetworkPolicies = %d, want 2", len(policies.Items))
	}
	for _, p := range policies.Items {
		switch p.Name {
		case "cobranch-ingress":
			if from := p.Spec.Ingress[0].From; len(from) != 2 || from[1].NamespaceSelector == nil {
				t.Errorf("ingress admits %+v, want the namespace's own pods and the gateway's namespace", from)
			}
		case "cobranch-egress":
			if except := p.Spec.Egress[2].To[0].IPBlock.Except; !slices.Equal(except, []string{"10.0.0.0/8"}) {
				t.Errorf("egress excepts %v, want 10.0.0.0/8", except)
			}
		}
	}
	quota, err := f.admin.CoreV1().ResourceQuotas(f.env.Namespace).Get(ctx, "cobranch-quota", metav1.GetOptions{})
	if err != nil || quota.Spec.Hard.Pods().Value() != 10 {
		t.Errorf("quota = %v, %v, want 10 pods", quota, err)
	}
}

// TestPrepareRefusesANamespaceNotItsOwn refuses a namespace that exists without the environment's labels.
func TestPrepareRefusesANamespaceNotItsOwn(t *testing.T) {
	tests := []struct {
		name   string
		labels func(env deliverer.Environment) map[string]string
	}{
		{name: "no labels", labels: func(deliverer.Environment) map[string]string { return nil }},
		{name: "another environment's", labels: func(deliverer.Environment) map[string]string {
			return map[string]string{"cobranch.dev/installation": "cobranch-cobranch", "cobranch.dev/environment": uuid.NewString()}
		}},
		{name: "another installation's", labels: func(env deliverer.Environment) map[string]string {
			return map[string]string{"cobranch.dev/installation": "staging-cobranch", "cobranch.dev/environment": env.ID.String()}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t, incluster.Config{})
			ctx := context.Background()
			ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: f.env.Namespace, Labels: tt.labels(f.env)}}
			if _, err := f.admin.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{}); err != nil {
				t.Fatal(err)
			}
			err := f.target.Prepare(ctx, f.env)
			if !errors.Is(err, deliverer.ErrRefused) || !strings.Contains(err.Error(), f.env.Namespace+" exists, and isn't this environment's") {
				t.Fatalf("Prepare() = %v, want the namespace refused", err)
			}
			bindings, err := f.admin.RbacV1().RoleBindings(f.env.Namespace).List(ctx, metav1.ListOptions{})
			if err != nil || len(bindings.Items) > 0 {
				t.Errorf("bindings = %v, %v, want none", bindings, err)
			}
		})
	}
}

// TestPrepareRefusesAJobNamespacesName refuses an environment's namespace named as the installation's job namespaces
// are, before it makes anything.
func TestPrepareRefusesAJobNamespacesName(t *testing.T) {
	f := newFixture(t, incluster.Config{JobsPrefix: "cobranch-jobs-"})
	ctx := context.Background()
	f.env.Namespace = "cobranch-jobs-" + uuid.NewString()
	err := f.target.Prepare(ctx, f.env)
	if !errors.Is(err, deliverer.ErrRefused) || !strings.Contains(err.Error(), "names this installation's job namespaces") {
		t.Fatalf("Prepare() = %v, want the namespace refused", err)
	}
	if _, err := f.admin.CoreV1().Namespaces().Get(ctx, f.env.Namespace, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Errorf("Get(namespace) = %v, want it never made", err)
	}
}

// TestApplyPruneAndHealth applies a render as the delivery account, reads its health, and prunes what the next
// render stops holding.
func TestApplyPruneAndHealth(t *testing.T) {
	f := newFixture(t, incluster.Config{})
	ctx := context.Background()
	if err := f.target.Prepare(ctx, f.env); err != nil {
		t.Fatal(err)
	}
	first, err := f.target.Apply(ctx, f.env, []byte(configMaps("site", "settings")))
	if err != nil {
		t.Fatalf("Apply() = %v", err)
	}
	if got := names(first); !slices.Equal(got, []string{"ConfigMap/site", "ConfigMap/settings"}) {
		t.Fatalf("applied %v", got)
	}
	read, health, err := f.target.Health(ctx, first)
	if err != nil || health != code.Healthy || read[0].Health != code.Healthy || read[0].UID == "" {
		t.Fatalf("Health() = %+v, %s, %v, want both healthy", read, health, err)
	}
	second, err := f.target.Apply(ctx, f.env, []byte(configMaps("site")))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.target.Prune(ctx, f.env, first, second); err != nil {
		t.Fatalf("Prune() = %v", err)
	}
	if _, err := f.admin.CoreV1().ConfigMaps(f.env.Namespace).Get(ctx, "settings", metav1.GetOptions{}); err == nil {
		t.Error("ConfigMap settings is still there")
	}
	site, err := f.admin.CoreV1().ConfigMaps(f.env.Namespace).Get(ctx, "site", metav1.GetOptions{})
	if err != nil || site.Labels["cobranch.dev/environment"] != f.env.ID.String() {
		t.Errorf("ConfigMap site = %v, %v, want it labelled as the environment's", site, err)
	}
	if _, health, _ := f.target.Health(ctx, first); health != code.Missing {
		t.Errorf("Health() of what was pruned = %s, want missing", health)
	}
}

// TestApplyRefuses refuses, and applies none of, a render that holds what the environment's namespace can't take.
func TestApplyRefuses(t *testing.T) {
	tests := []struct {
		name, render, want string
	}{
		{name: "a NetworkPolicy", render: "apiVersion: networking.k8s.io/v1\nkind: NetworkPolicy\nmetadata:\n  name: open\nspec:\n  podSelector: {}\n",
			want: "NetworkPolicy/open: NetworkPolicies fence the environment's namespace"},
		{name: "a ResourceQuota", render: "apiVersion: v1\nkind: ResourceQuota\nmetadata:\n  name: more\n", want: "ResourceQuota/more: ResourceQuotas"},
		{name: "a cluster-scoped object", render: "apiVersion: rbac.authorization.k8s.io/v1\nkind: ClusterRole\nmetadata:\n  name: wide\n",
			want: "ClusterRole/wide: it is cluster-scoped"},
		{name: "another namespace's object", render: "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: x\n  namespace: kube-system\n",
			want: "names a namespace other than the environment's"},
		{name: "a stream that isn't YAML", render: "apiVersion: v1\nkind: [\n", want: "decoding the render's object 2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t, incluster.Config{})
			ctx := context.Background()
			if err := f.target.Prepare(ctx, f.env); err != nil {
				t.Fatal(err)
			}
			applied, err := f.target.Apply(ctx, f.env, []byte(configMaps("site")+"---\n"+tt.render))
			if !errors.Is(err, deliverer.ErrRefused) || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Apply() = %v, want refused: %s", err, tt.want)
			}
			if len(applied) > 0 {
				t.Errorf("applied %v, want nothing", names(applied))
			}
		})
	}
}

// TestCapabilities reads what the cluster serves, as the delivery account.
func TestCapabilities(t *testing.T) {
	f := newFixture(t, incluster.Config{})
	version, apis, err := f.target.Capabilities(context.Background())
	if err != nil || !strings.HasPrefix(version, "v1.") || !slices.Contains(apis, "v1/ConfigMap") || !slices.Contains(apis, "apps/v1/Deployment") {
		t.Errorf("Capabilities() = %q, %d APIs, %v, want the server's version and its ConfigMaps and Deployments", version, len(apis), err)
	}
}

// configMaps is a render of a ConfigMap of each name.
func configMaps(names ...string) string {
	docs := make([]string, 0, len(names))
	for _, n := range names {
		docs = append(docs, "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: "+n+"\ndata:\n  greeting: hello\n")
	}
	return strings.Join(docs, "---\n")
}

// names is objects' kind/name, in order.
func names(objects []deliverer.Object) []string {
	out := make([]string, 0, len(objects))
	for _, o := range objects {
		out = append(out, o.String())
	}
	return out
}

// TestRelease deletes the environment's namespace, as the controller, and leaves another's.
func TestRelease(t *testing.T) {
	f := newFixture(t, incluster.Config{})
	ctx := context.Background()
	if err := f.target.Prepare(ctx, f.env); err != nil {
		t.Fatal(err)
	}
	another := f.env
	another.ID = uuid.New()
	if gone, err := f.target.Release(ctx, another); !gone || err != nil {
		t.Errorf("Release(another environment) = %t, %v, want the namespace left, counted gone", gone, err)
	}
	// The test's API server runs no namespace controller, so the namespace stays as it is deleting.
	for range 2 {
		if gone, err := f.target.Release(ctx, f.env); gone || err != nil {
			t.Errorf("Release() = %t, %v, want the namespace deleting", gone, err)
		}
	}
	ns, err := f.admin.CoreV1().Namespaces().Get(ctx, f.env.Namespace, metav1.GetOptions{})
	if err != nil || ns.DeletionTimestamp == nil {
		t.Errorf("the namespace = %+v, %v, want it deleting", ns, err)
	}
}
