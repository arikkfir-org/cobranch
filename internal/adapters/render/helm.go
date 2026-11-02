package render

import (
	"bytes"
	"context"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	chartcommon "helm.sh/helm/v4/pkg/chart/common"
	commonutil "helm.sh/helm/v4/pkg/chart/common/util"
	chart "helm.sh/helm/v4/pkg/chart/v2"
	"helm.sh/helm/v4/pkg/chart/v2/loader"
	chartutil "helm.sh/helm/v4/pkg/chart/v2/util"
	"helm.sh/helm/v4/pkg/engine"
	releaseutil "helm.sh/helm/v4/pkg/release/v1/util"

	"cobranch.dev/internal/services/delivery"
)

// renderChart renders the chart at dir as `helm template` would install it: its CRDs first, then its templates in
// Helm's install order. Hooks are left out until delivery orders work in waves and hooks. Its values are the chart's
// own, then each values file in order, then the root's values; vars, when set, are expanded in each values file
// first. Its dependencies must be in its charts/ directory: rendering downloads nothing.
func renderChart(ctx context.Context, repo, dir string, root delivery.PlannedRoot, namespace string, vars map[string]string, opts Options) ([]Object, error) {
	_, span := tracer.Start(ctx, "render.chart", trace.WithAttributes(
		attribute.String("cobranch.render.dir", root.Path), attribute.String("cobranch.render.release", root.Release)))
	defer span.End()
	out, err := chartObjects(repo, dir, root, namespace, vars, opts)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		span.RecordError(err)
	}
	return out, err
}

// chartObjects is renderChart without its span.
func chartObjects(repo, dir string, root delivery.PlannedRoot, namespace string, vars map[string]string, opts Options) ([]Object, error) {
	chrt, err := loader.LoadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("loading the chart: %w", err)
	}
	if err := vendored(chrt); err != nil {
		return nil, err
	}
	values, err := chartValues(repo, root, vars)
	if err != nil {
		return nil, err
	}
	if err := chartutil.ProcessDependencies(chrt, values); err != nil {
		return nil, fmt.Errorf("the chart's dependencies: %w", err)
	}
	caps, err := capabilities(chrt, opts)
	if err != nil {
		return nil, err
	}
	release := chartcommon.ReleaseOptions{Name: root.Release, Namespace: namespace, Revision: 1, IsInstall: true}
	top, err := commonutil.ToRenderValues(chrt, values, release, caps)
	if err != nil {
		return nil, fmt.Errorf("the chart's values: %w", err)
	}
	var out []Object
	for _, crd := range chrt.CRDObjects() {
		objects, err := decode(bytes.NewReader(crd.File.Data), crd.Filename)
		if err != nil {
			return nil, err
		}
		out = append(out, objects...)
	}
	manifests, err := templates(chrt, top, caps)
	if err != nil {
		return nil, err
	}
	for _, m := range manifests {
		objects, err := decode(bytes.NewReader([]byte(m.Content)), m.Name)
		if err != nil {
			return nil, err
		}
		out = append(out, objects...)
	}
	return out, nil
}

// templates renders a chart's templates and sorts them in Helm's install order, leaving out its hooks and its
// NOTES.txt files, which aren't objects, as Helm's install does.
func templates(chrt *chart.Chart, top chartcommon.Values, caps *chartcommon.Capabilities) ([]releaseutil.Manifest, error) {
	files, err := engine.Render(chrt, top)
	if err != nil {
		return nil, fmt.Errorf("rendering the chart: %w", err)
	}
	for name := range files {
		if strings.HasSuffix(name, "NOTES.txt") {
			delete(files, name)
		}
	}
	_, manifests, err := releaseutil.SortManifests(files, caps.APIVersions, releaseutil.InstallOrder) // hooks left out
	if err != nil {
		return nil, fmt.Errorf("sorting the chart's objects: %w", err)
	}
	return manifests, nil
}

// vendored reports a dependency Chart.yaml lists that the chart's charts/ directory lacks.
func vendored(chrt *chart.Chart) error {
	if chrt.Metadata == nil {
		return nil
	}
	for _, dep := range chrt.Metadata.Dependencies {
		if !slices.ContainsFunc(chrt.Dependencies(), func(c *chart.Chart) bool { return c.Name() == dep.Name }) {
			return fmt.Errorf("dependency %s is not in the chart's charts/ directory: vendor it with `helm dependency build`", dep.Name)
		}
	}
	return nil
}

// chartValues merges a root's values files, in order, and then its values, each over what came before.
func chartValues(repo string, root delivery.PlannedRoot, vars map[string]string) (map[string]any, error) {
	values := map[string]any{}
	for _, f := range root.ValuesFiles {
		p, err := inside(repo, f)
		if err != nil {
			return nil, fmt.Errorf("values file: %w", err)
		}
		content, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("values file %s: %w", f, err)
		}
		if content, err = substitute(content, vars, f); err != nil {
			return nil, err
		}
		file, err := chartcommon.ReadValues(content)
		if err != nil {
			return nil, fmt.Errorf("values file %s: %w", f, err)
		}
		values = merge(values, file)
	}
	return merge(values, root.Values), nil
}

// merge lays over onto base, as Helm merges values files: mappings key by key, anything else replaced.
func merge(base, over map[string]any) map[string]any {
	out := maps.Clone(base)
	for k, v := range over {
		if vm, ok := v.(map[string]any); ok {
			if bm, ok := out[k].(map[string]any); ok {
				out[k] = merge(bm, vm)
				continue
			}
		}
		out[k] = v
	}
	return out
}

// capabilities are Helm's defaults, with the target cluster's version and API versions when opts names them. A chart
// whose kubeVersion that version doesn't satisfy is refused, as Helm's install refuses it.
func capabilities(chrt *chart.Chart, opts Options) (*chartcommon.Capabilities, error) {
	caps := chartcommon.DefaultCapabilities.Copy()
	if opts.KubeVersion != "" {
		v, err := chartcommon.ParseKubeVersion(opts.KubeVersion)
		if err != nil {
			return nil, fmt.Errorf("the cluster's version %q: %w", opts.KubeVersion, err)
		}
		caps.KubeVersion = *v
	}
	caps.APIVersions = append(slices.Clone(caps.APIVersions), opts.APIVersions...)
	if want := chrt.Metadata.KubeVersion; want != "" && !chartutil.IsCompatibleRange(want, caps.KubeVersion.String()) {
		return nil, fmt.Errorf("the chart requires Kubernetes %s, which %s doesn't satisfy", want, caps.KubeVersion.Version)
	}
	return caps, nil
}
