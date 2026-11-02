package adminapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/admin"
)

// LimitChange is one of an organization's own limits in a merge patch: Set when the patch names it, to Value, or to
// none (the installation's again) when it names null. A patch that leaves it out leaves it unset.
type LimitChange struct {
	Set   bool
	Value *int64
}

// UnmarshalJSON reads a limit, or null, that the patch names.
func (c *LimitChange) UnmarshalJSON(b []byte) error {
	c.Set, c.Value = true, nil
	if bytes.Equal(bytes.TrimSpace(b), []byte("null")) {
		return nil
	}
	var n int64
	if err := json.Unmarshal(b, &n); err != nil {
		return fmt.Errorf("reading a limit: %w", err)
	}
	c.Value = &n
	return nil
}

// GetOrganizationLimits answers an organization's limits with their ETag, or 304 while If-None-Match names it.
func (a api) GetOrganizationLimits(ctx context.Context, req GetOrganizationLimitsRequestObject) (GetOrganizationLimitsResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	l, err := a.admin.OrganizationLimits(ctx, v, req.Org)
	if err != nil {
		return nil, fail(err, "organization")
	}
	tag := `"` + l.Fingerprint() + `"`
	if req.Params.IfNoneMatch != nil && matches(*req.Params.IfNoneMatch, tag) {
		return GetOrganizationLimits304Response{Headers: GetOrganizationLimits304ResponseHeaders{ETag: tag}}, nil
	}
	return GetOrganizationLimits200JSONResponse{Body: organizationLimits(l), Headers: GetOrganizationLimits200ResponseHeaders{ETag: tag}}, nil
}

// ChangeOrganizationLimits sets or clears the organization's own limits the body names, from those If-Match names:
// 428 without it, 412 once they changed.
func (a api) ChangeOrganizationLimits(ctx context.Context, req ChangeOrganizationLimitsRequestObject) (ChangeOrganizationLimitsResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	match, err := ifMatch(req.Params.IfMatch, "the limits'")
	if err != nil {
		return nil, err
	}
	change := admin.OwnLimitsChange{Repositories: admin.LimitChange(req.Body.Repositories), GitData: admin.LimitChange(req.Body.GitData)}
	l, err := a.admin.ChangeOrganizationLimits(ctx, v, req.Org, match, change)
	switch {
	case errors.Is(err, admin.ErrStale):
		return nil, refuse(http.StatusPreconditionFailed, "precondition.failed", "The limits changed since you read them: read them again.")
	case err != nil:
		return nil, fail(err, "organization")
	}
	return ChangeOrganizationLimits200JSONResponse{Body: organizationLimits(l),
		Headers: ChangeOrganizationLimits200ResponseHeaders{ETag: `"` + l.Fingerprint() + `"`}}, nil
}

func organizationLimits(l admin.OrganizationLimits) OrganizationLimits {
	applies := l.Applies()
	out := OrganizationLimits{OrgId: l.ID, Slug: l.Slug, Name: l.Name,
		Repositories: OrganizationLimit{Own: l.Own.Repositories, Installation: l.Installation.RepositoriesPerOrganization, Applies: applies.RepositoriesPerOrganization},
		GitData:      OrganizationLimit{Own: l.Own.GitData, Installation: l.Installation.GitDataPerOrganization, Applies: applies.GitDataPerOrganization}}
	if l.Own.Version > 0 {
		at := l.Own.UpdatedAt
		out.UpdatedAt = &at
	}
	if l.Own.UpdatedBy != uuid.Nil {
		by := l.Own.UpdatedBy
		out.UpdatedBy = &by
	}
	return out
}
