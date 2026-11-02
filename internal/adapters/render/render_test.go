package render_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	utilyaml "k8s.io/apimachinery/pkg/util/yaml"

	"cobranch.dev/internal/adapters/render"
	"cobranch.dev/internal/services/delivery"
)

// files writes each file under dir, creating its directories.
func files(t *testing.T, dir string, contents map[string]string) {
	t.Helper()
	for name, content := range contents {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// objects decodes a render's stream back into its objects.
func objects(t *testing.T, stream []byte) []map[string]any {
	t.Helper()
	dec := utilyaml.NewYAMLOrJSONDecoder(bytes.NewReader(stream), 4096)
	var out []map[string]any
	for {
		var o map[string]any
		if err := dec.Decode(&o); errors.Is(err, io.EOF) {
			return out
		} else if err != nil {
			t.Fatalf("decoding the render: %v\n%s", err, stream)
		}
		out = append(out, o)
	}
}

// names is each object's kind/name, in order.
func names(objs []map[string]any) []string {
	out := make([]string, 0, len(objs))
	for _, o := range objs {
		meta, _ := o["metadata"].(map[string]any)
		out = append(out, o["kind"].(string)+"/"+meta["name"].(string))
	}
	return out
}

// field is the value at a dotted path under o.
func field(o map[string]any, path string) any {
	var v any = o
	for _, k := range strings.Split(path, ".") {
		m, _ := v.(map[string]any)
		v = m[k]
	}
	return v
}

var vars = map[string]string{"target": "previews", "branch": "feature/Login", "branch.slug": "feature-login",
	"commit": "3b7d0c4e9f0a", "commit.short": "3b7d0c4", "namespace": "shop-feature-login"}

// chart is a chart with a CRD, a hook, a vendored subchart and templates that show its values and capabilities.
var chart = map[string]string{
	"chart/Chart.yaml":           "apiVersion: v2\nname: shop\nversion: 0.1.0\ndependencies:\n  - name: cache\n    version: 0.1.0\n",
	"chart/values.yaml":          "replicas: 1\nimage:\n  repository: example/api\n  tag: latest\nhost: example.com\n",
	"chart/values-previews.yaml": "replicas: 2\nhost: ${branch.slug}.example.com\n",
	"chart/crds/widget.yaml":     "apiVersion: apiextensions.k8s.io/v1\nkind: CustomResourceDefinition\nmetadata:\n  name: widgets.example.com\n",
	"chart/templates/deployment.yaml": `apiVersion: apps/v1
kind: Deployment
metadata:
  name: {{ .Release.Name }}
  namespace: {{ .Release.Namespace }}
  annotations:
    kube: {{ .Capabilities.KubeVersion.Version | quote }}
    host: {{ .Values.host }}
    {{- if .Capabilities.APIVersions.Has "example.com/v1" }}
    widgets: "yes"
    {{- end }}
spec:
  replicas: {{ .Values.replicas }}
  template:
    spec:
      containers:
        - name: api
          image: {{ .Values.image.repository }}:{{ .Values.image.tag }}
`,
	"chart/templates/service.yaml":         "apiVersion: v1\nkind: Service\nmetadata:\n  name: {{ include \"shop.name\" . }}\n",
	"chart/templates/namespace.yaml":       "apiVersion: v1\nkind: Namespace\nmetadata:\n  name: {{ .Release.Namespace }}-extra\n",
	"chart/templates/hook.yaml":            "apiVersion: batch/v1\nkind: Job\nmetadata:\n  name: migrate\n  annotations:\n    helm.sh/hook: pre-install\n",
	"chart/templates/NOTES.txt":            "Installed {{ .Release.Name }}.\n",
	"chart/templates/_helpers.tpl":         "{{- define \"shop.name\" -}}{{ .Release.Name }}{{- end -}}\n",
	"chart/templates/optional.yaml":        "{{- if .Values.optional }}\napiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: optional\n{{- end }}\n",
	"chart/charts/cache/Chart.yaml":        "apiVersion: v2\nname: cache\nversion: 0.1.0\n",
	"chart/charts/cache/templates/cm.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cache\n",
}

var renderTests = []struct {
	name   string
	files  map[string]string
	roots  []delivery.PlannedRoot
	opts   render.Options
	want   []string
	fields map[string]any
}{
	{name: "plain manifests", files: map[string]string{
		"deploy/a.yaml":          "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: a\ndata:\n  branch: ${branch}\n---\n---\napiVersion: v1\nkind: Secret\nmetadata:\n  name: b\n",
		"deploy/b/c.json":        `{"apiVersion": "v1", "kind": "ServiceAccount", "metadata": {"name": "c"}}`,
		"deploy/b/d.yml":         "apiVersion: v1\nkind: List\nitems:\n  - apiVersion: v1\n    kind: ConfigMap\n    metadata:\n      name: d1\n  - apiVersion: v1\n    kind: ConfigMap\n    metadata:\n      name: d2\n",
		"deploy/README.md":       "not a manifest",
		"deploy/.hidden/x.yaml":  "not: even: yaml",
		"deploy/.skipped.yaml":   "not: even: yaml",
		"elsewhere/ignored.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: ignored\n",
	}, roots: []delivery.PlannedRoot{{Path: "deploy", Kind: delivery.Manifests}},
		want: []string{"ConfigMap/a", "Secret/b", "ServiceAccount/c", "ConfigMap/d1", "ConfigMap/d2"}, fields: map[string]any{"0:data.branch": "${branch}"}},
	{name: "plain manifests substituted", files: map[string]string{
		"deploy/a.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: a-${branch.slug}\ndata:\n  branch: ${branch}\n  literal: $${branch}\n",
	}, roots: []delivery.PlannedRoot{{Path: "deploy", Kind: delivery.Manifests, Substitute: true}},
		want: []string{"ConfigMap/a-feature-login"}, fields: map[string]any{"0:data.branch": "feature/Login", "0:data.literal": "${branch}"}},
	{name: "a chart", files: chart, roots: []delivery.PlannedRoot{{Path: "chart", Kind: delivery.Helm, Release: "shop",
		ValuesFiles: []string{"chart/values-previews.yaml"}, Values: map[string]any{"image": map[string]any{"tag": "3b7d0c4"}}, Substitute: true}},
		opts: render.Options{KubeVersion: "v1.33.4", APIVersions: []string{"example.com/v1"}},
		want: []string{"CustomResourceDefinition/widgets.example.com", "Namespace/shop-feature-login-extra", "ConfigMap/cache", "Service/shop", "Deployment/shop"},
		fields: map[string]any{
			"4:metadata.namespace":           "shop-feature-login",
			"4:metadata.annotations.kube":    "v1.33.4",
			"4:metadata.annotations.host":    "feature-login.example.com",
			"4:metadata.annotations.widgets": "yes",
			"4:spec.replicas":                float64(2),
		}},
	{name: "a chart's values files left as they are", files: chart, roots: []delivery.PlannedRoot{{Path: "chart", Kind: delivery.Helm, Release: "shop",
		ValuesFiles: []string{"chart/values-previews.yaml"}}},
		want:   []string{"CustomResourceDefinition/widgets.example.com", "Namespace/shop-feature-login-extra", "ConfigMap/cache", "Service/shop", "Deployment/shop"},
		fields: map[string]any{"4:metadata.annotations.host": "${branch.slug}.example.com", "4:metadata.annotations.widgets": nil}},
	{name: "a kustomization", files: map[string]string{
		"k/base/kustomization.yaml":    "resources: [cm.yaml]\n",
		"k/base/cm.yaml":               "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: settings\ndata:\n  mode: base\n  branch: ${branch}\n",
		"k/preview/kustomization.yaml": "resources: [../base]\nnamePrefix: preview-\npatches:\n  - path: mode.yaml\n",
		"k/preview/mode.yaml":          "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: settings\ndata:\n  mode: preview\n",
	}, roots: []delivery.PlannedRoot{{Path: "k/preview", Kind: delivery.Kustomize, Substitute: true}},
		want: []string{"ConfigMap/preview-settings"}, fields: map[string]any{"0:data.mode": "preview", "0:data.branch": "feature/Login"}},
	{name: "roots in order", files: map[string]string{
		"b/x.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: from-b\n",
		"a/x.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: from-a\n",
	}, roots: []delivery.PlannedRoot{{Path: "b", Kind: delivery.Manifests}, {Path: "a", Kind: delivery.Manifests}},
		want: []string{"ConfigMap/from-b", "ConfigMap/from-a"}},
	{name: "nothing to render", roots: nil, want: []string{}},
}

func TestRender(t *testing.T) {
	for _, tt := range renderTests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			files(t, dir, tt.files)
			plan := delivery.Plan{Namespace: vars["namespace"], Roots: tt.roots, Variables: vars}
			out, err := render.Render(context.Background(), dir, plan, tt.opts)
			if err != nil {
				t.Fatal(err)
			}
			objs := objects(t, out)
			if got := names(objs); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Render() = %v, want %v\n%s", got, tt.want, out)
			}
			for at, want := range tt.fields {
				i, path, _ := strings.Cut(at, ":")
				if got := field(objs[int(i[0]-'0')], path); !reflect.DeepEqual(got, want) {
					t.Errorf("object %s's %s = %#v, want %#v", i, path, got, want)
				}
			}
		})
	}
}

// refusals are renders that fail; a link's target outside/ is under a directory outside the checkout.
var refusals = []struct {
	name    string
	files   map[string]string
	links   map[string]string
	root    delivery.PlannedRoot
	opts    render.Options
	wantErr string
}{
	{name: "a missing directory", root: delivery.PlannedRoot{Path: "nope", Kind: delivery.Manifests},
		wantErr: "render failed: manifests root nope: nope: no such file or directory"},
	{name: "a file for a directory", files: map[string]string{"f.yaml": "a: b\n"}, root: delivery.PlannedRoot{Path: "f.yaml", Kind: delivery.Manifests},
		wantErr: "render failed: manifests root f.yaml: f.yaml is not a directory of the commit"},
	{name: "a root linked outside", links: map[string]string{"deploy": "outside"}, root: delivery.PlannedRoot{Path: "deploy", Kind: delivery.Manifests},
		wantErr: "render failed: manifests root deploy: deploy leads outside the repository"},
	{name: "a manifest linked outside", files: map[string]string{"deploy/ok.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: ok\n"},
		links: map[string]string{"deploy/x.yaml": "outside/secret.yaml"}, root: delivery.PlannedRoot{Path: "deploy", Kind: delivery.Manifests},
		wantErr: "render failed: manifests root deploy: reading deploy: deploy/x.yaml leads outside the repository"},
	{name: "a chart's template linked outside", files: map[string]string{"c/Chart.yaml": "apiVersion: v2\nname: c\nversion: 0.1.0\n"},
		links: map[string]string{"c/templates/x.yaml": "outside/secret.yaml"}, root: delivery.PlannedRoot{Path: "c", Kind: delivery.Helm, Release: "c"},
		wantErr: "render failed: helm root c: reading c: c/templates/x.yaml leads outside the repository"},
	{name: "a values file linked outside", files: map[string]string{"c/Chart.yaml": "apiVersion: v2\nname: c\nversion: 0.1.0\n"},
		links: map[string]string{"v.yaml": "outside/secret.yaml"}, root: delivery.PlannedRoot{Path: "c", Kind: delivery.Helm, Release: "c", ValuesFiles: []string{"v.yaml"}},
		wantErr: "render failed: helm root c: values file: v.yaml leads outside the repository"},
	{name: "a missing values file", files: map[string]string{"c/Chart.yaml": "apiVersion: v2\nname: c\nversion: 0.1.0\n"},
		root:    delivery.PlannedRoot{Path: "c", Kind: delivery.Helm, Release: "c", ValuesFiles: []string{"c/nope.yaml"}},
		wantErr: "render failed: helm root c: values file: c/nope.yaml: no such file or directory"},
	{name: "an object without a name", files: map[string]string{"d/a.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata: {}\n"},
		root: delivery.PlannedRoot{Path: "d", Kind: delivery.Manifests}, wantErr: "render failed: manifests root d: reading manifests: d/a.yaml: an object without metadata.name"},
	{name: "an object without a kind", files: map[string]string{"d/a.yaml": "apiVersion: v1\nmetadata:\n  name: a\n"},
		root: delivery.PlannedRoot{Path: "d", Kind: delivery.Manifests}, wantErr: "d/a.yaml: an object without kind"},
	{name: "a list of strings", files: map[string]string{"d/a.yaml": "apiVersion: v1\nkind: List\nitems: [a]\n"},
		root: delivery.PlannedRoot{Path: "d", Kind: delivery.Manifests}, wantErr: "d/a.yaml: a List's items must be objects"},
	{name: "not YAML", files: map[string]string{"d/a.yaml": "a: [\n"},
		root: delivery.PlannedRoot{Path: "d", Kind: delivery.Manifests}, wantErr: "render failed: manifests root d: reading manifests: d/a.yaml: "},
	{name: "a variable without a value", files: map[string]string{"d/a.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: a-${repository}\n"},
		root: delivery.PlannedRoot{Path: "d", Kind: delivery.Manifests, Substitute: true}, wantErr: "d/a.yaml: unknown variable ${repository}"},
	{name: "a dependency not vendored", files: map[string]string{"c/Chart.yaml": "apiVersion: v2\nname: c\nversion: 0.1.0\ndependencies:\n  - name: redis\n    version: 1.0.0\n    repository: https://charts.example.com\n"},
		root: delivery.PlannedRoot{Path: "c", Kind: delivery.Helm, Release: "c"}, wantErr: "dependency redis is not in the chart's charts/ directory"},
	{name: "a chart that fails", files: map[string]string{"c/Chart.yaml": "apiVersion: v2\nname: c\nversion: 0.1.0\n", "c/templates/x.yaml": "{{ fail \"no\" }}"},
		root: delivery.PlannedRoot{Path: "c", Kind: delivery.Helm, Release: "c"}, wantErr: "render failed: helm root c: rendering the chart: "},
	{name: "not a chart", files: map[string]string{"c/x.yaml": "a: b\n"},
		root: delivery.PlannedRoot{Path: "c", Kind: delivery.Helm, Release: "c"}, wantErr: "render failed: helm root c: loading the chart: "},
	{name: "a cluster version Helm can't read", files: map[string]string{"c/Chart.yaml": "apiVersion: v2\nname: c\nversion: 0.1.0\n"},
		root: delivery.PlannedRoot{Path: "c", Kind: delivery.Helm, Release: "c"}, opts: render.Options{KubeVersion: "latest"}, wantErr: `the cluster's version "latest"`},
	{name: "a chart for another Kubernetes", files: map[string]string{"c/Chart.yaml": "apiVersion: v2\nname: c\nversion: 0.1.0\nkubeVersion: \">= 1.40.0\"\n"},
		root: delivery.PlannedRoot{Path: "c", Kind: delivery.Helm, Release: "c"}, opts: render.Options{KubeVersion: "v1.33.4"},
		wantErr: "the chart requires Kubernetes >= 1.40.0, which v1.33.4 doesn't satisfy"},
	{name: "not a kustomization", files: map[string]string{"k/x.yaml": "a: b\n"},
		root: delivery.PlannedRoot{Path: "k", Kind: delivery.Kustomize}, wantErr: "render failed: kustomize root k: building the kustomization: "},
	{name: "a kustomization's variable without a value", files: map[string]string{"k/kustomization.yaml": "resources: [cm.yaml]\n",
		"k/cm.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: x\ndata:\n  a: ${repository}\n"},
		root: delivery.PlannedRoot{Path: "k", Kind: delivery.Kustomize, Substitute: true}, wantErr: "the kustomization's output: unknown variable ${repository}"},
	{name: "a kustomization reading outside its directory", files: map[string]string{"k/kustomization.yaml": "configMapGenerator:\n  - name: x\n    files: [../secret.txt]\n", "secret.txt": "s"},
		root: delivery.PlannedRoot{Path: "k", Kind: delivery.Kustomize}, wantErr: "render failed: kustomize root k: building the kustomization: "},
}

func TestRenderRefuses(t *testing.T) {
	outside := t.TempDir()
	files(t, outside, map[string]string{"secret.yaml": "apiVersion: v1\nkind: Secret\nmetadata:\n  name: stolen\n"})
	for _, tt := range refusals {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			files(t, dir, tt.files)
			for name, target := range tt.links {
				p := filepath.Join(dir, filepath.FromSlash(name))
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(outside, strings.TrimPrefix(target, "outside")), p); err != nil {
					t.Fatal(err)
				}
			}
			plan := delivery.Plan{Namespace: "n", Roots: []delivery.PlannedRoot{tt.root}, Variables: vars}
			out, err := render.Render(context.Background(), dir, plan, tt.opts)
			if !errors.Is(err, render.ErrRender) || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Render() = %v, %q, want ErrRender containing %q", err, out, tt.wantErr)
			}
		})
	}
}

func TestRenderTooLarge(t *testing.T) {
	dir := t.TempDir()
	files(t, dir, map[string]string{"d/big.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: big\ndata:\n  a: " + strings.Repeat("a", render.MaxBytes) + "\n"})
	plan := delivery.Plan{Namespace: "n", Roots: []delivery.PlannedRoot{{Path: "d", Kind: delivery.Manifests}}}
	if _, err := render.Render(context.Background(), dir, plan, render.Options{}); !errors.Is(err, render.ErrRender) || !strings.Contains(err.Error(), "larger than 16 MiB") {
		t.Fatalf("Render() = %v, want ErrRender for its size", err)
	}
}

func TestRenderMissingCheckout(t *testing.T) {
	_, err := render.Render(context.Background(), filepath.Join(t.TempDir(), "nope"), delivery.Plan{}, render.Options{})
	if !errors.Is(err, render.ErrRender) {
		t.Fatalf("Render() = %v, want ErrRender", err)
	}
}
