package cluster

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// with is u with value set at the dotted fields.
func with(u *unstructured.Unstructured, value any, fields ...string) *unstructured.Unstructured {
	if err := unstructured.SetNestedField(u.Object, value, fields...); err != nil {
		panic(err)
	}
	return u
}

// deployment is a Deployment of generation 1 whose status is status.
func deployment(status map[string]any) *unstructured.Unstructured {
	u := with(obj("apps/v1", "Deployment", "shop", "api"), int64(1), "spec", "replicas")
	u.SetGeneration(1)
	return with(u, status, "status")
}

var healthTests = []struct {
	name string
	u    *unstructured.Unstructured
	want Health
}{
	{name: "an object without a status", u: obj("v1", "ConfigMap", "shop", "a"), want: Healthy},
	{name: "a Deployment rolled out", u: deployment(map[string]any{"observedGeneration": int64(1), "replicas": int64(1), "updatedReplicas": int64(1),
		"readyReplicas": int64(1), "availableReplicas": int64(1), "conditions": []any{
			map[string]any{"type": "Available", "status": "True"},
			map[string]any{"type": "Progressing", "status": "True", "reason": "NewReplicaSetAvailable"}}}), want: Healthy},
	{name: "a Deployment rolling out", u: deployment(map[string]any{"observedGeneration": int64(1), "replicas": int64(1), "updatedReplicas": int64(0)}),
		want: Progressing},
	{name: "a Deployment its controller hasn't seen", u: deployment(map[string]any{"observedGeneration": int64(0)}), want: Progressing},
	{name: "a Deployment past its deadline", u: deployment(map[string]any{"observedGeneration": int64(1), "conditions": []any{
		map[string]any{"type": "Progressing", "status": "False", "reason": "ProgressDeadlineExceeded"}}}), want: Degraded},
	{name: "an object being deleted", u: with(obj("v1", "ConfigMap", "shop", "a"), "2026-11-02T00:00:00Z", "metadata", "deletionTimestamp"), want: Progressing},
	{name: "a resource not ready", u: with(obj("example.com/v1", "Widget", "shop", "w"), []any{map[string]any{"type": "Ready", "status": "False"}}, "status", "conditions"),
		want: Progressing},
	{name: "a resource stalled", u: with(obj("example.com/v1", "Widget", "shop", "w"), []any{map[string]any{"type": "Stalled", "status": "True", "message": "no quota"}}, "status", "conditions"),
		want: Degraded},
	{name: "a Job that failed", u: with(obj("batch/v1", "Job", "shop", "migrate"), []any{map[string]any{"type": "Failed", "status": "True"}}, "status", "conditions"),
		want: Degraded},
}

func TestHealthOf(t *testing.T) {
	for _, tt := range healthTests {
		t.Run(tt.name, func(t *testing.T) {
			if got, message := healthOf(tt.u); got != tt.want {
				t.Errorf("healthOf() = %s (%s), want %s", got, message, tt.want)
			}
		})
	}
}

func TestWorst(t *testing.T) {
	tests := []struct {
		hs   []Health
		want Health
	}{
		{nil, Healthy},
		{[]Health{Healthy, Healthy}, Healthy},
		{[]Health{Healthy, Progressing}, Progressing},
		{[]Health{Unknown, Progressing}, Unknown},
		{[]Health{Missing, Unknown, Healthy}, Missing},
		{[]Health{Degraded, Missing}, Degraded},
	}
	for _, tt := range tests {
		if got := Worst(tt.hs...); got != tt.want {
			t.Errorf("Worst(%v) = %s, want %s", tt.hs, got, tt.want)
		}
	}
}

func TestDecode(t *testing.T) {
	got, err := Decode([]byte("---\napiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: a\n---\n---\napiVersion: v1\nkind: Secret\nmetadata:\n  name: b\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].GetKind() != "ConfigMap" || got[1].GetName() != "b" {
		t.Errorf("Decode() = %v", got)
	}
	if _, err := Decode([]byte("---\na: [\n")); err == nil {
		t.Error("Decode() of a broken stream succeeded")
	}
	if got, err := Decode(nil); err != nil || got != nil {
		t.Errorf("Decode(nil) = %v, %v", got, err)
	}
}
