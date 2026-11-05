package jobcluster

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"cobranch.dev/internal/services/deliverer"
	"cobranch.dev/internal/services/runner"
)

var (
	org = uuid.MustParse("0193a8c0-0000-7000-8000-000000000001")
	run = uuid.MustParse("0193a8c0-0000-7000-8000-0000000000aa")
	ns  = "cobranch-jobs-" + org.String()
)

// config is an installation's jobs settings, with a quota.
func config() Config {
	return Config{Prefix: "cobranch-jobs-", Installation: "cobranch-cobranch", PodSecurity: "restricted",
		Labels: map[string]string{"example.com/team": "ci"}, Home: "cobranch", Controller: "cobranch-controller", Release: "cobranch",
		RunnerRole: "cobranch-cobranch-jobs-runner", OwnerRole: "cobranch-cobranch-controller", LogsRole: "cobranch-cobranch-jobs-logs", LogsReader: "cobranch-server", EgressExcept: []string{"10.0.0.0/8", "169.254.169.254/32", "fc00::/7"},
		Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m")},
			Limits: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("1Gi")}},
		Quota: corev1.ResourceList{corev1.ResourcePods: resource.MustParse("10")},
		DNS: []networkingv1.NetworkPolicyPeer{{NamespaceSelector: &metav1.LabelSelector{},
			PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"k8s-app": "kube-dns"}}}},
		NodeSelector: map[string]string{"pool": "ci"}, Tolerations: []corev1.Toleration{{Key: "pool", Operator: corev1.TolerationOpEqual, Value: "ci"}},
		CheckoutImage: "cobranch:v1"}
}

// ownerUID is the UID of the installation's controller ClusterRole, which owns the job namespaces.
const ownerUID = types.UID("6a1e7c52-0000-4000-8000-000000000001")

// installed is a cluster where the installation's controller ClusterRole exists, with objects.
func installed(objects ...runtime.Object) *fake.Clientset {
	role := &rbacv1.ClusterRole{ObjectMeta: metav1.ObjectMeta{Name: "cobranch-cobranch-controller", UID: ownerUID}}
	return fake.NewClientset(append([]runtime.Object{role}, objects...)...)
}

// writes counts the client's actions that change something.
func writes(client *fake.Clientset) int {
	n := 0
	for _, a := range client.Actions() {
		if a.GetVerb() != "get" && a.GetVerb() != "list" && a.GetVerb() != "watch" {
			n++
		}
	}
	return n
}

func TestPrepare(t *testing.T) {
	ctx := context.Background()
	client := installed()
	c := New(client, config())
	if err := c.Prepare(ctx, org); err != nil {
		t.Fatalf("Prepare() = %v", err)
	}
	got, err := client.CoreV1().Namespaces().Get(ctx, ns, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"example.com/team": "ci", labelManagedBy: "cobranch", labelInstallation: "cobranch-cobranch",
		labelOrganization: org.String(), "pod-security.kubernetes.io/enforce": "restricted", "pod-security.kubernetes.io/enforce-version": "latest"}
	if !maps.Equal(got.Labels, want) {
		t.Errorf("namespace labels = %v, want %v", got.Labels, want)
	}
	owner := metav1.OwnerReference{APIVersion: "rbac.authorization.k8s.io/v1", Kind: "ClusterRole", Name: "cobranch-cobranch-controller", UID: ownerUID}
	if !slices.EqualFunc(got.OwnerReferences, []metav1.OwnerReference{owner}, ownerEqual) {
		t.Errorf("namespace owners = %+v, want %+v", got.OwnerReferences, owner)
	}
	checkPrepared(t, client)
	before := writes(client)
	if err := c.Prepare(ctx, org); err != nil || writes(client) != before {
		t.Errorf("Prepare() again = %v after %d writes, want none", err, writes(client)-before)
	}
	c.now = func() time.Time { return time.Now().Add(prepared) }
	cfg := config()
	cfg.Quota = nil
	c.cfg = cfg
	if err := c.Prepare(ctx, org); err != nil {
		t.Fatalf("Prepare() without a quota = %v", err)
	}
	if _, err := client.CoreV1().ResourceQuotas(ns).Get(ctx, resourceQuota, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Errorf("the quota after the installation dropped it: error = %v, want not found", err)
	}
}

// ownerEqual compares owner references, which the controller sets without blockOwnerDeletion or controller.
func ownerEqual(a, b metav1.OwnerReference) bool {
	return a.APIVersion == b.APIVersion && a.Kind == b.Kind && a.Name == b.Name && a.UID == b.UID &&
		a.BlockOwnerDeletion == nil && a.Controller == nil
}

func TestPrepareWithoutItsOwner(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()
	err := New(client, config()).Prepare(ctx, org)
	if err == nil || !strings.Contains(err.Error(), "ClusterRole cobranch-cobranch-controller") {
		t.Errorf("Prepare() = %v, want the owner named", err)
	}
	if n := writes(client); n != 0 {
		t.Errorf("Prepare() without its owner wrote %d times, want none: a namespace nothing owns would outlive the installation", n)
	}
}

// checkPrepared checks what Prepare put in the namespace: the controller's binding, the policies, the defaults and
// the quota.
func checkPrepared(t *testing.T, client *fake.Clientset) {
	t.Helper()
	ctx := context.Background()
	binding, err := client.RbacV1().RoleBindings(ns).Get(ctx, "cobranch-cobranch-jobs-runner", metav1.GetOptions{})
	if err != nil || binding.RoleRef.Name != "cobranch-cobranch-jobs-runner" || len(binding.Subjects) != 1 ||
		binding.Subjects[0].Namespace != "cobranch" || binding.Subjects[0].Name != "cobranch-controller" {
		t.Errorf("RoleBinding = %+v, %v", binding, err)
	}
	logs, err := client.RbacV1().RoleBindings(ns).Get(ctx, "cobranch-cobranch-jobs-logs", metav1.GetOptions{})
	if err != nil || logs.RoleRef.Name != "cobranch-cobranch-jobs-logs" || len(logs.Subjects) != 1 ||
		logs.Subjects[0].Namespace != "cobranch" || logs.Subjects[0].Name != "cobranch-server" {
		t.Errorf("the servers' RoleBinding = %+v, %v", logs, err)
	}
	deny, err := client.NetworkingV1().NetworkPolicies(ns).Get(ctx, policyDenyIngress, metav1.GetOptions{})
	if err != nil || !slices.Equal(deny.Spec.PolicyTypes, []networkingv1.PolicyType{"Ingress"}) || len(deny.Spec.Ingress) != 0 {
		t.Errorf("deny-ingress = %+v, %v", deny, err)
	}
	egress, err := client.NetworkingV1().NetworkPolicies(ns).Get(ctx, policyEgress, metav1.GetOptions{})
	if err != nil || len(egress.Spec.Egress) != 3 {
		t.Fatalf("egress = %+v, %v", egress, err)
	}
	if dns := egress.Spec.Egress[0]; len(dns.Ports) != 2 || len(dns.To) != 1 || dns.To[0].PodSelector.MatchLabels["k8s-app"] != "kube-dns" {
		t.Errorf("egress to DNS = %+v", dns)
	}
	blocks := egress.Spec.Egress[1].To
	if len(blocks) != 2 || !slices.Equal(blocks[0].IPBlock.Except, []string{"10.0.0.0/8", "169.254.169.254/32"}) ||
		!slices.Equal(blocks[1].IPBlock.Except, []string{"fc00::/7"}) {
		t.Errorf("egress to the internet = %+v", blocks)
	}
	servers := egress.Spec.Egress[2].To[0]
	if servers.NamespaceSelector.MatchLabels["kubernetes.io/metadata.name"] != "cobranch" ||
		servers.PodSelector.MatchLabels["app.kubernetes.io/instance"] != "cobranch" || egress.Spec.Egress[2].Ports[0].Port.IntValue() != 8080 {
		t.Errorf("egress to the servers = %+v", egress.Spec.Egress[2])
	}
	limits, err := client.CoreV1().LimitRanges(ns).Get(ctx, limitRange, metav1.GetOptions{})
	if err != nil || limits.Spec.Limits[0].DefaultRequest.Cpu().String() != "100m" || limits.Spec.Limits[0].Default.Memory().String() != "1Gi" {
		t.Errorf("LimitRange = %+v, %v", limits, err)
	}
	quota, err := client.CoreV1().ResourceQuotas(ns).Get(ctx, resourceQuota, metav1.GetOptions{})
	if err != nil || quota.Spec.Hard.Pods().String() != "10" {
		t.Errorf("ResourceQuota = %+v, %v", quota, err)
	}
}

func TestPreparePullSecrets(t *testing.T) {
	ctx := context.Background()
	registry := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "registry", Namespace: "cobranch"}, Type: corev1.SecretTypeDockerConfigJson,
		Data: map[string][]byte{corev1.DockerConfigJsonKey: []byte(`{"auths":{}}`)}}
	stale := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "old-registry", Namespace: ns, Labels: map[string]string{labelPullSecret: "true"}}}
	runSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: runs.name(run), Namespace: ns, Labels: map[string]string{labelRun: run.String()}}}
	client := installed(registry, stale, runSecret)
	cfg := config()
	cfg.ImagePullSecrets = []string{"registry"}
	if err := New(client, cfg).Prepare(ctx, org); err != nil {
		t.Fatalf("Prepare() = %v", err)
	}
	copied, err := client.CoreV1().Secrets(ns).Get(ctx, "registry", metav1.GetOptions{})
	if err != nil || copied.Type != corev1.SecretTypeDockerConfigJson || string(copied.Data[corev1.DockerConfigJsonKey]) != `{"auths":{}}` ||
		copied.Labels[labelPullSecret] != "true" || copied.Labels[labelManagedBy] != "cobranch" {
		t.Errorf("the copy = %+v, %v", copied, err)
	}
	if _, err := client.CoreV1().Secrets(ns).Get(ctx, stale.Name, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Errorf("the copy the installation no longer names: error = %v, want not found", err)
	}
	if _, err := client.CoreV1().Secrets(ns).Get(ctx, runSecret.Name, metav1.GetOptions{}); err != nil {
		t.Errorf("a run's Secret: %v, want it kept", err)
	}
	cfg.ImagePullSecrets = []string{"registry", "missing"}
	err = New(installed(registry), cfg).Prepare(ctx, org)
	if err == nil || !strings.Contains(err.Error(), "image pull secret missing in namespace cobranch") {
		t.Errorf("Prepare() naming a missing Secret = %v, want it named", err)
	}
}

func TestPrepareBindsNoServersWithoutALogsRole(t *testing.T) {
	ctx := context.Background()
	client := installed()
	cfg := config()
	cfg.LogsRole, cfg.LogsReader = "", ""
	if err := New(client, cfg).Prepare(ctx, org); err != nil {
		t.Fatal(err)
	}
	bindings, err := client.RbacV1().RoleBindings(ns).List(ctx, metav1.ListOptions{})
	if err != nil || len(bindings.Items) != 1 || bindings.Items[0].Name != "cobranch-cobranch-jobs-runner" {
		t.Errorf("RoleBindings = %+v, %v, want the controller's alone", bindings, err)
	}
}

func TestPrepareWaitsForItsBinding(t *testing.T) {
	client := installed()
	refusals := 2
	client.PrependReactor("patch", "networkpolicies", func(k8stesting.Action) (bool, runtime.Object, error) {
		if refusals == 0 {
			return false, nil, nil
		}
		refusals--
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Group: "networking.k8s.io", Resource: "networkpolicies"}, policyDenyIngress, errors.New("not bound yet"))
	})
	if err := New(client, config()).Prepare(context.Background(), org); err != nil || refusals != 0 {
		t.Errorf("Prepare() = %v with %d refusals left, want it to wait out both", err, refusals)
	}
}

func TestPrepareRefusesABadRange(t *testing.T) {
	cfg := config()
	cfg.EgressExcept = []string{"10.0.0.0/33"}
	err := New(installed(), cfg).Prepare(context.Background(), org)
	if err == nil || !strings.Contains(err.Error(), `jobs' egress excepts "10.0.0.0/33"`) {
		t.Errorf("Prepare() = %v, want the range named", err)
	}
}

// launch is a run of a script that checks its commit out, with a token and a secret.
func launch(change func(*runner.Launch)) runner.Launch {
	l := runner.Launch{Run: runner.Run{ID: run, OrgID: org, Event: json.RawMessage(`{"kind":"push"}`)}, Script: "make", Image: "bash:5",
		Checkout: true, Timeout: time.Hour, Env: map[string]string{"COBRANCH_TOKEN": "cbr_token", "API_TOKEN": "s3cret", "COBRANCH_SHA": "abc"},
		Masked: []string{"cbr_token", "s3cret"}}
	if change != nil {
		change(&l)
	}
	return l
}

func TestStart(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()
	if err := New(client, config()).Start(ctx, launch(nil)); err != nil {
		t.Fatalf("Start() = %v", err)
	}
	pod, err := client.CoreV1().Pods(ns).Get(ctx, "run-"+run.String(), metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	s := pod.Spec
	if pod.Labels[labelRun] != run.String() || s.RestartPolicy != corev1.RestartPolicyNever || *s.ActiveDeadlineSeconds != 3600 ||
		*s.AutomountServiceAccountToken || *s.EnableServiceLinks || s.NodeSelector["pool"] != "ci" || len(s.Tolerations) != 1 ||
		!*s.SecurityContext.RunAsNonRoot || *s.SecurityContext.RunAsUser != 65532 {
		t.Errorf("pod = %+v", pod)
	}
	if len(s.InitContainers) != 1 || s.InitContainers[0].Image != "cobranch:v1" || !slices.Equal(s.InitContainers[0].Args, []string{"checkout"}) {
		t.Errorf("init containers = %+v, want the checkout", s.InitContainers)
	}
	main := s.Containers[0]
	var names []string
	for _, e := range main.Env {
		names = append(names, e.Name)
		if e.ValueFrom.SecretKeyRef.Name != pod.Name || e.ValueFrom.SecretKeyRef.Key != e.Name {
			t.Errorf("variable %s = %+v, want it from the run's Secret", e.Name, e.ValueFrom)
		}
	}
	if main.Image != "bash:5" || !slices.Equal(main.Command, []string{"bash", "-eo", "pipefail", "/cobranch/script"}) ||
		main.WorkingDir != "/workspace" || !slices.Equal(names, []string{"API_TOKEN", "COBRANCH_SHA", "COBRANCH_TOKEN"}) ||
		len(main.VolumeMounts) != 2 || *main.SecurityContext.AllowPrivilegeEscalation {
		t.Errorf("main container = %+v", main)
	}
	secret, err := client.CoreV1().Secrets(ns).Get(ctx, pod.Name, metav1.GetOptions{})
	if err != nil || len(secret.OwnerReferences) != 1 || secret.OwnerReferences[0].Name != pod.Name || string(secret.Data["API_TOKEN"]) != "s3cret" ||
		string(secret.Data[keyScript]) != "make" || string(secret.Data[keyEvent]) != `{"kind":"push"}` ||
		string(secret.Data[keyMasked]) != `["cbr_token","s3cret"]` {
		t.Errorf("Secret = %+v, %v", secret, err)
	}
}

func TestStartWithoutPullSecrets(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()
	l := launch(func(l *runner.Launch) {
		l.Script, l.Pod = "", json.RawMessage(`{"imagePullSecrets":[{"name":"registry"}],"containers":[{"name":"a","image":"b"}]}`)
	})
	cfg := config()
	cfg.ImagePullSecrets = []string{"registry"}
	if err := New(client, cfg).Start(ctx, l); err != nil {
		t.Fatalf("Start() = %v", err)
	}
	pod, err := client.CoreV1().Pods(ns).Get(ctx, "run-"+run.String(), metav1.GetOptions{})
	if err != nil || len(pod.Spec.ImagePullSecrets) != 0 {
		t.Errorf("pod's image pull secrets = %+v, %v; want none: a run never pulls with the installation's", pod.Spec.ImagePullSecrets, err)
	}
}

func TestStartRefusals(t *testing.T) {
	quota := apierrors.NewForbidden(schema.GroupResource{Resource: "pods"}, "run", errors.New(`exceeded quota: cobranch-quota, requested: pods=1`))
	security := apierrors.NewForbidden(schema.GroupResource{Resource: "pods"}, "run", errors.New(`violates PodSecurity "restricted:latest": privileged`))
	tests := []struct {
		name   string
		launch runner.Launch
		create error
		want   error
		says   string
	}{
		{name: "a pod spec with a field pods have not", want: runner.ErrRefused, says: `unknown field "bogus"`,
			launch: launch(func(l *runner.Launch) { l.Pod = json.RawMessage(`{"bogus":1,"containers":[]}`) })},
		{name: "a field spelt in another case", want: runner.ErrRefused, says: `unknown field "containers[0].Env"`,
			launch: launch(func(l *runner.Launch) {
				l.Pod = json.RawMessage(`{"containers":[{"name":"a","image":"b","Env":[{"name":"K","valueFrom":{"secretKeyRef":{"name":"s","key":"k"}}}]}]}`)
			})},
		{name: "a field given twice", want: runner.ErrRefused, says: `duplicate field "containers"`,
			launch: launch(func(l *runner.Launch) {
				l.Pod = json.RawMessage(`{"containers":[{"name":"a","image":"b"}],"containers":[{"name":"c","image":"d"}]}`)
			})},
		{name: "a volume named as Cobranch's", want: runner.ErrRefused, says: "volume cobranch-run takes a name of Cobranch's own",
			launch: launch(func(l *runner.Launch) {
				l.Pod = json.RawMessage(`{"containers":[{"name":"a","image":"b"}],"volumes":[{"name":"cobranch-run","emptyDir":{}}]}`)
			})},
		{name: "no room in the quota", launch: launch(nil), create: quota, want: runner.ErrNoRoom, says: "exceeded quota"},
		{name: "a pod admission refuses", launch: launch(nil), create: security, want: runner.ErrRefused, says: "violates PodSecurity"},
		{name: "a pod left by an earlier attempt", launch: launch(nil),
			create: apierrors.NewAlreadyExists(schema.GroupResource{Resource: "pods"}, "run"), says: "from an earlier attempt"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := fake.NewClientset()
			if tt.create != nil {
				client.PrependReactor("create", "pods", func(k8stesting.Action) (bool, runtime.Object, error) { return true, nil, tt.create })
			}
			err := New(client, config()).Start(context.Background(), tt.launch)
			if err == nil || (tt.want != nil && !errors.Is(err, tt.want)) || !strings.Contains(err.Error(), tt.says) {
				t.Errorf("Start() = %v, want %v saying %q", err, tt.want, tt.says)
			}
		})
	}
}

func TestPods(t *testing.T) {
	deadline := int64(600)
	exited := func(name string, code int32, reason string) corev1.ContainerStatus {
		return corev1.ContainerStatus{Name: name, State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: code, Reason: reason}}}
	}
	tests := []struct {
		name   string
		status corev1.PodStatus
		want   runner.Pod
	}{
		{name: "running", status: corev1.PodStatus{Phase: corev1.PodRunning}, want: runner.Pod{Outcome: runner.Pending}},
		{name: "pending", status: corev1.PodStatus{Phase: corev1.PodPending}, want: runner.Pod{Outcome: runner.Pending}},
		{name: "succeeded", status: corev1.PodStatus{Phase: corev1.PodSucceeded}, want: runner.Pod{Outcome: runner.Succeeded}},
		{name: "past its deadline", status: corev1.PodStatus{Phase: corev1.PodFailed, Reason: "DeadlineExceeded"},
			want: runner.Pod{Outcome: runner.Failed, Reason: "timed out after 10m0s"}},
		{name: "a container that failed", status: corev1.PodStatus{Phase: corev1.PodFailed,
			InitContainerStatuses: []corev1.ContainerStatus{exited("cobranch-checkout", 0, "Completed")},
			ContainerStatuses:     []corev1.ContainerStatus{exited("main", 2, "Error")}},
			want: runner.Pod{Outcome: runner.Failed, Reason: "container main exited 2"}},
		{name: "a container out of memory", status: corev1.PodStatus{Phase: corev1.PodFailed,
			ContainerStatuses: []corev1.ContainerStatus{exited("main", 137, "OOMKilled")}},
			want: runner.Pod{Outcome: runner.Failed, Reason: "container main exited 137: OOMKilled"}},
		{name: "evicted", status: corev1.PodStatus{Phase: corev1.PodFailed, Reason: "Evicted", Message: "The node was low on resource: memory."},
			want: runner.Pod{Outcome: runner.Failed, Reason: "its pod failed: The node was low on resource: memory."}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "run-" + run.String(), Namespace: ns, Labels: map[string]string{labelRun: run.String()}},
				Spec: corev1.PodSpec{ActiveDeadlineSeconds: &deadline}, Status: tt.status}
			stray := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "other", Namespace: ns}}
			pods, err := New(fake.NewClientset(pod, stray), config()).Pods(context.Background(), org)
			if got := pods[run]; err != nil || len(pods) != 1 || got.Outcome != tt.want.Outcome || got.Reason != tt.want.Reason {
				t.Errorf("Pods() = %+v, %v, want %+v", pods, err, tt.want)
			}
		})
	}
}

func TestLogs(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()
	c := New(client, config())
	if logs, err := c.Logs(ctx, org, run, 100); err != nil || logs != nil {
		t.Errorf("Logs() of no pod = %v, %v, want none", logs, err)
	}
	if err := c.Start(ctx, launch(nil)); err != nil {
		t.Fatal(err)
	}
	logs, err := c.Logs(ctx, org, run, 100)
	if err != nil || len(logs) != 2 || logs["main"] == nil || logs["cobranch-checkout"] == nil {
		t.Errorf("Logs() = %q, %v, want the checkout's and main's", logs, err)
	}
	if err := c.Remove(ctx, org, run); err != nil {
		t.Fatal(err)
	}
	if _, err := client.CoreV1().Secrets(ns).Get(ctx, "run-"+run.String(), metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Errorf("the Secret after Remove(): error = %v, want not found", err)
	}
	if err := c.Remove(ctx, org, run); err != nil {
		t.Errorf("Remove() of a removed run = %v", err)
	}
}

func TestMask(t *testing.T) {
	tests := []struct {
		name   string
		log    string
		values []string
		want   string
	}{
		{name: "a token", log: "token cbr_abcd used", values: []string{"cbr_abcd"}, want: "token *** used"},
		{name: "a short value", log: "abc and abc", values: []string{"abc"}, want: "abc and abc"},
		{name: "a value inside another", log: "key=s3cret-s3cret", values: []string{"s3cret", "s3cret-s3cret"}, want: "key=***"},
		{name: "a value of several lines", log: "line one\nother\nline two", values: []string{"line one\nline two"}, want: "***\nother\n***"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := string(mask([]byte(tt.log), tt.values)); got != tt.want {
				t.Errorf("mask() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMaskLines(t *testing.T) {
	const stamp = "2026-11-05T08:00:01.123456789Z"
	tests := []struct {
		name    string
		log     string
		stamped bool
		want    string
	}{
		{name: "each line's text, never its time", log: stamp + " key 2026\n" + stamp + " ok\n", stamped: true, want: stamp + " key ***\n" + stamp + " ok\n"},
		{name: "a line without its time, whole", log: stamp + " one\n2026 cut", stamped: true, want: stamp + " one\n*** cut"},
		{name: "a log without times, whole", log: "key 2026\n", want: "key ***\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := string(maskLines([]byte(tt.log), []string{"2026"}, tt.stamped)); got != tt.want {
				t.Errorf("maskLines() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSteps(t *testing.T) {
	started, ended := metav1.NewTime(time.Date(2026, 11, 5, 8, 0, 0, 0, time.UTC)), metav1.NewTime(time.Date(2026, 11, 5, 8, 0, 6, 0, time.UTC))
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "run-" + run.String(), Namespace: ns, Labels: map[string]string{labelRun: run.String()}},
		Spec: corev1.PodSpec{InitContainers: []corev1.Container{{Name: "cobranch-checkout"}, {Name: "deps"}, {Name: "lint"}},
			Containers: []corev1.Container{{Name: "test"}, {Name: "build"}, {Name: "notify"}}},
		Status: corev1.PodStatus{Phase: corev1.PodRunning,
			InitContainerStatuses: []corev1.ContainerStatus{
				{Name: "cobranch-checkout", State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{Reason: "Completed", StartedAt: started, FinishedAt: ended}}},
				{Name: "deps", State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 137, Reason: "OOMKilled", StartedAt: started, FinishedAt: ended}}},
				{Name: "lint", State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "PodInitializing"}}}},
			ContainerStatuses: []corev1.ContainerStatus{
				{Name: "test", State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{StartedAt: started}}},
				{Name: "build", State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ImagePullBackOff"}}}}}}
	steps, err := New(fake.NewClientset(pod), config()).Steps(context.Background(), org, run)
	var got []string
	for _, s := range steps {
		got = append(got, fmt.Sprintf("%s %s %d %q %v %v", s.Name, s.State, s.ExitCode, s.Reason, s.StartedAt != nil, s.EndedAt != nil))
	}
	want := []string{`cobranch-checkout succeeded 0 "" true true`, `deps failed 137 "OOMKilled" true true`, `lint waiting 0 "" false false`,
		`test running 0 "" true false`, `build waiting 0 "ImagePullBackOff" false false`, `notify waiting 0 "" false false`}
	if err != nil || !slices.Equal(got, want) {
		t.Errorf("Steps() = %q, %v, want %q", got, err, want)
	}
	if steps, err := New(fake.NewClientset(), config()).Steps(context.Background(), org, run); err != nil || steps != nil {
		t.Errorf("Steps() of a pod that is gone = %v, %v, want none", steps, err)
	}
}

func TestNamespacesAndDrop(t *testing.T) {
	ctx := context.Background()
	other := uuid.MustParse("0193a8c0-0000-7000-8000-000000000002")
	labelled := func(name, installation string) *corev1.Namespace {
		return &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name, Labels: map[string]string{labelInstallation: installation}}}
	}
	// An environment's namespace carries the installation's label as well, whatever its name.
	environment := labelled("cobranch-jobs-0193a8c0-0000-7000-8000-000000000003", "cobranch-cobranch")
	environment.Labels[labelEnvironment] = uuid.NewString()
	client := fake.NewClientset(labelled(ns, "cobranch-cobranch"), labelled("cobranch-jobs-"+other.String(), "cobranch-cobranch"),
		labelled("elsewhere-jobs-"+other.String(), "another"), labelled("cobranch-jobs-x", "cobranch-cobranch"), environment)
	c := New(client, config())
	got, err := c.Namespaces(ctx)
	slices.SortFunc(got, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	if err != nil || !slices.Equal(got, []uuid.UUID{org, other}) {
		t.Errorf("Namespaces() = %v, %v, want both organizations, and no environment's", got, err)
	}
	if err := c.Drop(ctx, org); err != nil {
		t.Fatal(err)
	}
	if err := c.Drop(ctx, org); err != nil {
		t.Errorf("Drop() of a dropped namespace = %v", err)
	}
	if got, err := c.Namespaces(ctx); err != nil || !slices.Equal(got, []uuid.UUID{other}) {
		t.Errorf("Namespaces() after Drop() = %v, %v", got, err)
	}
}

// TestRenders starts a delivery's render pod beside a run's, and lists, reads and removes each apart from the other:
// the runs never see a render pod, nor the renders a run's.
func TestRenders(t *testing.T) {
	ctx := context.Background()
	delivery := uuid.MustParse("0193a8c0-0000-7000-8000-0000000000dd")
	client := fake.NewClientset()
	cfg := config()
	cfg.RenderResources = corev1.ResourceRequirements{Limits: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("2Gi")}}
	c := New(client, cfg)
	if err := c.Start(ctx, launch(nil)); err != nil {
		t.Fatalf("Start() = %v", err)
	}
	render := deliverer.Render{Delivery: deliverer.Delivery{ID: delivery, OrgID: org}, Args: []string{"--target", "preview"},
		Env: map[string]string{"COBRANCH_TOKEN": "cbd_token", "COBRANCH_UPLOAD_URL": "http://rest/v1/…"}, Masked: []string{"cbd_token"},
		Timeout: 10 * time.Minute}
	if err := c.StartRender(ctx, render); err != nil {
		t.Fatalf("StartRender() = %v", err)
	}
	pod, err := client.CoreV1().Pods(ns).Get(ctx, "render-"+delivery.String(), metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	s := pod.Spec
	main := s.Containers[0]
	if pod.Labels[labelDelivery] != delivery.String() || pod.Labels[labelRun] != "" || *s.ActiveDeadlineSeconds != 600 ||
		len(s.InitContainers) != 1 || s.InitContainers[0].Name != "cobranch-checkout" || main.Name != "render" || main.Image != "cobranch:v1" ||
		!slices.Equal(main.Args, []string{"render", "--target", "preview"}) || main.Resources.Limits.Memory().String() != "2Gi" ||
		main.WorkingDir != "/workspace" || len(main.Env) != 2 || main.Env[0].ValueFrom.SecretKeyRef.Name != pod.Name {
		t.Errorf("render pod = %+v", pod)
	}
	secret, err := client.CoreV1().Secrets(ns).Get(ctx, pod.Name, metav1.GetOptions{})
	if err != nil || string(secret.Data["COBRANCH_TOKEN"]) != "cbd_token" || string(secret.Data[keyMasked]) != `["cbd_token"]` {
		t.Errorf("Secret = %+v, %v", secret, err)
	}
	runs, err := c.Pods(ctx, org)
	if _, ok := runs[run]; err != nil || len(runs) != 1 || !ok {
		t.Errorf("Pods() = %v, %v, want the run's alone", runs, err)
	}
	renders, err := c.Renders(ctx, org)
	if _, ok := renders[delivery]; err != nil || len(renders) != 1 || !ok {
		t.Errorf("Renders() = %v, %v, want the render's alone", renders, err)
	}
	if err := c.RemoveRender(ctx, org, delivery); err != nil {
		t.Fatalf("RemoveRender() = %v", err)
	}
	if renders, err := c.Renders(ctx, org); err != nil || len(renders) != 0 {
		t.Errorf("Renders() after RemoveRender = %v, %v", renders, err)
	}
	if runs, err := c.Pods(ctx, org); err != nil || len(runs) != 1 {
		t.Errorf("Pods() after RemoveRender = %v, %v, want the run's still", runs, err)
	}
}
