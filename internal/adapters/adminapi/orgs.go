package adminapi

import (
	"context"
	"strings"

	"cobranch.dev/internal/services/admin"
)

// ListOrganizations answers a page of the installation's organizations, newest first, and the Link of the next.
func (a api) ListOrganizations(ctx context.Context, req ListOrganizationsRequestObject) (ListOrganizationsResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	p, err := page(req.Params.Q, req.Params.Cursor, req.Params.Limit)
	if err != nil {
		return nil, err
	}
	list, err := a.admin.Organizations(ctx, v, p)
	if err != nil {
		return nil, fail(err, "organization")
	}
	var resp ListOrganizations200JSONResponse
	resp.Body.Organizations = make([]Organization, 0, len(list.Items))
	for _, o := range list.Items {
		resp.Body.Organizations = append(resp.Body.Organizations, organization(o))
	}
	resp.Headers.Link = next(p, list.Next)
	return resp, nil
}

// CreateOrganization creates an organization for the user who holds the body's owner email.
func (a api) CreateOrganization(ctx context.Context, req CreateOrganizationRequestObject) (CreateOrganizationResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	o, err := a.admin.CreateOrganization(ctx, v, req.Body.Slug, req.Body.Name, req.Body.OwnerEmail)
	if err != nil {
		return nil, fail(err, "organization")
	}
	return CreateOrganization201JSONResponse(organization(o)), nil
}

// EnterOrganization makes the administrator an Owner of the organization the path names, for the body's reason.
func (a api) EnterOrganization(ctx context.Context, req EnterOrganizationRequestObject) (EnterOrganizationResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	o, err := a.admin.Enter(ctx, v, req.Org, req.Body.Reason)
	if err != nil {
		return nil, fail(err, "organization")
	}
	return EnterOrganization201JSONResponse{OrgId: o.ID, Slug: o.Slug, Name: o.Name, Reason: strings.TrimSpace(req.Body.Reason)}, nil
}

// ListUsers answers a page of the installation's users, newest first, and the Link of the next.
func (a api) ListUsers(ctx context.Context, req ListUsersRequestObject) (ListUsersResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	p, err := page(req.Params.Q, req.Params.Cursor, req.Params.Limit)
	if err != nil {
		return nil, err
	}
	list, err := a.admin.Users(ctx, v, p)
	if err != nil {
		return nil, fail(err, "user")
	}
	var resp ListUsers200JSONResponse
	resp.Body.Users = make([]User, 0, len(list.Items))
	for _, u := range list.Items {
		resp.Body.Users = append(resp.Body.Users, User{
			Id: u.ID, DisplayName: u.DisplayName, Emails: emails(u.Emails), Issuers: nonNil(u.Issuers), Roles: nonNil(u.Roles),
			Organizations: u.Organizations, Suspended: u.Suspended, CreatedAt: u.CreatedAt, LastSignInAt: u.LastSignInAt,
		})
	}
	resp.Headers.Link = next(p, list.Next)
	return resp, nil
}

func organization(o admin.Organization) Organization {
	return Organization{Id: o.ID, Slug: o.Slug, Name: o.Name, CreatedAt: o.CreatedAt, Members: o.Members}
}
