package jobcluster

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"cobranch.dev/internal/services/runner"
	"cobranch.dev/internal/services/taskrunner"
)

var (
	taskID    = uuid.MustParse("0193a8c0-0000-7000-8000-0000000000cc")
	otherTask = uuid.MustParse("0193a8c0-0000-7000-8000-0000000000bb")
)

// taskConfig is config with an agents' image and the harness's resources.
func taskConfig() Config {
	cfg := config()
	cfg.AgentImage = "agents:v1"
	cfg.AgentResources = corev1.ResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("500m")}}
	cfg.ProviderURLs = map[string]string{"anthropic": "https://gateway.example.com/anthropic", "openai": "https://gateway.example.com/openai"}
	cfg.ImagePullSecrets = []string{"registry", "mirror"}
	return cfg
}

func taskLaunch(pod int) taskrunner.Launch {
	return taskrunner.Launch{Task: taskrunner.Task{ID: taskID, OrgID: org, Provider: "anthropic", Model: "test-model"}, Pod: pod,
		Token: "cba_token", Key: []byte("provider-key"), Timeout: 24 * time.Hour,
		Env: map[string]string{"COBRANCH_MODEL": "test-model", "COBRANCH_REST_URL": "http://cobranch-rest.cobranch:8080", "API_TOKEN": "s3cret"}}
}

func TestStartTask(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()
	if err := New(client, taskConfig()).StartTask(ctx, taskLaunch(2)); err != nil {
		t.Fatalf("StartTask() = %v", err)
	}
	pod, err := client.CoreV1().Pods(ns).Get(ctx, "task-"+taskID.String()+"-2", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	s := pod.Spec
	if pod.Labels[labelTask] != taskID.String() || pod.Labels[labelTaskPod] != "2" || pod.Labels[labelRun] != "" ||
		s.RestartPolicy != corev1.RestartPolicyNever || *s.ActiveDeadlineSeconds != 86400 || *s.AutomountServiceAccountToken ||
		*s.EnableServiceLinks || s.NodeSelector["pool"] != "ci" || !*s.SecurityContext.RunAsNonRoot ||
		!slices.Equal(s.ImagePullSecrets, []corev1.LocalObjectReference{{Name: "registry"}, {Name: "mirror"}}) {
		t.Errorf("pod = %+v", pod)
	}
	checkTaskContainers(t, s)
	checkKeyOnlyInTheProxy(t, s)
	secret, err := client.CoreV1().Secrets(ns).Get(ctx, pod.Name, metav1.GetOptions{})
	if err != nil || len(secret.OwnerReferences) != 1 || secret.OwnerReferences[0].Name != pod.Name || secret.Labels[labelTask] != taskID.String() ||
		string(secret.Data[keyToken]) != "cba_token" || string(secret.Data[keyKey]) != "provider-key" || string(secret.Data["API_TOKEN"]) != "s3cret" {
		t.Errorf("Secret = %+v, %v", secret, err)
	}
}

// checkTaskContainers checks a task's pod's containers: the copy of the binary, the model proxy beside the harness, and
// the harness in the agents' image, with Cobranch's variables and the task's from its Secret.
func checkTaskContainers(t *testing.T, s corev1.PodSpec) {
	t.Helper()
	if len(s.InitContainers) != 2 || len(s.Containers) != 1 {
		t.Fatalf("containers = %+v, %+v, want the copy and the proxy before the harness", s.InitContainers, s.Containers)
	}
	copyBin, proxy, harness := s.InitContainers[0], s.InitContainers[1], s.Containers[0]
	if copyBin.Image != "cobranch:v1" || !slices.Equal(copyBin.Command, []string{"cp", "/usr/local/bin/cobranch", "/cobranch/bin/cobranch"}) ||
		copyBin.RestartPolicy != nil {
		t.Errorf("copy = %+v", copyBin)
	}
	if proxy.Image != "cobranch:v1" || !slices.Equal(proxy.Args, []string{"model-proxy"}) || proxy.RestartPolicy == nil ||
		*proxy.RestartPolicy != corev1.ContainerRestartPolicyAlways || !hasEnv(proxy.Env, "COBRANCH_MODEL_PROVIDER", "anthropic") ||
		!hasEnv(proxy.Env, "COBRANCH_HTTP_ADDR", "127.0.0.1:8090") || !hasEnv(proxy.Env, "COBRANCH_MODEL_URL", "https://gateway.example.com/anthropic") ||
		!hasEnv(proxy.Env, "COBRANCH_MODEL", "test-model") || !hasEnv(proxy.Env, "COBRANCH_REST_URL", "http://cobranch-rest.cobranch:8080") ||
		!hasEnv(proxy.Env, "COBRANCH_TOKEN_FILE", "/cobranch/token/token") ||
		!slices.ContainsFunc(proxy.VolumeMounts, func(m corev1.VolumeMount) bool {
			return m.Name == volumeToken && m.MountPath == "/cobranch/token" && m.ReadOnly
		}) {
		t.Errorf("model proxy = %+v, want it to report each turn's spend as the task's token", proxy)
	}
	var fromSecret []string
	for _, e := range harness.Env {
		if e.ValueFrom != nil {
			fromSecret = append(fromSecret, e.Name)
		}
	}
	if harness.Image != "agents:v1" || !slices.Equal(harness.Command, []string{"/cobranch/bin/cobranch", "agent"}) || harness.WorkingDir != "/workspace" ||
		!hasEnv(harness.Env, "COBRANCH_MODEL_URL", "http://127.0.0.1:8090") || !hasEnv(harness.Env, "COBRANCH_TOKEN_FILE", "/cobranch/token/token") ||
		!hasEnv(harness.Env, "HOME", "/home/agent") || !slices.Equal(fromSecret, []string{"API_TOKEN", "COBRANCH_MODEL", "COBRANCH_REST_URL"}) ||
		!harness.Resources.Requests.Cpu().Equal(resource.MustParse("500m")) {
		t.Errorf("harness = %+v", harness)
	}
	for _, c := range append(slices.Clone(s.InitContainers), s.Containers...) {
		if c.SecurityContext == nil || *c.SecurityContext.AllowPrivilegeEscalation || !slices.Equal(c.SecurityContext.Capabilities.Drop, []corev1.Capability{"ALL"}) {
			t.Errorf("container %s's security context = %+v", c.Name, c.SecurityContext)
		}
	}
}

// checkKeyOnlyInTheProxy checks that the provider's key reaches the model proxy alone: only its volume holds the key,
// only the proxy mounts that volume, and no variable reads it.
func checkKeyOnlyInTheProxy(t *testing.T, s corev1.PodSpec) {
	t.Helper()
	for _, v := range s.Volumes {
		if v.Secret != nil && slices.ContainsFunc(v.Secret.Items, func(i corev1.KeyToPath) bool { return i.Key == keyKey }) != (v.Name == volumeKey) {
			t.Errorf("volume %s holds %+v", v.Name, v.Secret.Items)
		}
	}
	for _, c := range append(slices.Clone(s.InitContainers), s.Containers...) {
		if readsKey(c) != (c.Name == "model-proxy") {
			t.Errorf("container %s reads the key: %t", c.Name, readsKey(c))
		}
	}
}

// readsKey reports whether the container mounts the key's volume, or has a variable read the key.
func readsKey(c corev1.Container) bool {
	return slices.ContainsFunc(c.VolumeMounts, func(m corev1.VolumeMount) bool { return m.Name == volumeKey }) ||
		slices.ContainsFunc(c.Env, func(e corev1.EnvVar) bool {
			return e.ValueFrom != nil && e.ValueFrom.SecretKeyRef != nil && e.ValueFrom.SecretKeyRef.Key == keyKey
		})
}

func hasEnv(env []corev1.EnvVar, name, value string) bool {
	return slices.ContainsFunc(env, func(e corev1.EnvVar) bool { return e.Name == name && e.Value == value })
}

// taskPod is pod numbered n of task, as the controller labels it, with phase and its Secret.
func taskPod(task uuid.UUID, n int, phase corev1.PodPhase) []runtime.Object {
	name := taskPodName(task, n)
	labels := map[string]string{labelTask: task.String(), labelTaskPod: strconv.Itoa(n)}
	return []runtime.Object{
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, Labels: labels}, Status: corev1.PodStatus{Phase: phase}},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, Labels: labels}, Data: map[string][]byte{keyToken: []byte("cba_old"), keyKey: []byte("k")}},
	}
}

func TestStartTaskRefusals(t *testing.T) {
	quota := apierrors.NewForbidden(schema.GroupResource{Resource: "pods"}, "task", errors.New(`exceeded quota: cobranch-quota, requested: pods=1`))
	security := apierrors.NewForbidden(schema.GroupResource{Resource: "pods"}, "task", errors.New(`violates PodSecurity "restricted:latest": runAsNonRoot`))
	tests := []struct {
		name     string
		existing []runtime.Object
		create   error
		want     error
		says     string
		wantLeft int
	}{
		{name: "no room in the quota", create: quota, want: runner.ErrNoRoom, says: "exceeded quota"},
		{name: "a pod admission refuses", create: security, want: runner.ErrRefused, says: "violates PodSecurity"},
		{name: "a namespace that isn't there", create: apierrors.NewNotFound(schema.GroupResource{Resource: "namespaces"}, ns), says: "which isn't there"},
		{name: "a pod left by an earlier attempt", existing: append(taskPod(taskID, 1, corev1.PodRunning), taskPod(otherTask, 1, corev1.PodRunning)...),
			says: "from an earlier attempt", wantLeft: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := fake.NewClientset(tt.existing...)
			if tt.create != nil {
				client.PrependReactor("create", "pods", func(k8stesting.Action) (bool, runtime.Object, error) { return true, nil, tt.create })
			}
			err := New(client, taskConfig()).StartTask(context.Background(), taskLaunch(1))
			if err == nil || (tt.want != nil && !errors.Is(err, tt.want)) || !strings.Contains(err.Error(), tt.says) {
				t.Errorf("StartTask() = %v, want %v saying %q", err, tt.want, tt.says)
			}
			pods, _ := client.CoreV1().Pods(ns).List(context.Background(), metav1.ListOptions{})
			secrets, _ := client.CoreV1().Secrets(ns).List(context.Background(), metav1.ListOptions{})
			if len(pods.Items) != tt.wantLeft || len(secrets.Items) != tt.wantLeft {
				t.Errorf("left %d pods and %d Secrets, want %d", len(pods.Items), len(secrets.Items), tt.wantLeft)
			}
		})
	}
}

func TestTaskPods(t *testing.T) {
	objects := slices.Concat(taskPod(taskID, 1, corev1.PodFailed), taskPod(taskID, 2, corev1.PodRunning), taskPod(otherTask, 1, corev1.PodSucceeded))
	runPod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "run-" + run.String(), Namespace: ns, Labels: map[string]string{labelRun: run.String()}}}
	stray := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "stray", Namespace: ns, Labels: map[string]string{labelTask: "not-a-task"}}}
	pods, err := New(fake.NewClientset(append(objects, runPod, stray)...), taskConfig()).TaskPods(context.Background(), org)
	want := map[uuid.UUID]taskrunner.Pod{taskID: {Number: 2, Pod: runner.Pod{Outcome: runner.Pending}},
		otherTask: {Number: 1, Pod: runner.Pod{Outcome: runner.Succeeded}}}
	if err != nil || len(pods) != len(want) || pods[taskID] != want[taskID] || pods[otherTask] != want[otherTask] {
		t.Errorf("TaskPods() = %+v, %v, want %+v", pods, err, want)
	}
}

func TestRenew(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset(taskPod(taskID, 1, corev1.PodRunning)...)
	c := New(client, taskConfig())
	if err := c.Renew(ctx, org, taskID, 1, "cba_new"); err != nil {
		t.Fatalf("Renew() = %v", err)
	}
	secret, err := client.CoreV1().Secrets(ns).Get(ctx, "task-"+taskID.String()+"-1", metav1.GetOptions{})
	if err != nil || string(secret.Data[keyToken]) != "cba_new" || string(secret.Data[keyKey]) != "k" {
		t.Errorf("Secret = %+v, %v, want the new token beside the key", secret, err)
	}
	if err := c.Renew(ctx, org, taskID, 2, "cba_new"); !apierrors.IsNotFound(errors.Unwrap(err)) {
		t.Errorf("Renew() of a pod that isn't there = %v, want not found", err)
	}
}

func TestRemoveTask(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset(slices.Concat(taskPod(taskID, 1, corev1.PodFailed), taskPod(taskID, 2, corev1.PodRunning), taskPod(otherTask, 1, corev1.PodRunning))...)
	if err := New(client, taskConfig()).RemoveTask(ctx, org, taskID); err != nil {
		t.Fatalf("RemoveTask() = %v", err)
	}
	pods, _ := client.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{})
	secrets, _ := client.CoreV1().Secrets(ns).List(ctx, metav1.ListOptions{})
	if len(pods.Items) != 1 || pods.Items[0].Labels[labelTask] != otherTask.String() || len(secrets.Items) != 1 ||
		secrets.Items[0].Labels[labelTask] != otherTask.String() {
		t.Errorf("left pods %+v and Secrets %+v, want the other task's alone", pods.Items, secrets.Items)
	}
}
