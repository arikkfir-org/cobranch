package jobcluster

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kjson "sigs.k8s.io/json"

	"cobranch.dev/internal/services/runner"
)

// Where every container of a run finds the run.
const (
	// workspace is the directory every container shares, into which the checkout fetches the commit.
	workspace = "/workspace"
	// runDir holds event.json and, for a script, the script, from the run's Secret.
	runDir = "/cobranch"
)

// The run's Secret's keys beside its variables, which are environment variables' names and so never hold a dot.
const (
	keyEvent  = "cobranch.event.json"
	keyScript = "cobranch.script"
	// keyMasked holds the values its logs never show, as a JSON list, for Logs.
	keyMasked = "cobranch.masked"
)

// The volumes the controller adds to every run's pod.
const (
	volumeWorkspace = "cobranch-workspace"
	volumeRun       = "cobranch-run"
)

// Start creates the run's pod in its organization's namespace, then its Secret, which the pod owns and waits for
// (runner.Cluster). A pod left by an earlier attempt, which may have started with an older token, is removed instead,
// and the run starts again once its claim lapses.
func (c *Cluster) Start(ctx context.Context, l runner.Launch) (err error) {
	ctx, span := tracer.Start(ctx, "jobcluster.Cluster.Start", runAttrs(l.Run.OrgID, l.Run.ID))
	defer span.End()
	defer func() { record(span, err) }()
	pod, err := c.podOf(l)
	if err != nil {
		return fmt.Errorf("%w: %w", runner.ErrRefused, err)
	}
	ns := c.namespace(l.Run.OrgID)
	created, err := c.client.CoreV1().Pods(ns).Create(ctx, pod, metav1.CreateOptions{FieldManager: fieldManager})
	switch {
	case apierrors.IsAlreadyExists(err):
		if err := c.Remove(ctx, l.Run.OrgID, l.Run.ID); err != nil {
			return err
		}
		return fmt.Errorf("run %s's pod from an earlier attempt was still there; it is removed, and the run starts again", l.Run.ID)
	case apierrors.IsNotFound(err):
		c.forget(l.Run.OrgID)
		return fmt.Errorf("creating run %s's pod in namespace %s, which isn't there: %w", l.Run.ID, ns, err)
	case err != nil:
		return classify(err, "creating run "+l.Run.ID.String()+"'s pod")
	}
	secret, err := secretOf(l, created)
	if err != nil {
		return err
	}
	secrets := c.client.CoreV1().Secrets(ns)
	if _, err = secrets.Create(ctx, secret, metav1.CreateOptions{FieldManager: fieldManager}); apierrors.IsAlreadyExists(err) {
		_, err = secrets.Update(ctx, secret, metav1.UpdateOptions{FieldManager: fieldManager})
	}
	if err != nil {
		return classify(err, "creating run "+l.Run.ID.String()+"'s Secret")
	}
	return nil
}

// classify names why the cluster refused to create an object: ErrNoRoom for the quota, ErrRefused for a spec it, or
// its admission, refuses, with the cluster's message, and the error as it is otherwise.
func classify(err error, what string) error {
	var status apierrors.APIStatus
	message := err.Error()
	if errors.As(err, &status) {
		message = status.Status().Message
	}
	switch {
	case apierrors.IsForbidden(err) && strings.Contains(message, "exceeded quota"):
		return fmt.Errorf("%s: %w: %s", what, runner.ErrNoRoom, message)
	case apierrors.IsInvalid(err), apierrors.IsForbidden(err) && (strings.Contains(message, "violates PodSecurity") ||
		strings.Contains(message, "denied request")):
		return fmt.Errorf("%w: %s", runner.ErrRefused, message)
	}
	return fmt.Errorf("%s: %w", what, err)
}

// podOf is the run's pod: its job's pod spec, or a script's container, with what every run needs.
func (c *Cluster) podOf(l runner.Launch) (*corev1.Pod, error) {
	spec, err := specOf(l)
	if err != nil {
		return nil, err
	}
	for _, v := range spec.Volumes {
		if v.Name == volumeWorkspace || v.Name == volumeRun {
			return nil, fmt.Errorf("its pod's volume %s takes a name of Cobranch's own", v.Name)
		}
	}
	deadline := int64(l.Timeout / time.Second)
	spec.RestartPolicy, spec.ActiveDeadlineSeconds = corev1.RestartPolicyNever, &deadline
	spec.EnableServiceLinks, spec.AutomountServiceAccountToken = new(false), new(false)
	// The namespace holds the installation's image pull secrets for agents' pods; a run never pulls with them, even one
	// queued before jobs refused them.
	spec.ImagePullSecrets = nil
	spec.NodeSelector, spec.Tolerations, spec.Affinity = c.cfg.NodeSelector, c.cfg.Tolerations, c.cfg.Affinity
	spec.SecurityContext = podSecurity(spec.SecurityContext)
	spec.Volumes = append(spec.Volumes,
		corev1.Volume{Name: volumeWorkspace, VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
		corev1.Volume{Name: volumeRun, VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{
			SecretName: podName(l.Run.ID), DefaultMode: new(int32(0o444)),
			Items: []corev1.KeyToPath{{Key: keyEvent, Path: "event.json"}, {Key: keyScript, Path: "script"}}}}})
	if l.Checkout {
		checkout := corev1.Container{Name: "cobranch-checkout", Image: c.cfg.CheckoutImage, Args: []string{"checkout"}}
		spec.InitContainers = append([]corev1.Container{checkout}, spec.InitContainers...)
	}
	env := envOf(l)
	for i := range spec.InitContainers {
		dress(&spec.InitContainers[i], env)
	}
	for i := range spec.Containers {
		dress(&spec.Containers[i], env)
	}
	return &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: podName(l.Run.ID), Namespace: c.namespace(l.Run.OrgID),
		Labels: map[string]string{labelManagedBy: "cobranch", labelRun: l.Run.ID.String()}}, Spec: spec}, nil
}

// specOf is the run's job's pod spec, or a pod of one container that runs the script in the image.
func specOf(l runner.Launch) (corev1.PodSpec, error) {
	if l.Pod == nil {
		return corev1.PodSpec{Containers: []corev1.Container{{Name: "main", Image: l.Image,
			Command: []string{"bash", "-eo", "pipefail", runDir + "/script"}}}}, nil
	}
	// Decoded as Kubernetes decodes it, matching field names case-sensitively and refusing unknown and repeated ones:
	// jobs checked the spec's fields by their exact names, and encoding/json would also take a field spelt otherwise,
	// such as Env for env, past those checks.
	var spec corev1.PodSpec
	strict, err := kjson.UnmarshalStrict(l.Pod, &spec)
	if err == nil {
		err = errors.Join(strict...)
	}
	if err != nil {
		return spec, fmt.Errorf("its pod isn't a pod spec: %w", err)
	}
	return spec, nil
}

// podSecurity is a pod's security context with the restricted Pod Security Standard's pod half where the job set
// none: not root, as Cobranch's image's user unless the job names another, and the runtime's seccomp profile.
func podSecurity(sc *corev1.PodSecurityContext) *corev1.PodSecurityContext {
	if sc == nil {
		sc = &corev1.PodSecurityContext{}
	}
	if sc.RunAsNonRoot == nil {
		sc.RunAsNonRoot = new(true)
	}
	if sc.RunAsUser == nil {
		sc.RunAsUser, sc.RunAsGroup = new(int64(65532)), new(int64(65532))
	}
	if sc.FSGroup == nil {
		sc.FSGroup = new(int64(65532))
	}
	if sc.SeccompProfile == nil {
		sc.SeccompProfile = &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault}
	}
	return sc
}

// dress gives a container what every container of a run gets: the run's variables after its own, so that they win,
// the workspace, where it starts unless it names a directory, and the run's files, and the restricted Pod Security
// Standard's container half where the job set none.
func dress(container *corev1.Container, env []corev1.EnvVar) {
	container.Env = append(container.Env, env...)
	if !slices.ContainsFunc(container.VolumeMounts, func(m corev1.VolumeMount) bool { return m.MountPath == workspace }) {
		container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{Name: volumeWorkspace, MountPath: workspace})
	}
	container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{Name: volumeRun, MountPath: runDir, ReadOnly: true})
	if container.WorkingDir == "" {
		container.WorkingDir = workspace
	}
	if container.SecurityContext == nil {
		container.SecurityContext = &corev1.SecurityContext{}
	}
	if container.SecurityContext.AllowPrivilegeEscalation == nil {
		container.SecurityContext.AllowPrivilegeEscalation = new(false)
	}
	if container.SecurityContext.Capabilities == nil {
		container.SecurityContext.Capabilities = &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}}
	}
}

// envOf is every variable of the run, Cobranch's and the job's secrets, each read from the run's Secret, in order.
func envOf(l runner.Launch) []corev1.EnvVar {
	names := make([]string, 0, len(l.Env))
	for name := range l.Env {
		names = append(names, name)
	}
	slices.Sort(names)
	env := make([]corev1.EnvVar, 0, len(names))
	for _, name := range names {
		env = append(env, corev1.EnvVar{Name: name, ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: podName(l.Run.ID)}, Key: name}}})
	}
	return env
}

// secretOf is the run's Secret, which pod owns: its variables, its event, its script, and the values its logs mask.
func secretOf(l runner.Launch, pod *corev1.Pod) (*corev1.Secret, error) {
	masked, err := json.Marshal(l.Masked)
	if err != nil {
		return nil, fmt.Errorf("encoding run %s's masked values: %w", l.Run.ID, err)
	}
	data := map[string][]byte{keyEvent: l.Run.Event, keyScript: []byte(l.Script), keyMasked: masked}
	for name, value := range l.Env {
		data[name] = []byte(value)
	}
	owner := metav1.OwnerReference{APIVersion: "v1", Kind: "Pod", Name: pod.Name, UID: pod.UID}
	return &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: pod.Name, Namespace: pod.Namespace, Labels: pod.Labels,
		OwnerReferences: []metav1.OwnerReference{owner}}, Type: corev1.SecretTypeOpaque, Data: data}, nil
}
