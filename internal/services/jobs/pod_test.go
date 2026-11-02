package jobs_test

import (
	"encoding/json"
	"testing"

	"cobranch.dev/internal/services/jobs"
)

func TestCheckPod(t *testing.T) {
	tests := []struct {
		name, spec, wantErr string
	}{
		{name: "one container", spec: `{"containers":[{"name":"a","image":"b"}]}`},
		{name: "scratch volumes and an init container", spec: `{"initContainers":[{"name":"i","image":"b"}],"containers":[{"name":"a","image":"b",
			"securityContext":{"privileged":false}}],"volumes":[{"name":"tmp","emptyDir":{}},{"name":"e","ephemeral":{}}]}`},
		{name: "no containers", spec: `{}`, wantErr: "a job's pod needs containers, a list of at least one"},
		{name: "a ServiceAccount", spec: `{"serviceAccountName":"x","containers":[{"name":"a","image":"b"}]}`, wantErr: "a job's pod may not set serviceAccountName"},
		{name: "image pull secrets", spec: `{"imagePullSecrets":[{"name":"registry"}],"containers":[{"name":"a","image":"b"}]}`,
			wantErr: "a job's pod may not set imagePullSecrets"},
		{name: "a node", spec: `{"nodeName":"n","containers":[{"name":"a","image":"b"}]}`, wantErr: "a job's pod may not set nodeName"},
		{name: "tolerations", spec: `{"tolerations":[],"containers":[{"name":"a","image":"b"}]}`, wantErr: "a job's pod may not set tolerations"},
		{name: "a nameless container", spec: `{"containers":[{"image":"b"}]}`, wantErr: "containers[0] needs a name"},
		{name: "an imageless init container", spec: `{"initContainers":[{"name":"i"}],"containers":[{"name":"a","image":"b"}]}`,
			wantErr: "initContainers[0] needs an image"},
		{name: "a privileged container", spec: `{"containers":[{"name":"a","image":"b","securityContext":{"privileged":true}}]}`,
			wantErr: "containers[0] may not be privileged"},
		{name: "a host path", spec: `{"containers":[{"name":"a","image":"b"}],"volumes":[{"name":"h","hostPath":{"path":"/"}}]}`,
			wantErr: "volumes[0] may only be emptyDir or ephemeral, not hostPath"},
		{name: "a Secret volume", spec: `{"containers":[{"name":"a","image":"b"}],"volumes":[{"name":"s","secret":{}}]}`,
			wantErr: "volumes[0] may only be emptyDir or ephemeral, not secret"},
		{name: "plain variables and the pod's own fields", spec: `{"containers":[{"name":"a","image":"b","env":[{"name":"X","value":"1"},
			{"name":"N","valueFrom":{"fieldRef":{"fieldPath":"metadata.name"}}}]}]}`},
		{name: "a Secret's variables", spec: `{"containers":[{"name":"a","image":"b","envFrom":[{"secretRef":{"name":"other-run"}}]}]}`,
			wantErr: "containers[0].envFrom[0] may not read the namespace's secretRef: a job receives the secrets it names"},
		{name: "a ConfigMap's variables in an init container", spec: `{"initContainers":[{"name":"i","image":"b","envFrom":[{"prefix":"P_"},
			{"configMapRef":{"name":"c"}}]}],"containers":[{"name":"a","image":"b"}]}`,
			wantErr: "initContainers[0].envFrom[1] may not read the namespace's configMapRef: a job receives the secrets it names"},
		{name: "a variable from a Secret's key", spec: `{"containers":[{"name":"a","image":"b","env":[{"name":"T","valueFrom":{"secretKeyRef":{"name":"other-run","key":"token"}}}]}]}`,
			wantErr: "containers[0].env[0] may not read the namespace's secretKeyRef: a job receives the secrets it names"},
		{name: "a variable from a ConfigMap's key", spec: `{"containers":[{"name":"a","image":"b","env":[{"name":"C","valueFrom":{"configMapKeyRef":{"name":"c","key":"k"}}}]}]}`,
			wantErr: "containers[0].env[0] may not read the namespace's configMapKeyRef: a job receives the secrets it names"},
		{name: "env that isn't a list", spec: `{"containers":[{"name":"a","image":"b","env":{}}]}`, wantErr: "containers[0].env must be a list"},
		{name: "ephemeral containers", spec: `{"containers":[{"name":"a","image":"b"}],"ephemeralContainers":[]}`,
			wantErr: "a job's pod may not set ephemeralContainers"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var spec map[string]any
			if err := json.Unmarshal([]byte(tt.spec), &spec); err != nil {
				t.Fatal(err)
			}
			err := jobs.CheckPod(spec)
			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("CheckPod() = %v", err)
			case tt.wantErr != "" && (err == nil || err.Error() != tt.wantErr):
				t.Fatalf("CheckPod() = %v, want %q", err, tt.wantErr)
			}
		})
	}
}
