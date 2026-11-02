package rest

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/issues"
)

// Labels is what the API needs of the issues' service for the organization's labels.
type Labels interface {
	Labels(ctx context.Context, v issues.Viewer, orgID uuid.UUID) ([]issues.Label, error)
	Label(ctx context.Context, v issues.Viewer, orgID, labelID uuid.UUID) (issues.Label, error)
	CreateLabel(ctx context.Context, v issues.Viewer, orgID uuid.UUID, in issues.LabelInput) (issues.Label, error)
	UpdateLabel(ctx context.Context, v issues.Viewer, orgID, labelID uuid.UUID, p issues.LabelPatch) (issues.Label, error)
	DeleteLabel(ctx context.Context, v issues.Viewer, orgID, labelID uuid.UUID, version int64) error
}

// labelOf is l as the API shows it.
func labelOf(l issues.Label) Label {
	return Label{Id: l.ID, Name: l.Name, Color: LabelColor(l.Color), Description: l.Description, Issues: l.Issues, CreatedAt: l.CreatedAt.UTC(),
		UpdatedAt: l.UpdatedAt.UTC(), Version: l.Version}
}

// issueLabel is a label as an issue shows it.
func issueLabel(l issues.IssueLabel) IssueLabel {
	return IssueLabel{Id: l.ID, Name: l.Name, Color: LabelColor(l.Color)}
}

// labelTag is a label's ETag: its version, quoted.
func labelTag(l issues.Label) string {
	return `"` + strconv.FormatInt(l.Version, 10) + `"`
}

// labelMatch is the version of a label that If-Match names: 428 without one, 412 for one that names none.
func labelMatch(header *string) (int64, error) {
	if header == nil || strings.TrimSpace(*header) == "" {
		return 0, refuse(http.StatusPreconditionRequired, "precondition.required", "Name the label's ETag in If-Match.")
	}
	tag := strings.TrimSpace(*header)
	if tag == "*" {
		return 0, nil
	}
	version, err := strconv.ParseInt(strings.Trim(tag, `"`), 10, 64)
	if err != nil || version < 1 {
		return 0, refuse(http.StatusPreconditionFailed, "precondition.failed", "If-Match names no version of the label.")
	}
	return version, nil
}

// failLabel maps the labels' answers, then failIssue's.
func failLabel(err error) error {
	switch {
	case errors.Is(err, issues.ErrLabelNotFound):
		return refuse(http.StatusNotFound, "label.not_found", "There is no such label, or you may not see it.")
	case errors.Is(err, issues.ErrLabelChanged):
		return refuse(http.StatusPreconditionFailed, "precondition.failed", "The label changed since you read it: read it again.")
	case errors.Is(err, issues.ErrLabelTaken):
		return refuse(http.StatusConflict, "label.taken", "The organization has a label of that name.")
	}
	return failIssue(err)
}

// ListLabels lists the organization's labels.
func (a api) ListLabels(ctx context.Context, req ListLabelsRequestObject) (ListLabelsResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	org, err := a.org(ctx, v, req.Org)
	if err != nil {
		return nil, err
	}
	labels, err := a.issues.Labels(ctx, v, org.ID)
	if err != nil {
		return nil, failLabel(err)
	}
	return ListLabels200JSONResponse{Labels: mapAll(labels, labelOf)}, nil
}

// CreateLabel creates a label in the organization.
func (a api) CreateLabel(ctx context.Context, req CreateLabelRequestObject) (CreateLabelResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	org, err := a.org(ctx, v, req.Org)
	if err != nil {
		return nil, err
	}
	in := issues.LabelInput{Name: req.Body.Name}
	if req.Body.Color != nil {
		in.Color = issues.Color(*req.Body.Color)
	}
	if req.Body.Description != nil {
		in.Description = *req.Body.Description
	}
	created, err := a.issues.CreateLabel(ctx, v, org.ID, in)
	if err != nil {
		return nil, failLabel(err)
	}
	location := BaseURL + "/orgs/" + url.PathEscape(org.Slug) + "/labels/" + created.ID.String()
	return CreateLabel201JSONResponse{Body: labelOf(created), Headers: CreateLabel201ResponseHeaders{ETag: labelTag(created), Location: location}}, nil
}

// GetLabel answers one of the organization's labels, or 304 while the caller holds it.
func (a api) GetLabel(ctx context.Context, req GetLabelRequestObject) (GetLabelResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	org, err := a.org(ctx, v, req.Org)
	if err != nil {
		return nil, err
	}
	label, err := a.issues.Label(ctx, v, org.ID, req.LabelID)
	if err != nil {
		return nil, failLabel(err)
	}
	tag := labelTag(label)
	if matches(req.Params.IfNoneMatch, tag) {
		return GetLabel304Response{Headers: GetLabel304ResponseHeaders{ETag: tag}}, nil
	}
	return GetLabel200JSONResponse{Body: labelOf(label), Headers: GetLabel200ResponseHeaders{ETag: tag}}, nil
}

// UpdateLabel changes the fields the merge patch names.
func (a api) UpdateLabel(ctx context.Context, req UpdateLabelRequestObject) (UpdateLabelResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	version, err := labelMatch(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	org, err := a.org(ctx, v, req.Org)
	if err != nil {
		return nil, err
	}
	p := issues.LabelPatch{Name: req.Body.Name, Description: req.Body.Description, Version: version}
	if req.Body.Color != nil {
		c := issues.Color(*req.Body.Color)
		p.Color = &c
	}
	updated, err := a.issues.UpdateLabel(ctx, v, org.ID, req.LabelID, p)
	if err != nil {
		return nil, failLabel(err)
	}
	return UpdateLabel200JSONResponse{Body: labelOf(updated), Headers: UpdateLabel200ResponseHeaders{ETag: labelTag(updated)}}, nil
}

// DeleteLabel deletes one of the organization's labels.
func (a api) DeleteLabel(ctx context.Context, req DeleteLabelRequestObject) (DeleteLabelResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	version, err := labelMatch(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	org, err := a.org(ctx, v, req.Org)
	if err != nil {
		return nil, err
	}
	if err := a.issues.DeleteLabel(ctx, v, org.ID, req.LabelID, version); err != nil {
		return nil, failLabel(err)
	}
	return DeleteLabel204Response{}, nil
}
