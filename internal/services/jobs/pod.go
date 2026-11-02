package jobs

import (
	"fmt"
	"slices"
)

// forbiddenPodFields are the fields of a pod spec that would take a run out of its namespace's bounds: a
// ServiceAccount or its token, the node's namespaces, a node, where the scheduler places it, or how much it outranks
// others. The operator places job pods; the controller sets restartPolicy and the deadline itself.
var forbiddenPodFields = []string{
	"serviceAccountName", "serviceAccount", "automountServiceAccountToken", "hostNetwork", "hostPID", "hostIPC",
	"hostUsers", "nodeName", "nodeSelector", "affinity", "tolerations", "schedulerName", "priorityClassName", "priority",
	"preemptionPolicy", "runtimeClassName", "restartPolicy", "activeDeadlineSeconds",
}

// allowedVolumeSources are the only volumes a job's pod may mount: scratch space that lives and dies with it.
var allowedVolumeSources = []string{"emptyDir", "ephemeral"}

// CheckPod checks a job's pod spec, as JSON decodes it, against what jobs may use: one or more containers, each named
// and with an image, no field of forbiddenPodFields, no volume but emptyDir and ephemeral ones, and no privileged
// container. It answers what's wrong, naming the field, or nil. Kubernetes checks the rest when the pod is created.
func CheckPod(spec map[string]any) error {
	for _, field := range forbiddenPodFields {
		if _, ok := spec[field]; ok {
			return fmt.Errorf("a job's pod may not set %s", field)
		}
	}
	containers, ok := spec["containers"].([]any)
	if !ok || len(containers) == 0 {
		return fmt.Errorf("a job's pod needs containers, a list of at least one")
	}
	for _, key := range []string{"initContainers", "containers"} {
		list, ok := spec[key].([]any)
		if spec[key] != nil && !ok {
			return fmt.Errorf("%s must be a list", key)
		}
		for i, c := range list {
			if err := checkContainer(key, i, c); err != nil {
				return err
			}
		}
	}
	if _, ok := spec["ephemeralContainers"]; ok {
		return fmt.Errorf("a job's pod may not set ephemeralContainers")
	}
	return checkVolumes(spec["volumes"])
}

// checkContainer checks the i-th container of a pod spec's list key: named, with an image, and not privileged.
func checkContainer(key string, i int, c any) error {
	container, ok := c.(map[string]any)
	if !ok {
		return fmt.Errorf("%s[%d] must be a container", key, i)
	}
	for _, field := range [][2]string{{"name", "a name"}, {"image", "an image"}} {
		if s, ok := container[field[0]].(string); !ok || s == "" {
			return fmt.Errorf("%s[%d] needs %s", key, i, field[1])
		}
	}
	security, _ := container["securityContext"].(map[string]any) // none is fine
	if privileged, _ := security["privileged"].(bool); privileged {
		return fmt.Errorf("%s[%d] may not be privileged", key, i)
	}
	return nil
}

// checkVolumes checks that a pod spec's volumes, if any, are all scratch space.
func checkVolumes(v any) error {
	if v == nil {
		return nil
	}
	volumes, ok := v.([]any)
	if !ok {
		return fmt.Errorf("volumes must be a list")
	}
	for i, vol := range volumes {
		volume, ok := vol.(map[string]any)
		if !ok {
			return fmt.Errorf("volumes[%d] must be a volume", i)
		}
		for field := range volume {
			if field != "name" && !slices.Contains(allowedVolumeSources, field) {
				return fmt.Errorf("volumes[%d] may only be emptyDir or ephemeral, not %s", i, field)
			}
		}
	}
	return nil
}
