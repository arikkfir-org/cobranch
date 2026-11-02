package rest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"cobranch.dev/internal/services/limits"
	"cobranch.dev/internal/services/repos"
)

// GetMyUsage answers how many organizations the viewer created for themselves against the most a user may create, or
// 304 while the caller holds that answer.
func (a api) GetMyUsage(ctx context.Context, req GetMyUsageRequestObject) (GetMyUsageResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	created, err := a.orgs.Created(ctx, v)
	if err != nil {
		return nil, fmt.Errorf("counting the organizations the viewer created: %w", err)
	}
	body := ViewerUsage{Organizations: quota(created)}
	tag, err := usageTag("viewer", body)
	if err != nil {
		return nil, err
	}
	if matches(req.Params.IfNoneMatch, tag) {
		return GetMyUsage304Response{Headers: GetMyUsage304ResponseHeaders{ETag: tag}}, nil
	}
	return GetMyUsage200JSONResponse{Body: body, Headers: GetMyUsage200ResponseHeaders{ETag: tag}}, nil
}

// GetOrganizationUsage answers what the organization takes of what its limits cap, with the Git data of each of its
// repositories the viewer reads, or 304 while the caller holds that answer.
func (a api) GetOrganizationUsage(ctx context.Context, req GetOrganizationUsageRequestObject) (GetOrganizationUsageResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	org, err := a.org(ctx, v, req.Org)
	if err != nil {
		return nil, err
	}
	usage, err := a.repos.Usage(ctx, v, org.ID)
	if err != nil {
		return nil, fail(err, "organization")
	}
	body := organizationUsage(usage)
	tag, err := usageTag("organization", body)
	if err != nil {
		return nil, err
	}
	if matches(req.Params.IfNoneMatch, tag) {
		return GetOrganizationUsage304Response{Headers: GetOrganizationUsage304ResponseHeaders{ETag: tag}}, nil
	}
	return GetOrganizationUsage200JSONResponse{Body: body, Headers: GetOrganizationUsage200ResponseHeaders{ETag: tag}}, nil
}

// quota is q as the API shows it.
func quota(q limits.Quota) Usage {
	return Usage{Used: q.Used, Limit: q.Limit}
}

// organizationUsage is u as the API shows it.
func organizationUsage(u repos.Usage) OrganizationUsage {
	return OrganizationUsage{Repositories: quota(u.Repositories), GitData: quota(u.GitData),
		ByRepository: mapAll(u.ByRepository, func(r repos.RepositoryUsage) RepositoryUsage {
			return RepositoryUsage{Id: r.Repository.ID, Slug: r.Repository.Slug, Name: r.Repository.Name, GitData: r.GitData}
		})}
}

// usageTag is the strong entity tag of a usage answer of kind: its body fixes it, and nothing cheaper than the
// counts themselves can.
func usageTag(kind string, body any) (string, error) {
	encoded, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("tagging the %s's usage: %w", kind, err)
	}
	sum := sha256.Sum256([]byte("usage\x00" + kind + "\x00" + string(encoded)))
	return `"` + hex.EncodeToString(sum[:16]) + `"`, nil
}
