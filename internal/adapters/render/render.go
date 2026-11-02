// Package render renders a delivery's planned roots, from a repository checked out on disk, into the Kubernetes
// objects the controller applies: Helm charts as `helm template` would, kustomizations as `kustomize build` would,
// with no plugins, and plain manifests as they are. It runs in a delivery's render pod, which `cobranch render`
// starts, and on a developer's checkout, never in a workload that holds a cluster's credentials
// (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/delivery.md, "Roots and rendering").
package render

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/yaml"

	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/delivery"
)

var tracer = otel.Tracer("cobranch.dev/internal/adapters/render")

// MaxBytes is the largest render a delivery keeps: the YAML stream of all its roots' objects.
const MaxBytes = 16 << 20

// ErrRender is a root that doesn't render: a missing directory or file, a chart or kustomization that fails, or an
// object that isn't one.
var ErrRender = errors.New("render failed")

// Options are what rendering takes from the target cluster: its Kubernetes version, such as v1.33.4, and the API
// versions it serves, beside those every cluster does, so that charts checking .Capabilities render as they would
// install there. Empty, Helm's defaults stand.
type Options struct {
	KubeVersion string
	APIVersions []string
}

// Object is one Kubernetes object a root renders, as its JSON decodes.
type Object = map[string]any

// Render renders plan's roots from the repository checked out at dir, in order, and returns their objects as one
// YAML stream, each object a document. A render larger than MaxBytes is ErrRender.
func Render(ctx context.Context, dir string, plan delivery.Plan, opts Options) ([]byte, error) {
	ctx, span := tracer.Start(ctx, "render.Render", trace.WithAttributes(
		attribute.String("cobranch.delivery.namespace", plan.Namespace), attribute.Int("cobranch.delivery.roots", len(plan.Roots))))
	defer span.End()
	out, err := render(ctx, dir, plan, opts)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		span.RecordError(err)
	}
	return out, err
}

// render is Render without its span.
func render(ctx context.Context, dir string, plan delivery.Plan, opts Options) ([]byte, error) {
	repo, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return nil, fmt.Errorf("%w: the checkout %s: %w", ErrRender, dir, err)
	}
	var buf bytes.Buffer
	for _, root := range plan.Roots {
		objects, err := renderRoot(ctx, repo, root, plan, opts)
		if err != nil {
			return nil, fmt.Errorf("%w: %s root %s: %w", ErrRender, root.Kind, root.Path, err)
		}
		for _, o := range objects {
			doc, err := yaml.Marshal(o)
			if err != nil {
				return nil, fmt.Errorf("%w: %s root %s: encoding %s: %w", ErrRender, root.Kind, root.Path, describe(o), err)
			}
			buf.WriteString("---\n")
			buf.Write(doc)
			if buf.Len() > MaxBytes {
				return nil, fmt.Errorf("%w: the render is larger than %d MiB", ErrRender, MaxBytes>>20)
			}
		}
	}
	return buf.Bytes(), nil
}

// renderRoot renders one root, by its kind, into its objects.
func renderRoot(ctx context.Context, repo string, root delivery.PlannedRoot, plan delivery.Plan, opts Options) ([]Object, error) {
	dir, err := inside(repo, root.Path)
	if err != nil {
		return nil, err
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory of the commit", root.Path)
	}
	if err := confined(repo, dir); err != nil {
		return nil, err
	}
	var vars map[string]string
	if root.Substitute {
		vars = plan.Variables
	}
	switch root.Kind {
	case delivery.Helm:
		return renderChart(ctx, repo, dir, root, plan.Namespace, vars, opts)
	case delivery.Kustomize:
		return renderKustomization(ctx, dir, vars)
	default:
		return renderManifests(ctx, repo, dir, vars)
	}
}

// inside is rel's path under repo once its links are followed, or an error when they lead outside repo.
func inside(repo, rel string) (string, error) {
	p, err := filepath.EvalSymlinks(filepath.Join(repo, filepath.FromSlash(rel)))
	if err != nil {
		return "", fmt.Errorf("%s: %w", rel, errors.Unwrap(err))
	}
	if p != repo && !strings.HasPrefix(p, repo+string(filepath.Separator)) {
		return "", fmt.Errorf("%s leads outside the repository", rel)
	}
	return p, nil
}

// confined reports a link under dir that leads outside repo: Helm's loader follows links, and a chart or a
// kustomization must not read what the commit doesn't hold.
func confined(repo, dir string) error {
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&fs.ModeSymlink != 0 {
			_, err := inside(repo, rel(repo, p))
			return err
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("reading %s: %w", rel(repo, dir), err)
	}
	return nil
}

// decode reads every object of a stream of YAML or JSON documents, skipping empty ones, expanding Lists into their
// items, and checking that each names its apiVersion, kind and name.
func decode(r io.Reader, source string) ([]Object, error) {
	dec := utilyaml.NewYAMLOrJSONDecoder(r, 4096)
	var out []Object
	for {
		var o Object
		if err := dec.Decode(&o); errors.Is(err, io.EOF) {
			return out, nil
		} else if err != nil {
			return nil, fmt.Errorf("%s: %w", source, err)
		}
		if len(o) == 0 {
			continue
		}
		objects, err := expandList(o, source)
		if err != nil {
			return nil, err
		}
		out = append(out, objects...)
	}
}

// expandList is o's items when o is a List, or o itself, each checked to be an object.
func expandList(o Object, source string) ([]Object, error) {
	items, isList := o["items"].([]any)
	if kind, _ := o["kind"].(string); !isList || !strings.HasSuffix(kind, "List") { // a kind that isn't a string is caught by check
		return []Object{o}, check(o, source)
	}
	out := make([]Object, 0, len(items))
	for _, item := range items {
		obj, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s: a %s's items must be objects", source, o["kind"])
		}
		if err := check(obj, source); err != nil {
			return nil, err
		}
		out = append(out, obj)
	}
	return out, nil
}

// check reports an object without an apiVersion, a kind or a name.
func check(o Object, source string) error {
	meta, _ := o["metadata"].(map[string]any) // nil when missing, which the name's check reports
	for _, f := range []struct {
		name  string
		value any
	}{{"apiVersion", o["apiVersion"]}, {"kind", o["kind"]}, {"metadata.name", meta["name"]}} {
		if s, ok := f.value.(string); !ok || s == "" {
			return fmt.Errorf("%s: an object without %s", source, f.name)
		}
	}
	return nil
}

// describe names an object as kubectl does, kind/name, for messages.
func describe(o Object) string {
	meta, _ := o["metadata"].(map[string]any) // check made sure there is one
	return fmt.Sprintf("%v/%v", o["kind"], meta["name"])
}

// substitute expands vars in content, a root's file, when the root substitutes; source names the file in an error.
func substitute(content []byte, vars map[string]string, source string) ([]byte, error) {
	if vars == nil {
		return content, nil
	}
	out, err := code.Expand(string(content), vars)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", source, err)
	}
	return []byte(out), nil
}
