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

	"cobranch.dev/internal/services/runner"
)

// minMasked is the shortest value a run's logs mask: a shorter one would hide every place its letters happen to
// appear, and tells little.
const minMasked = 4

// Pods reports the organization's runs' pods, by run ID (runner.Cluster).
func (c *Cluster) Pods(ctx context.Context, orgID uuid.UUID) (_ map[uuid.UUID]runner.Pod, err error) {
	ctx, span := tracer.Start(ctx, "jobcluster.Cluster.Pods", orgAttr(orgID))
	defer span.End()
	defer func() { record(span, err) }()
	list, err := c.client.CoreV1().Pods(c.namespace(orgID)).List(ctx, metav1.ListOptions{LabelSelector: labelRun})
	if err != nil {
		return nil, fmt.Errorf("listing the pods in namespace %s: %w", c.namespace(orgID), err)
	}
	out := make(map[uuid.UUID]runner.Pod, len(list.Items))
	for i := range list.Items {
		if id, err := uuid.Parse(list.Items[i].Labels[labelRun]); err == nil {
			out[id] = outcomeOf(&list.Items[i])
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

// Logs reads each of the run's pod's containers' logs, at most limit bytes each, with the run's masked values hidden,
// by container; none for a pod that is gone, or a container that never started (runner.Cluster).
func (c *Cluster) Logs(ctx context.Context, orgID, runID uuid.UUID, limit int64) (_ map[string][]byte, err error) {
	ctx, span := tracer.Start(ctx, "jobcluster.Cluster.Logs", runAttrs(orgID, runID))
	defer span.End()
	defer func() { record(span, err) }()
	pods, name := c.client.CoreV1().Pods(c.namespace(orgID)), podName(runID)
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
		log, err := pods.GetLogs(name, &corev1.PodLogOptions{Container: container.Name, LimitBytes: &limit}).DoRaw(ctx)
		switch {
		case apierrors.IsBadRequest(err), apierrors.IsNotFound(err):
			continue // it never started, or the pod went meanwhile
		case err != nil:
			return nil, fmt.Errorf("reading pod %s's %s log: %w", name, container.Name, err)
		}
		out[container.Name] = mask(log, masked)
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
	ctx, span := tracer.Start(ctx, "jobcluster.Cluster.Remove", runAttrs(orgID, runID))
	defer span.End()
	defer func() { record(span, err) }()
	ns, name := c.namespace(orgID), podName(runID)
	if err := c.client.CoreV1().Pods(ns).Delete(ctx, name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("deleting pod %s: %w", name, err)
	}
	if err := c.client.CoreV1().Secrets(ns).Delete(ctx, name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("deleting Secret %s: %w", name, err)
	}
	return nil
}
