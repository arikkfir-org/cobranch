package render

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// renderManifests reads every .yaml, .yml and .json file under dir, in path order, skipping hidden files and
// directories, each one or more documents; vars, when set, are expanded in each file first.
func renderManifests(ctx context.Context, repo, dir string, vars map[string]string) ([]Object, error) {
	_, span := tracer.Start(ctx, "render.manifests", trace.WithAttributes(attribute.String("cobranch.render.dir", rel(repo, dir))))
	defer span.End()
	var out []Object
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p != dir && strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() || !isManifest(d.Name()) {
			return nil
		}
		objects, err := readManifest(repo, p, vars)
		if err != nil {
			return err
		}
		out = append(out, objects...)
		return nil
	})
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		span.RecordError(err)
		return nil, fmt.Errorf("reading manifests: %w", err)
	}
	return out, nil
}

// isManifest reports whether a file's name makes it a manifest.
func isManifest(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".yaml", ".yml", ".json":
		return true
	}
	return false
}

// readManifest reads one manifest file's objects.
func readManifest(repo, p string, vars map[string]string) ([]Object, error) {
	name := rel(repo, p)
	content, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	if content, err = substitute(content, vars, name); err != nil {
		return nil, err
	}
	return decode(bytes.NewReader(content), name)
}

// rel is p relative to repo, with forward slashes, for messages.
func rel(repo, p string) string {
	r, err := filepath.Rel(repo, p)
	if err != nil {
		return p
	}
	return filepath.ToSlash(r)
}
