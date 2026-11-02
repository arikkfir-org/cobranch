package render

import (
	"bytes"
	"context"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"sigs.k8s.io/kustomize/api/krusty"
	"sigs.k8s.io/kustomize/kyaml/filesys"
)

// renderKustomization builds the kustomization at dir as `kustomize build` would, with its default options: no
// plugins, no Helm charts inflated, and files only from the kustomization's own directory, while its resources may
// name other kustomizations and remote bases. vars, when set, are expanded in what it builds.
func renderKustomization(ctx context.Context, dir string, vars map[string]string) ([]Object, error) {
	_, span := tracer.Start(ctx, "render.kustomization", trace.WithAttributes(attribute.String("cobranch.render.dir", dir)))
	defer span.End()
	out, err := kustomization(dir, vars)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		span.RecordError(err)
	}
	return out, err
}

// kustomization is renderKustomization without its span.
func kustomization(dir string, vars map[string]string) ([]Object, error) {
	resources, err := krusty.MakeKustomizer(krusty.MakeDefaultOptions()).Run(filesys.MakeFsOnDisk(), dir)
	if err != nil {
		return nil, fmt.Errorf("building the kustomization: %w", err)
	}
	built, err := resources.AsYaml()
	if err != nil {
		return nil, fmt.Errorf("encoding the kustomization: %w", err)
	}
	if built, err = substitute(built, vars, "the kustomization's output"); err != nil {
		return nil, err
	}
	return decode(bytes.NewReader(built), "the kustomization")
}
