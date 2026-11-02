package jobs

import (
	"fmt"
	"slices"
)

// forbiddenPodFields are the fields of a pod spec that would take a run out of its namespace's bounds: a
// ServiceAccount or its token, the installation's image pull secrets, which the namespace holds for agents' pods, the
// node's namespaces, a node, where the scheduler places it, or how much it outranks others. The operator places job
// pods; the controller sets restartPolicy and the deadline itself.
var forbiddenPodFields = []string{
	"serviceAccountName", "serviceAccount", "automountServiceAccountToken", "imagePullSecrets", "hostNetwork", "hostPID",
	"hostIPC", "hostUsers", "nodeName", "nodeSelector", "affinity", "tolerations", "schedulerName", "priorityClassName",
	"priority", "preemptionPolicy", "runtimeClassName", "restartPolicy", "activeDeadlineSeconds",
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

// checkContainer checks the i-th container of a pod spec's list key: named, with an image, not privileged, and
// reading no Secret or ConfigMap of the namespace (checkEnv).
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
	return checkEnv(fmt.Sprintf("%s[%d]", key, i), container)
}

// namespaceSources are the sources of a container's environment that read an object of its namespace, where every
// run of the organization keeps its Secret: a job receives only the secrets its own run's Secret holds.
var namespaceSources = []string{"secretRef", "configMapRef", "secretKeyRef", "configMapKeyRef"}

// checkEnv checks that a container, which path names, takes no variable from a Secret or a ConfigMap of its
// namespace, through envFrom or an env variable's valueFrom.
func checkEnv(path string, container map[string]any) error {
	for _, list := range [][2]string{{"envFrom", ""}, {"env", "valueFrom"}} {
		entries, ok := container[list[0]].([]any)
		if container[list[0]] != nil && !ok {
			return fmt.Errorf("%s.%s must be a list", path, list[0])
		}
		for j, e := range entries {
			entry, _ := e.(map[string]any) // Kubernetes refuses what isn't one
			if list[1] != "" {
				entry, _ = entry[list[1]].(map[string]any) // a plain value reads nothing
			}
			for _, source := range namespaceSources {
				if _, ok := entry[source]; ok {
					return fmt.Errorf("%s.%s[%d] may not read the namespace's %s: a job receives the secrets it names", path, list[0], j, source)
				}
			}
		}
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
