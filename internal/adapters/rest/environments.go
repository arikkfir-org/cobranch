package rest

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"cobranch.dev/internal/services/code"
)

// environment is e as the API shows it.
func environment(e code.Environment) Environment {
	out := Environment{Id: e.ID, Target: Principal{Id: e.Target.ID, DisplayName: e.Target.DisplayName}, Branch: e.Branch,
		Cluster: e.Cluster, Namespace: e.Namespace, SyncedAt: utc(e.SyncedAt), Sync: EnvironmentSync(e.Sync),
		Health: EnvironmentHealth(e.Health), Paused: e.Paused, CreatedAt: e.CreatedAt.UTC(), Version: e.Version}
	if e.Revision != "" {
		out.Revision = &e.Revision
	}
	if e.Latest != nil {
		latest := delivery(*e.Latest)
		out.Latest = &latest
	}
	return out
}

// delivery is d as the API shows it.
func delivery(d code.Delivery) Delivery {
	out := Delivery{Id: d.ID, Number: d.Number, Environment: d.EnvironmentID, Target: d.Target, Branch: d.Branch,
		Kind: DeliveryKind(d.Kind), Trigger: DeliveryTrigger(d.Trigger), Push: d.PushID, State: DeliveryState(d.State),
		Reason: d.Reason, StartedBy: principal(d.StartedBy), QueuedAt: d.QueuedAt.UTC(), StartedAt: utc(d.StartedAt),
		EndedAt: utc(d.EndedAt), Version: d.Version}
	if d.SHA != "" {
		out.Sha = &d.SHA
	}
	return out
}

// taggedEnvironment is e as the API shows it, with its ETag, which digests how it reads: its newest delivery, its
// target's name and its revision change without its version.
func taggedEnvironment(e code.Environment) (Environment, string, error) {
	body := environment(e)
	tag, err := versionTag(e.Version, body)
	return body, tag, err
}

// taggedDelivery is d as the API shows it, with its ETag, which digests how it reads: the names of its target and of
// who started it change without its version.
func taggedDelivery(d code.Delivery) (Delivery, string, error) {
	body := delivery(d)
	tag, err := versionTag(d.Version, body)
	return body, tag, err
}

// deliveryLocation is the URL of the repository's delivery numbered number.
func deliveryLocation(org, repo string, number int64) string {
	return "/v1/orgs/" + url.PathEscape(org) + "/repos/" + url.PathEscape(repo) + "/deliveries/" + strconv.FormatInt(number, 10)
}

// ListEnvironments lists the repository's environments, of one branch when the query names it.
func (a api) ListEnvironments(ctx context.Context, req ListEnvironmentsRequestObject) (ListEnvironmentsResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	repo, err := a.codeRepo(ctx, v, req.Org, req.Repo)
	if err != nil {
		return nil, err
	}
	branch := ""
	if req.Params.Branch != nil {
		branch = *req.Params.Branch
	}
	list, err := a.code.Environments(ctx, v, repo.ID, branch)
	if err != nil {
		return nil, fail(err, "repository")
	}
	out := ListEnvironments200JSONResponse{Environments: make([]Environment, 0, len(list))}
	for _, e := range list {
		out.Environments = append(out.Environments, environment(e))
	}
	return out, nil
}

// GetEnvironment answers one of the repository's environments, or 304 while the caller holds how it reads.
func (a api) GetEnvironment(ctx context.Context, req GetEnvironmentRequestObject) (GetEnvironmentResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	repo, err := a.codeRepo(ctx, v, req.Org, req.Repo)
	if err != nil {
		return nil, err
	}
	e, err := a.code.Environment(ctx, v, repo.ID, req.Environment)
	if err != nil {
		return nil, fail(err, "environment")
	}
	body, tag, err := taggedEnvironment(e)
	if err != nil {
		return nil, err
	}
	if matches(req.Params.IfNoneMatch, tag) {
		return GetEnvironment304Response{Headers: GetEnvironment304ResponseHeaders{ETag: tag}}, nil
	}
	return GetEnvironment200JSONResponse{Body: body, Headers: GetEnvironment200ResponseHeaders{ETag: tag}}, nil
}

// ListDeliveries lists a page of one of the repository's environments' deliveries, and links the next.
func (a api) ListDeliveries(ctx context.Context, req ListDeliveriesRequestObject) (ListDeliveriesResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	repo, err := a.codeRepo(ctx, v, req.Org, req.Repo)
	if err != nil {
		return nil, err
	}
	before, limit := int64(0), 0
	if p := req.Params; p.Cursor != nil {
		if before, err = strconv.ParseInt(*p.Cursor, 10, 64); err != nil || before < 1 {
			return nil, refuse(http.StatusBadRequest, "cursor.invalid", "Start from the cursor the previous page's Link names.")
		}
	}
	if p := req.Params; p.Limit != nil {
		limit = *p.Limit
	}
	page, err := a.code.Deliveries(ctx, v, repo.ID, req.Environment, before, limit)
	if err != nil {
		return nil, fail(err, "environment")
	}
	out := ListDeliveries200JSONResponse{}
	out.Body.Deliveries = make([]Delivery, 0, len(page.Deliveries))
	for _, d := range page.Deliveries {
		out.Body.Deliveries = append(out.Body.Deliveries, delivery(d))
	}
	if page.Next != 0 {
		next := url.Values{"cursor": {strconv.FormatInt(page.Next, 10)}}
		if limit != 0 {
			next.Set("limit", strconv.Itoa(limit))
		}
		link := `<?` + next.Encode() + `>; rel="next"`
		out.Headers.Link = &link
	}
	return out, nil
}

// Redeliver queues a delivery of the head of one of the repository's environments' branch for the viewer, and names
// it in Location.
func (a api) Redeliver(ctx context.Context, req RedeliverRequestObject) (RedeliverResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	repo, err := a.codeRepo(ctx, v, req.Org, req.Repo)
	if err != nil {
		return nil, err
	}
	d, err := a.code.Redeliver(ctx, v, repo.ID, req.Environment)
	if err != nil {
		return nil, fail(err, "environment")
	}
	body, tag, err := taggedDelivery(d)
	if err != nil {
		return nil, err
	}
	headers := Redeliver202ResponseHeaders{ETag: tag, Location: deliveryLocation(req.Org, req.Repo, d.Number)}
	return Redeliver202JSONResponse{Body: body, Headers: headers}, nil
}

// PauseEnvironment pauses one of the repository's environments for the viewer.
func (a api) PauseEnvironment(ctx context.Context, req PauseEnvironmentRequestObject) (PauseEnvironmentResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	repo, err := a.codeRepo(ctx, v, req.Org, req.Repo)
	if err != nil {
		return nil, err
	}
	e, err := a.code.PauseEnvironment(ctx, v, repo.ID, req.Environment)
	if err != nil {
		return nil, fail(err, "environment")
	}
	body, tag, err := taggedEnvironment(e)
	if err != nil {
		return nil, err
	}
	return PauseEnvironment200JSONResponse{Body: body, Headers: PauseEnvironment200ResponseHeaders{ETag: tag}}, nil
}

// ResumeEnvironment resumes one of the repository's paused environments for the viewer.
func (a api) ResumeEnvironment(ctx context.Context, req ResumeEnvironmentRequestObject) (ResumeEnvironmentResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	repo, err := a.codeRepo(ctx, v, req.Org, req.Repo)
	if err != nil {
		return nil, err
	}
	e, err := a.code.ResumeEnvironment(ctx, v, repo.ID, req.Environment)
	if err != nil {
		return nil, fail(err, "environment")
	}
	body, tag, err := taggedEnvironment(e)
	if err != nil {
		return nil, err
	}
	return ResumeEnvironment200JSONResponse{Body: body, Headers: ResumeEnvironment200ResponseHeaders{ETag: tag}}, nil
}

// GetDelivery answers one of the repository's deliveries, or 304 while the caller holds how it reads.
func (a api) GetDelivery(ctx context.Context, req GetDeliveryRequestObject) (GetDeliveryResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	repo, err := a.codeRepo(ctx, v, req.Org, req.Repo)
	if err != nil {
		return nil, err
	}
	d, err := a.code.Delivery(ctx, v, repo.ID, req.Number)
	if err != nil {
		return nil, fail(err, "delivery")
	}
	body, tag, err := taggedDelivery(d)
	if err != nil {
		return nil, err
	}
	if matches(req.Params.IfNoneMatch, tag) {
		return GetDelivery304Response{Headers: GetDelivery304ResponseHeaders{ETag: tag}}, nil
	}
	return GetDelivery200JSONResponse{Body: body, Headers: GetDelivery200ResponseHeaders{ETag: tag}}, nil
}

// StartDelivery queues one of the repository's waiting deliveries for the viewer, and names it in Location.
func (a api) StartDelivery(ctx context.Context, req StartDeliveryRequestObject) (StartDeliveryResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	repo, err := a.codeRepo(ctx, v, req.Org, req.Repo)
	if err != nil {
		return nil, err
	}
	d, err := a.code.StartDelivery(ctx, v, repo.ID, req.Number)
	if err != nil {
		return nil, fail(err, "delivery")
	}
	body, tag, err := taggedDelivery(d)
	if err != nil {
		return nil, err
	}
	headers := StartDelivery202ResponseHeaders{ETag: tag, Location: deliveryLocation(req.Org, req.Repo, d.Number)}
	return StartDelivery202JSONResponse{Body: body, Headers: headers}, nil
}
