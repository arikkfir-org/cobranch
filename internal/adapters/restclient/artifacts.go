package restclient

import (
	"context"
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/harness"
)

// CreateArtifact makes an artifact of the organization with its first version, both named a.ID, attached to a.Repo or
// to none, and answers it; making it again answers it as it is (harness.Cobranch).
func (c *API) CreateArtifact(ctx context.Context, org string, a harness.NewArtifact) (harness.Artifact, error) {
	in := NewArtifact{Id: &a.ID, Title: a.Title, Kind: ArtifactKind(a.Kind), MediaType: ArtifactMediaType(a.Format), Body: a.Body}
	if a.Summary != "" {
		in.Summary = &a.Summary
	}
	if a.Note != "" {
		in.Note = &a.Note
	}
	if a.Branch != "" {
		in.Branch = &a.Branch
	}
	var (
		made   *Artifact
		resp   *http.Response
		status int
		body   []byte
	)
	if a.Repo == "" {
		r, err := c.api.CreateOrganizationArtifactWithResponse(ctx, org, in)
		if err != nil {
			return harness.Artifact{}, fmt.Errorf("writing artifact %s: %w", a.ID, err)
		}
		made, resp, status, body = either(r.JSON201, r.JSON200), r.HTTPResponse, r.StatusCode(), r.Body
	} else {
		r, err := c.api.CreateArtifactWithResponse(ctx, org, a.Repo, in)
		if err != nil {
			return harness.Artifact{}, fmt.Errorf("writing artifact %s: %w", a.ID, err)
		}
		made, resp, status, body = either(r.JSON201, r.JSON200), r.HTTPResponse, r.StatusCode(), r.Body
	}
	if made == nil {
		return harness.Artifact{}, refused("writing artifact "+a.ID.String(), status, body)
	}
	return artifact(*made, resp.Header.Get("ETag")), nil
}

// Artifact reads one of the organization's artifacts with its version number n, 0 for its newest (harness.Cobranch).
func (c *API) Artifact(ctx context.Context, org string, id uuid.UUID, n int) (harness.Artifact, error) {
	r, err := c.api.GetArtifactWithResponse(ctx, org, id, nil)
	if err != nil {
		return harness.Artifact{}, fmt.Errorf("reading artifact %s: %w", id, err)
	}
	if r.JSON200 == nil {
		return harness.Artifact{}, refused("reading artifact "+id.String(), r.StatusCode(), r.Body)
	}
	out := artifact(*r.JSON200, r.HTTPResponse.Header.Get("ETag"))
	v, err := c.api.GetArtifactVersionWithResponse(ctx, org, id, int32(n), nil) //nolint:gosec // a version's number fits
	if err != nil {
		return harness.Artifact{}, fmt.Errorf("reading version %d of artifact %s: %w", n, id, err)
	}
	if v.JSON200 == nil {
		return harness.Artifact{}, refused(fmt.Sprintf("reading version %d of artifact %s", n, id), v.StatusCode(), v.Body)
	}
	out.Version = artifactVersion(*v.JSON200)
	return out, nil
}

// AddArtifactVersion adds the next version of one of the organization's artifacts, made from the artifact as etag
// names it, and answers its number and the artifact's ETag now; adding it again answers it as it is
// (harness.Cobranch).
func (c *API) AddArtifactVersion(ctx context.Context, org string, id uuid.UUID, etag string, v harness.ArtifactVersion) (int, string, error) {
	in := NewArtifactVersion{Id: &v.ID, MediaType: ArtifactMediaType(v.Format), Body: v.Body}
	if v.Note != "" {
		in.Note = &v.Note
	}
	r, err := c.api.AddArtifactVersionWithResponse(ctx, org, id, &AddArtifactVersionParams{IfMatch: &etag}, in)
	if err != nil {
		return 0, "", fmt.Errorf("adding a version to artifact %s: %w", id, err)
	}
	added := either(r.JSON201, r.JSON200)
	switch {
	case r.StatusCode() == http.StatusPreconditionFailed:
		return 0, "", fmt.Errorf("adding a version to artifact %s: %w", id, harness.ErrArtifactChanged)
	case added == nil:
		return 0, "", refused("adding a version to artifact "+id.String(), r.StatusCode(), r.Body)
	}
	return int(added.Number), r.HTTPResponse.Header.Get("ETag"), nil
}

// either is created, or else found: the 201 of a call that made something, or the 200 of one that found it made.
func either[T any](created, found *T) *T {
	if created != nil {
		return created
	}
	return found
}

// artifact is a as the harness reads it, with its ETag.
func artifact(a Artifact, etag string) harness.Artifact {
	out := harness.Artifact{ID: a.Id, Title: a.Title, Kind: string(a.Kind), Head: int(deref(a.Head, 0)), ETag: etag}
	if a.Repository != nil {
		out.Repo = a.Repository.Slug
	}
	return out
}

// artifactVersion is v as the harness reads it.
func artifactVersion(v ArtifactVersion) harness.ArtifactVersion {
	return harness.ArtifactVersion{ID: v.Id, Number: int(v.Number), Format: string(v.MediaType), Body: deref(v.Body, ""), Note: v.Note}
}
