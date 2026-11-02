package jobcluster

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strconv"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"cobranch.dev/internal/services/taskrunner"
)

// The labels of an agent task's pod, beside labelManagedBy; never labelRun, so jobs' runs never see them.
const (
	// labelTask names the task a pod works, by its ID.
	labelTask = "cobranch.dev/task"
	// labelTaskPod numbers the task's pods, from 1.
	labelTaskPod = "cobranch.dev/task-pod"
)

// Where an agent task's pod keeps what the harness and its model proxy read.
const (
	// binDir holds the cobranch binary the harness runs, copied from Cobranch's image, so that the agents' image may be
	// any image with the tools they need.
	binDir = "/cobranch/bin"
	// tokenDir holds the task token, which the controller renews in the pod's Secret.
	tokenDir = "/cobranch/token"
	// keyDir holds the provider's key, which only the model proxy mounts.
	keyDir = "/cobranch/key"
	// home is the harness's home directory, where Git and the tools keep their own files.
	home = "/home/agent"
	// proxyAddr is where the model proxy listens: in the pod alone.
	proxyAddr = "127.0.0.1:8090"
)

// The Secret keys of a task's pod beside its variables, which never hold a dot.
const (
	keyToken = "cobranch.token"
	keyKey   = "cobranch.key"
)

// The volumes of a task's pod.
const (
	volumeBin   = "cobranch-bin"
	volumeToken = "cobranch-token"
	volumeKey   = "cobranch-key"
	volumeHome  = "cobranch-home"
)

// proxyResources are the model proxy's: it forwards a request at a time.
var proxyResources = corev1.ResourceRequirements{
	Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("10m"), corev1.ResourceMemory: resource.MustParse("32Mi")},
	Limits:   corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("128Mi")},
}

// taskPodName is the name of a task's pod numbered n, and of its Secret.
func taskPodName(taskID uuid.UUID, n int) string {
	return "task-" + taskID.String() + "-" + strconv.Itoa(n)
}

// taskAttrs are a span's attributes naming the task and its organization.
func taskAttrs(orgID, taskID uuid.UUID) trace.SpanStartOption {
	return trace.WithAttributes(attribute.String("cobranch.org.id", orgID.String()), attribute.String("cobranch.task.id", taskID.String()))
}

// StartTask creates the task's pod in its organization's namespace, then its Secret, which the pod owns and waits for
// (taskrunner.Cluster). A pod of that number left by an earlier attempt, which may hold an older token, is removed
// with the task's others instead, and the task starts it again on a later pass.
func (c *Cluster) StartTask(ctx context.Context, l taskrunner.Launch) (err error) {
	ctx, span := tracer.Start(ctx, "jobcluster.Cluster.StartTask", taskAttrs(l.Task.OrgID, l.Task.ID))
	defer span.End()
	defer func() { record(span, err) }()
	ns, name := c.namespace(l.Task.OrgID), taskPodName(l.Task.ID, l.Pod)
	created, err := c.client.CoreV1().Pods(ns).Create(ctx, c.taskPodOf(l), metav1.CreateOptions{FieldManager: fieldManager})
	switch {
	case apierrors.IsAlreadyExists(err):
		if err := c.RemoveTask(ctx, l.Task.OrgID, l.Task.ID); err != nil {
			return err
		}
		return fmt.Errorf("task %s's pod %d from an earlier attempt was still there; it is removed, and the pod starts again", l.Task.ID, l.Pod)
	case apierrors.IsNotFound(err):
		c.forget(l.Task.OrgID)
		return fmt.Errorf("creating pod %s in namespace %s, which isn't there: %w", name, ns, err)
	case err != nil:
		return classify(err, "creating pod "+name)
	}
	data := map[string][]byte{keyToken: []byte(l.Token), keyKey: l.Key}
	for k, v := range l.Env {
		data[k] = []byte(v)
	}
	owner := metav1.OwnerReference{APIVersion: "v1", Kind: "Pod", Name: created.Name, UID: created.UID}
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, Labels: created.Labels,
		OwnerReferences: []metav1.OwnerReference{owner}}, Type: corev1.SecretTypeOpaque, Data: data}
	secrets := c.client.CoreV1().Secrets(ns)
	if _, err = secrets.Create(ctx, secret, metav1.CreateOptions{FieldManager: fieldManager}); apierrors.IsAlreadyExists(err) {
		_, err = secrets.Update(ctx, secret, metav1.UpdateOptions{FieldManager: fieldManager})
	}
	if err != nil {
		return classify(err, "creating Secret "+name)
	}
	return nil
}

// taskPodOf is the pod numbered l.Pod of the task: the harness in the agents' image, beside the model proxy, which
// alone holds the provider's key, and after the copy of the cobranch binary it runs.
func (c *Cluster) taskPodOf(l taskrunner.Launch) *corev1.Pod {
	name := taskPodName(l.Task.ID, l.Pod)
	deadline := int64(l.Timeout.Seconds())
	secretVolume := func(volume, key, path string) corev1.Volume {
		return corev1.Volume{Name: volume, VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{
			SecretName: name, DefaultMode: new(int32(0o440)), Items: []corev1.KeyToPath{{Key: key, Path: path}}}}}
	}
	emptyDir := func(volume string) corev1.Volume {
		return corev1.Volume{Name: volume, VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}}
	}
	copyBin := corev1.Container{Name: "cobranch-bin", Image: c.cfg.CheckoutImage, Command: []string{"cp", "/usr/local/bin/cobranch", binDir + "/cobranch"},
		VolumeMounts: []corev1.VolumeMount{{Name: volumeBin, MountPath: binDir}}, Resources: proxyResources}
	proxy := corev1.Container{Name: "model-proxy", Image: c.cfg.CheckoutImage, Args: []string{"model-proxy"}, RestartPolicy: new(corev1.ContainerRestartPolicyAlways),
		Env: []corev1.EnvVar{{Name: "COBRANCH_HTTP_ADDR", Value: proxyAddr}, {Name: "COBRANCH_MODEL_KEY_FILE", Value: keyDir + "/key"},
			{Name: "COBRANCH_MODEL_PROVIDER", Value: l.Task.Provider}},
		VolumeMounts: []corev1.VolumeMount{{Name: volumeKey, MountPath: keyDir, ReadOnly: true}}, Resources: proxyResources}
	if u := c.cfg.ProviderURLs[l.Task.Provider]; u != "" {
		proxy.Env = append(proxy.Env, corev1.EnvVar{Name: "COBRANCH_MODEL_URL", Value: u})
	}
	harness := corev1.Container{Name: "harness", Image: c.cfg.AgentImage, Command: []string{binDir + "/cobranch", "agent"}, WorkingDir: workspace,
		Env: append([]corev1.EnvVar{{Name: "HOME", Value: home}, {Name: "COBRANCH_WORKSPACE", Value: workspace},
			{Name: "COBRANCH_TOKEN_FILE", Value: tokenDir + "/token"}, {Name: "COBRANCH_MODEL_URL", Value: "http://" + proxyAddr}}, taskEnv(name, l.Env)...),
		VolumeMounts: []corev1.VolumeMount{{Name: volumeWorkspace, MountPath: workspace}, {Name: volumeHome, MountPath: home},
			{Name: volumeBin, MountPath: binDir, ReadOnly: true}, {Name: volumeToken, MountPath: tokenDir, ReadOnly: true}},
		Resources: c.cfg.AgentResources}
	containers := []*corev1.Container{&copyBin, &proxy, &harness}
	for _, container := range containers {
		container.SecurityContext = &corev1.SecurityContext{AllowPrivilegeEscalation: new(false),
			Capabilities: &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}}}
	}
	labels := map[string]string{labelManagedBy: "cobranch", labelTask: l.Task.ID.String(), labelTaskPod: strconv.Itoa(l.Pod)}
	return &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: c.namespace(l.Task.OrgID), Labels: labels},
		Spec: corev1.PodSpec{InitContainers: []corev1.Container{copyBin, proxy}, Containers: []corev1.Container{harness},
			RestartPolicy: corev1.RestartPolicyNever, ActiveDeadlineSeconds: &deadline, EnableServiceLinks: new(false),
			AutomountServiceAccountToken: new(false), NodeSelector: c.cfg.NodeSelector, Tolerations: c.cfg.Tolerations, Affinity: c.cfg.Affinity,
			SecurityContext: podSecurity(nil), ImagePullSecrets: c.pullSecretRefs(),
			Volumes: []corev1.Volume{emptyDir(volumeWorkspace), emptyDir(volumeHome), emptyDir(volumeBin), secretVolume(volumeToken, keyToken, "token"),
				secretVolume(volumeKey, keyKey, "key")}}}
}

// pullSecretRefs are the pod's references to the copies of the installation's image pull secrets in its namespace.
func (c *Cluster) pullSecretRefs() []corev1.LocalObjectReference {
	var refs []corev1.LocalObjectReference
	for _, name := range c.cfg.ImagePullSecrets {
		refs = append(refs, corev1.LocalObjectReference{Name: name})
	}
	return refs
}

// taskEnv is the harness's variables of env, each read from the task's pod's Secret, in order.
func taskEnv(secret string, env map[string]string) []corev1.EnvVar {
	names := slices.Sorted(maps.Keys(env))
	out := make([]corev1.EnvVar, 0, len(names))
	for _, name := range names {
		out = append(out, corev1.EnvVar{Name: name, ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: secret}, Key: name}}})
	}
	return out
}

// TaskPods reports the organization's tasks' newest pods, by task ID (taskrunner.Cluster).
func (c *Cluster) TaskPods(ctx context.Context, orgID uuid.UUID) (_ map[uuid.UUID]taskrunner.Pod, err error) {
	ctx, span := tracer.Start(ctx, "jobcluster.Cluster.TaskPods", orgAttr(orgID))
	defer span.End()
	defer func() { record(span, err) }()
	list, err := c.client.CoreV1().Pods(c.namespace(orgID)).List(ctx, metav1.ListOptions{LabelSelector: labelTask})
	if err != nil {
		return nil, fmt.Errorf("listing the task pods in namespace %s: %w", c.namespace(orgID), err)
	}
	out := map[uuid.UUID]taskrunner.Pod{}
	for i := range list.Items {
		pod := &list.Items[i]
		id, err := uuid.Parse(pod.Labels[labelTask])
		if err != nil {
			continue
		}
		n, err := strconv.Atoi(pod.Labels[labelTaskPod])
		if err != nil || n <= out[id].Number {
			continue
		}
		out[id] = taskrunner.Pod{Number: n, Pod: outcomeOf(pod)}
	}
	return out, nil
}

// Renew writes token into the Secret of the task's pod numbered n, whose volume the harness reads it from
// (taskrunner.Cluster).
func (c *Cluster) Renew(ctx context.Context, orgID, taskID uuid.UUID, n int, token string) (err error) {
	ctx, span := tracer.Start(ctx, "jobcluster.Cluster.Renew", taskAttrs(orgID, taskID))
	defer span.End()
	defer func() { record(span, err) }()
	patch, err := json.Marshal(map[string]any{"data": map[string][]byte{keyToken: []byte(token)}})
	if err != nil {
		return fmt.Errorf("encoding the token's patch: %w", err)
	}
	name := taskPodName(taskID, n)
	_, err = c.client.CoreV1().Secrets(c.namespace(orgID)).Patch(ctx, name, types.MergePatchType, patch, metav1.PatchOptions{FieldManager: fieldManager})
	if err != nil {
		return fmt.Errorf("writing the token into Secret %s: %w", name, err)
	}
	return nil
}

// RemoveTask deletes every pod of the task, and their Secrets (taskrunner.Cluster).
func (c *Cluster) RemoveTask(ctx context.Context, orgID, taskID uuid.UUID) (err error) {
	ctx, span := tracer.Start(ctx, "jobcluster.Cluster.RemoveTask", taskAttrs(orgID, taskID))
	defer span.End()
	defer func() { record(span, err) }()
	ns, selector := c.namespace(orgID), metav1.ListOptions{LabelSelector: labelTask + "=" + taskID.String()}
	pods, err := c.client.CoreV1().Pods(ns).List(ctx, selector)
	if err != nil {
		return fmt.Errorf("listing task %s's pods: %w", taskID, err)
	}
	secrets, err := c.client.CoreV1().Secrets(ns).List(ctx, selector)
	if err != nil {
		return fmt.Errorf("listing task %s's Secrets: %w", taskID, err)
	}
	for _, pod := range pods.Items {
		if err := c.client.CoreV1().Pods(ns).Delete(ctx, pod.Name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("deleting pod %s: %w", pod.Name, err)
		}
	}
	for _, secret := range secrets.Items {
		if err := c.client.CoreV1().Secrets(ns).Delete(ctx, secret.Name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("deleting Secret %s: %w", secret.Name, err)
		}
	}
	return nil
}

var _ taskrunner.Cluster = (*Cluster)(nil)
