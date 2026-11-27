package rest

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"cobranch.dev/internal/services/code"
)

// environment is e as the API shows it.
func environment(e code.Environment) Environment {
	out := Environment{Id: e.ID, Target: Principal{Id: e.Target.ID, DisplayName: e.Target.DisplayName}, Branch: e.Branch,
		Cluster: e.Cluster, ClusterName: optional(e.ClusterName), Namespace: e.Namespace, Url: optional(e.URL),
		Renders: make([]Render, 0, len(e.Renders)), SyncedAt: utc(e.SyncedAt), Sync: EnvironmentSync(e.Sync),
		Health: EnvironmentHealth(e.Health), Paused: e.Paused, CreatedAt: e.CreatedAt.UTC(), Version: e.Version}
	for _, r := range e.Renders {
		out.Renders = append(out.Renders, Render{Kind: RenderKind(r.Kind), Path: r.Path})
	}
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
		Kind: DeliveryKind(d.Kind), Subject: d.Subject, Trigger: DeliveryTrigger(d.Trigger), Push: d.PushID, State: DeliveryState(d.State),
		Reason: d.Reason, StartedBy: principal(d.StartedBy), QueuedAt: d.QueuedAt.UTC(), StartedAt: utc(d.StartedAt),
		EndedAt: utc(d.EndedAt), Version: d.Version}
	if d.SHA != "" {
		out.Sha = &d.SHA
	}
	return out
}

// environmentObject is o as the API shows it.
func environmentObject(o code.EnvironmentObject) EnvironmentObject {
	out := EnvironmentObject{Group: optional(o.Group), Version: o.Version, Kind: o.Kind, Namespace: o.Namespace, Name: o.Name,
		Health: EnvironmentHealth(o.Health), Message: o.Message, Ready: optional(o.Ready), CreatedAt: utc(o.CreatedAt)}
	if o.Delivery != 0 {
		out.Delivery = &o.Delivery
	}
	return out
}

// deliveryTarget is t as the API shows it.
func deliveryTarget(t code.DeliveryTarget) DeliveryTarget {
	return DeliveryTarget{Name: t.Name, Branches: t.Branches, Cluster: t.Cluster, ClusterName: optional(t.ClusterName),
		Namespace: t.Namespace, Url: optional(t.URL)}
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

// ListEnvironments lists the repository's environments, of one branch when the query names it, or 304 while the caller
// holds them, after waiting for them to change as wait asks.
func (a api) ListEnvironments(ctx context.Context, req ListEnvironmentsRequestObject) (ListEnvironmentsResponseObject, error) {
	v, repo, waits, err := a.codeList(ctx, req.Org, req.Repo, req.Params.Wait, req.Params.IfNoneMatch)
	if err != nil {
		return nil, err
	}
	branch := orEmpty(req.Params.Branch)
	return readList(ctx, waits, func(ctx context.Context) ([]code.Environment, error) {
		list, err := a.code.Environments(ctx, v, repo.ID, branch)
		return list, fail(err, "repository")
	}, func(ctx context.Context) (<-chan []code.Environment, error) {
		lists, err := a.code.FollowEnvironments(ctx, v, repo.ID, branch)
		return lists, fail(err, "repository")
	}, func(list []code.Environment) (ListEnvironmentsResponseObject, bool, error) {
		out := ListEnvironments200JSONResponse{}
		out.Body.Environments = mapAll(list, environment)
		return listAnswer("environments", out.Body, req.Params.IfNoneMatch,
			func(etag string) ListEnvironmentsResponseObject { out.Headers.ETag = etag; return out },
			func(etag string) ListEnvironmentsResponseObject {
				return ListEnvironments304Response{Headers: ListEnvironments304ResponseHeaders{ETag: etag}}
			})
	})
}

// GetEnvironment answers one of the repository's environments, or 304 while the caller holds how it reads, after
// waiting for it to change as wait asks.
func (a api) GetEnvironment(ctx context.Context, req GetEnvironmentRequestObject) (GetEnvironmentResponseObject, error) {
	v, repo, waits, err := a.codeList(ctx, req.Org, req.Repo, req.Params.Wait, req.Params.IfNoneMatch)
	if err != nil {
		return nil, err
	}
	return readList(ctx, waits, func(ctx context.Context) (code.Environment, error) {
		e, err := a.code.Environment(ctx, v, repo.ID, req.Environment)
		return e, fail(err, "environment")
	}, func(ctx context.Context) (<-chan code.Environment, error) {
		e, err := a.code.FollowEnvironment(ctx, v, repo.ID, req.Environment)
		return e, fail(err, "environment")
	}, func(e code.Environment) (GetEnvironmentResponseObject, bool, error) {
		body, tag, err := taggedEnvironment(e)
		if err != nil {
			return nil, false, err
		}
		if matches(req.Params.IfNoneMatch, tag) {
			return GetEnvironment304Response{Headers: GetEnvironment304ResponseHeaders{ETag: tag}}, true, nil
		}
		return GetEnvironment200JSONResponse{Body: body, Headers: GetEnvironment200ResponseHeaders{ETag: tag}}, false, nil
	})
}

// ListEnvironmentObjects lists what one of the repository's environments holds, in order, or 304 while the caller
// holds it, after waiting for it to change as wait asks.
func (a api) ListEnvironmentObjects(ctx context.Context, req ListEnvironmentObjectsRequestObject) (ListEnvironmentObjectsResponseObject, error) {
	v, repo, waits, err := a.codeList(ctx, req.Org, req.Repo, req.Params.Wait, req.Params.IfNoneMatch)
	if err != nil {
		return nil, err
	}
	return readList(ctx, waits, func(ctx context.Context) ([]code.EnvironmentObject, error) {
		list, err := a.code.EnvironmentObjects(ctx, v, repo.ID, req.Environment)
		return list, fail(err, "environment")
	}, func(ctx context.Context) (<-chan []code.EnvironmentObject, error) {
		lists, err := a.code.FollowEnvironmentObjects(ctx, v, repo.ID, req.Environment)
		return lists, fail(err, "environment")
	}, func(list []code.EnvironmentObject) (ListEnvironmentObjectsResponseObject, bool, error) {
		out := ListEnvironmentObjects200JSONResponse{}
		out.Body.Objects = mapAll(list, environmentObject)
		return listAnswer("objects", out.Body, req.Params.IfNoneMatch,
			func(etag string) ListEnvironmentObjectsResponseObject { out.Headers.ETag = etag; return out },
			func(etag string) ListEnvironmentObjectsResponseObject {
				return ListEnvironmentObjects304Response{Headers: ListEnvironmentObjects304ResponseHeaders{ETag: etag}}
			})
	})
}

// ListDeliveryTargets lists the targets the repository's default branch declares, or 304 while the caller holds them,
// after waiting for them to change as wait asks.
func (a api) ListDeliveryTargets(ctx context.Context, req ListDeliveryTargetsRequestObject) (ListDeliveryTargetsResponseObject, error) {
	v, repo, waits, err := a.codeList(ctx, req.Org, req.Repo, req.Params.Wait, req.Params.IfNoneMatch)
	if err != nil {
		return nil, err
	}
	return readList(ctx, waits, func(ctx context.Context) ([]code.DeliveryTarget, error) {
		list, err := a.code.DeliveryTargets(ctx, v, repo.ID)
		return list, fail(err, "repository")
	}, func(ctx context.Context) (<-chan []code.DeliveryTarget, error) {
		lists, err := a.code.FollowDeliveryTargets(ctx, v, repo.ID)
		return lists, fail(err, "repository")
	}, func(list []code.DeliveryTarget) (ListDeliveryTargetsResponseObject, bool, error) {
		out := ListDeliveryTargets200JSONResponse{}
		out.Body.Targets = mapAll(list, deliveryTarget)
		return listAnswer("targets", out.Body, req.Params.IfNoneMatch,
			func(etag string) ListDeliveryTargetsResponseObject { out.Headers.ETag = etag; return out },
			func(etag string) ListDeliveryTargetsResponseObject {
				return ListDeliveryTargets304Response{Headers: ListDeliveryTargets304ResponseHeaders{ETag: etag}}
			})
	})
}

// ListDeliveries lists a page of one of the repository's environments' deliveries, of the kinds the query names, and
// links the next, or answers 304 while the caller holds the page, after waiting for it to change as wait asks.
func (a api) ListDeliveries(ctx context.Context, req ListDeliveriesRequestObject) (ListDeliveriesResponseObject, error) {
	v, repo, waits, err := a.codeList(ctx, req.Org, req.Repo, req.Params.Wait, req.Params.IfNoneMatch)
	if err != nil {
		return nil, err
	}
	before, limit, kinds, err := deliveriesQuery(req.Params)
	if err != nil {
		return nil, err
	}
	return readList(ctx, waits, func(ctx context.Context) (code.DeliveryPage, error) {
		page, err := a.code.Deliveries(ctx, v, repo.ID, req.Environment, kinds, before, limit)
		return page, fail(err, "environment")
	}, func(ctx context.Context) (<-chan code.DeliveryPage, error) {
		pages, err := a.code.FollowDeliveries(ctx, v, repo.ID, req.Environment, kinds, before, limit)
		return pages, fail(err, "environment")
	}, func(page code.DeliveryPage) (ListDeliveriesResponseObject, bool, error) {
		out := ListDeliveries200JSONResponse{}
		out.Body.Deliveries = mapAll(page.Deliveries, delivery)
		if page.Next != 0 {
			link := `<?` + deliveriesNext(page.Next, kinds, limit).Encode() + `>; rel="next"`
			out.Headers.Link = &link
		}
		etag, err := bodyTag("deliveries", out.Body, out.Headers.Link)
		if err != nil {
			return nil, false, err
		}
		if matches(req.Params.IfNoneMatch, etag) {
			return ListDeliveries304Response{Headers: ListDeliveries304ResponseHeaders{ETag: etag}}, true, nil
		}
		out.Headers.ETag = etag
		return out, false, nil
	})
}

// deliveriesQuery is the page of deliveries p asks for: where it starts, how long it is and the kinds it holds.
func deliveriesQuery(p ListDeliveriesParams) (int64, int, []code.DeliveryKind, error) {
	before, limit := int64(0), 0
	if p.Cursor != nil {
		var err error
		if before, err = strconv.ParseInt(*p.Cursor, 10, 64); err != nil || before < 1 {
			return 0, 0, nil, refuse(http.StatusBadRequest, "cursor.invalid", "Start from the cursor the previous page's Link names.")
		}
	}
	if p.Limit != nil {
		limit = *p.Limit
	}
	var kinds []code.DeliveryKind
	if p.Kind != nil {
		for _, k := range *p.Kind {
			kinds = append(kinds, code.DeliveryKind(k))
		}
	}
	return before, limit, kinds, nil
}

// deliveriesNext is the query of the page of deliveries of kinds, limit long, after the one that ends at next.
func deliveriesNext(next int64, kinds []code.DeliveryKind, limit int) url.Values {
	q := url.Values{"cursor": {strconv.FormatInt(next, 10)}}
	for _, k := range kinds {
		q.Add("kind", string(k))
	}
	if limit != 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	return q
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

// UploadManifests keeps a delivery's render, which its render pod uploads with the delivery's own token, and names it
// in Location. It reads one byte past the most a render may be, so that a larger one is refused as such.
func (a api) UploadManifests(ctx context.Context, req UploadManifestsRequestObject) (UploadManifestsResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	repo, err := a.codeRepo(ctx, v, req.Org, req.Repo)
	if err != nil {
		return nil, err
	}
	manifests, err := io.ReadAll(io.LimitReader(req.Body, code.MaxRender+1))
	if err != nil {
		return nil, refuse(http.StatusBadRequest, "request.invalid", "Reading the render failed: "+err.Error())
	}
	if err := a.code.UploadRender(ctx, v, repo.ID, req.Number, manifests); err != nil {
		return nil, fail(err, "delivery")
	}
	location := deliveryLocation(req.Org, req.Repo, req.Number) + "/manifests"
	return UploadManifests201Response{Headers: UploadManifests201ResponseHeaders{Location: location}}, nil
}
