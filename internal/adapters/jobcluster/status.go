package jobcluster

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"cobranch.dev/internal/services/jobs"
	"cobranch.dev/internal/services/runner"
)

// minMasked is the shortest value a run's logs mask: a shorter one would hide every place its letters happen to
// appear, and tells little.
const minMasked = 4

// Pods reports the organization's runs' pods, by run ID (runner.Cluster).
func (c *Cluster) Pods(ctx context.Context, orgID uuid.UUID) (map[uuid.UUID]runner.Pod, error) {
	return c.pods(ctx, orgID, runs)
}

// Renders reports the organization's render pods, by delivery ID (deliverer.Pods).
func (c *Cluster) Renders(ctx context.Context, orgID uuid.UUID) (map[uuid.UUID]runner.Pod, error) {
	return c.pods(ctx, orgID, renders)
}

// pods reports the organization's pods of the workload, by its ID, each with its steps.
func (c *Cluster) pods(ctx context.Context, orgID uuid.UUID, w workload) (_ map[uuid.UUID]runner.Pod, err error) {
	ctx, span := tracer.Start(ctx, "jobcluster.Cluster.Pods", orgAttr(orgID))
	defer span.End()
	defer func() { record(span, err) }()
	list, err := c.client.CoreV1().Pods(c.namespace(orgID)).List(ctx, metav1.ListOptions{LabelSelector: w.label})
	if err != nil {
		return nil, fmt.Errorf("listing the pods in namespace %s: %w", c.namespace(orgID), err)
	}
	out := make(map[uuid.UUID]runner.Pod, len(list.Items))
	for i := range list.Items {
		if id, err := uuid.Parse(list.Items[i].Labels[w.label]); err == nil {
			pod := outcomeOf(&list.Items[i])
			pod.Steps = stepsOf(&list.Items[i])
			out[id] = pod
		}
	}
	return out, nil
}

// outcomeOf is how a run's pod stands: Pending until it ends, which a pod being deleted hasn't.
func outcomeOf(pod *corev1.Pod) runner.Pod {
	switch pod.Status.Phase {
	case corev1.PodSucceeded:
		return runner.Pod{Outcome: runner.Succeeded}
	case corev1.PodFailed:
		return runner.Pod{Outcome: runner.Failed, Reason: failureOf(pod)}
	}
	return runner.Pod{Outcome: runner.Pending}
}

// stepsOf is each of the pod's containers as a step of its run, in the order they run: its init containers', the
// checkout's first, then its containers'.
func stepsOf(pod *corev1.Pod) []jobs.Step {
	statuses := map[string]corev1.ContainerStatus{}
	for _, s := range slices.Concat(pod.Status.InitContainerStatuses, pod.Status.ContainerStatuses) {
		statuses[s.Name] = s
	}
	containers := slices.Concat(pod.Spec.InitContainers, pod.Spec.Containers)
	steps := make([]jobs.Step, 0, len(containers))
	for _, c := range containers {
		steps = append(steps, stepOf(c.Name, statuses[c.Name].State))
	}
	return steps
}

// stepOf is a container named name as a step, as its state says it stands: waiting until it starts, and succeeded or
// failed by its exit code once it ends. The reason is the cluster's only where it says more than that: OOMKilled, not
// Completed or Error; ImagePullBackOff, not PodInitializing or ContainerCreating.
func stepOf(name string, state corev1.ContainerState) jobs.Step {
	step := jobs.Step{Name: name, State: jobs.StepWaiting}
	switch {
	case state.Terminated != nil:
		t := state.Terminated
		step.State, step.ExitCode, step.StartedAt, step.EndedAt = jobs.StepSucceeded, t.ExitCode, timeOf(t.StartedAt), timeOf(t.FinishedAt)
		if t.ExitCode != 0 {
			step.State = jobs.StepFailed
		}
		if t.Reason != "Completed" && t.Reason != "Error" {
			step.Reason = t.Reason
		}
	case state.Running != nil:
		step.State, step.StartedAt = jobs.StepRunning, timeOf(state.Running.StartedAt)
	case state.Waiting != nil && state.Waiting.Reason != "PodInitializing" && state.Waiting.Reason != "ContainerCreating":
		step.Reason = state.Waiting.Reason
	}
	return step
}

// timeOf is a Kubernetes time in UTC, or nil for none.
func timeOf(t metav1.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	at := t.UTC()
	return &at
}

// failureOf says how a failed pod failed: its deadline passed, the first of its containers that failed and how, or
// what the cluster says.
func failureOf(pod *corev1.Pod) string {
	if pod.Status.Reason == "DeadlineExceeded" && pod.Spec.ActiveDeadlineSeconds != nil {
		return "timed out after " + (time.Duration(*pod.Spec.ActiveDeadlineSeconds) * time.Second).String()
	}
	for _, s := range slices.Concat(pod.Status.InitContainerStatuses, pod.Status.ContainerStatuses) {
		t := s.State.Terminated
		switch {
		case t == nil || t.ExitCode == 0:
		case t.Reason != "" && t.Reason != "Error":
			return fmt.Sprintf("container %s exited %d: %s", s.Name, t.ExitCode, t.Reason)
		default:
			return fmt.Sprintf("container %s exited %d", s.Name, t.ExitCode)
		}
	}
	if pod.Status.Message != "" {
		return "its pod failed: " + pod.Status.Message
	}
	return "its pod failed"
}

// Logs reads each of the run's pod's containers' logs, at most limit bytes each, each line with its time before it
// (jobs.Stamp) and the run's masked values hidden, by container; none for a pod that is gone, or a container that never
// started (runner.Cluster).
func (c *Cluster) Logs(ctx context.Context, orgID, runID uuid.UUID, limit int64) (map[string][]byte, error) {
	return c.logs(ctx, orgID, runs, runID, limit, true)
}

// RenderLogs reads each of the delivery's render pod's containers' logs, as Logs does a run's but without their lines'
// times (deliverer.Pods).
func (c *Cluster) RenderLogs(ctx context.Context, orgID, deliveryID uuid.UUID, limit int64) (map[string][]byte, error) {
	return c.logs(ctx, orgID, renders, deliveryID, limit, false)
}

// Steps reads the run's pod's containers as its steps, in the order they run; none for a pod that is gone
// (code.LiveLogs).
func (c *Cluster) Steps(ctx context.Context, orgID, runID uuid.UUID) (_ []jobs.Step, err error) {
	ctx, span := tracer.Start(ctx, "jobcluster.Cluster.Steps", runs.attrs(orgID, runID))
	defer span.End()
	defer func() { record(span, err) }()
	name := runs.name(runID)
	pod, err := c.client.CoreV1().Pods(c.namespace(orgID)).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("finding pod %s: %w", name, err)
	}
	return stepsOf(pod), nil
}

// logs reads each of the containers' logs of the workload's pod of id, each line with its time before it when stamped.
func (c *Cluster) logs(ctx context.Context, orgID uuid.UUID, w workload, id uuid.UUID, limit int64, stamped bool) (_ map[string][]byte, err error) {
	ctx, span := tracer.Start(ctx, "jobcluster.Cluster.Logs", w.attrs(orgID, id))
	defer span.End()
	defer func() { record(span, err) }()
	pods, name := c.client.CoreV1().Pods(c.namespace(orgID)), w.name(id)
	pod, err := pods.Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("finding pod %s: %w", name, err)
	}
	masked, err := c.masked(ctx, pod)
	if err != nil {
		return nil, err
	}
	out := map[string][]byte{}
	for _, container := range slices.Concat(pod.Spec.InitContainers, pod.Spec.Containers) {
		log, err := pods.GetLogs(name, &corev1.PodLogOptions{Container: container.Name, LimitBytes: &limit, Timestamps: stamped}).DoRaw(ctx)
		switch {
		case apierrors.IsBadRequest(err), apierrors.IsNotFound(err):
			continue // it never started, or the pod went meanwhile
		case err != nil:
			return nil, fmt.Errorf("reading pod %s's %s log: %w", name, container.Name, err)
		}
		out[container.Name] = maskLines(log, masked, stamped)
	}
	return out, nil
}

// masked reads the values the run's logs never show from its Secret; none when it is gone.
func (c *Cluster) masked(ctx context.Context, pod *corev1.Pod) ([]string, error) {
	secret, err := c.client.CoreV1().Secrets(pod.Namespace).Get(ctx, pod.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading Secret %s: %w", pod.Name, err)
	}
	var masked []string
	if err := json.Unmarshal(secret.Data[keyMasked], &masked); err != nil && len(secret.Data[keyMasked]) > 0 {
		return nil, fmt.Errorf("decoding Secret %s's masked values: %w", pod.Name, err)
	}
	return masked, nil
}

// maskLines masks values in log as mask does, but in each line's text alone when stamped, so that no value hides a
// line's time.
func maskLines(log []byte, values []string, stamped bool) []byte {
	if !stamped || len(values) == 0 {
		return mask(log, values)
	}
	var out []byte
	for _, line := range bytes.SplitAfter(log, []byte("\n")) {
		if _, text, ok := jobs.Stamp(line); ok {
			out = append(append(out, line[:len(line)-len(text)]...), mask(text, values)...)
			continue
		}
		out = append(out, mask(line, values)...)
	}
	return out
}

// mask replaces each of values in log, and each line of a value of several, longest first so that none shows through
// another's mask, with ***.
func mask(log []byte, values []string) []byte {
	var all []string
	for _, v := range values {
		all = append(all, v)
		if strings.Contains(v, "\n") {
			all = append(all, strings.Split(v, "\n")...)
		}
	}
	slices.SortFunc(all, func(a, b string) int { return len(b) - len(a) })
	for _, v := range all {
		if v = strings.TrimSpace(v); len(v) >= minMasked {
			log = bytes.ReplaceAll(log, []byte(v), []byte("***"))
		}
	}
	return log
}

// Remove deletes the run's pod, and its Secret with it (runner.Cluster).
func (c *Cluster) Remove(ctx context.Context, orgID, runID uuid.UUID) (err error) {
	ctx, span := tracer.Start(ctx, "jobcluster.Cluster.Remove", runs.attrs(orgID, runID))
	defer span.End()
	defer func() { record(span, err) }()
	return c.remove(ctx, c.namespace(orgID), runs.name(runID))
}

// RemoveRender deletes the delivery's render pod, and its Secret with it (deliverer.Pods).
func (c *Cluster) RemoveRender(ctx context.Context, orgID, deliveryID uuid.UUID) (err error) {
	ctx, span := tracer.Start(ctx, "jobcluster.Cluster.Remove", renders.attrs(orgID, deliveryID))
	defer span.End()
	defer func() { record(span, err) }()
	return c.remove(ctx, c.namespace(orgID), renders.name(deliveryID))
}

// remove deletes pod name in namespace ns, and its Secret.
func (c *Cluster) remove(ctx context.Context, ns, name string) error {
	if err := c.client.CoreV1().Pods(ns).Delete(ctx, name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("deleting pod %s: %w", name, err)
	}
	if err := c.client.CoreV1().Secrets(ns).Delete(ctx, name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("deleting Secret %s: %w", name, err)
	}
	return nil
}
