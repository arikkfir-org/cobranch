package rest

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/code"
)

// Two environments of website: production of main and previews of feature/login, by these IDs.
var (
	productionID = uuid.MustParse("0195d3c4-0000-7000-8000-000000000001")
	previewID    = uuid.MustParse("0195d3c4-0000-7000-8000-000000000002")
)

// withEnvironments adds website's production environment of main, whose delivery #1 of b succeeded and #2 of c is
// queued, and its preview of feature/login, whose #3 of c waits for a member, before the viewer's setup.
func withEnvironments(viewer func(*orgFixture) *auth.Authenticated) func(*orgFixture) *auth.Authenticated {
	return func(f *orgFixture) *auth.Authenticated {
		at := time.Date(2026, 11, 2, 9, 0, 0, 0, time.FixedZone("IST", 2*3600))
		f.codeStore.Now = at
		f.codeStore.AddEnvironment(f.website.ID, code.Environment{ID: productionID, Target: code.Principal{ID: uuid.New(), DisplayName: "production"},
			Branch: "main", Namespace: "shop", Revision: shaOf("b"), SyncedAt: &at, Sync: code.OutOfSync, Health: code.Healthy, CreatedAt: at})
		f.codeStore.AddEnvironment(f.website.ID, code.Environment{ID: previewID, Target: code.Principal{ID: uuid.New(), DisplayName: "previews"},
			Branch: "feature/login", Namespace: "shop-feature-login", Sync: code.OutOfSync, Health: code.Unknown, CreatedAt: at})
		for _, d := range []code.Delivery{
			{EnvironmentID: productionID, SHA: shaOf("b"), State: code.DeliverySucceeded, EndedAt: &at},
			{EnvironmentID: productionID, SHA: shaOf("c"), State: code.DeliveryQueued},
			{EnvironmentID: previewID, SHA: shaOf("c"), State: code.DeliveryWaiting, Reason: "waits for a member to start it"},
		} {
			d.Kind, d.Trigger, d.QueuedAt = code.DeliverCommit, code.TriggeredByPush, at
			f.codeStore.AddDelivery(f.website.ID, d)
		}
		if viewer == nil {
			return nil
		}
		return viewer(f)
	}
}

const environmentsPath = "/v1/orgs/acme/repos/website/environments"

var (
	productionPath = environmentsPath + "/" + productionID.String()
	previewPath    = environmentsPath + "/" + previewID.String()
)

func TestListEnvironmentsAPI(t *testing.T) {
	runOrgCases(t, []orgCase{
		{name: "a Reader lists them, by target", viewer: withEnvironments(maxReading), method: http.MethodGet, path: fixed(environmentsPath), wantStatus: 200,
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				got := decode[struct{ Environments []Environment }](t, body)
				if len(got.Environments) != 2 || got.Environments[0].Id != previewID || got.Environments[1].Id != productionID {
					t.Fatalf("environments = %s", body)
				}
				e := got.Environments[1]
				if e.Target.DisplayName != "production" || e.Branch != "main" || e.Namespace != "shop" || e.Cluster != nil ||
					e.Revision == nil || *e.Revision != shaOf("b") || e.Sync != "out_of_sync" || e.Health != "healthy" ||
					e.SyncedAt == nil || e.SyncedAt.Location() != time.UTC || e.Latest == nil || e.Latest.Number != 2 ||
					e.Latest.State != "queued" || e.Version != 1 {
					t.Errorf("production = %+v", e)
				}
				if p := got.Environments[0]; p.Revision != nil || p.SyncedAt != nil || p.Latest == nil || p.Latest.Reason != "waits for a member to start it" {
					t.Errorf("preview = %+v", p)
				}
			}},
		{name: "of a branch", viewer: withEnvironments(maxReading), method: http.MethodGet, path: fixed(environmentsPath + "?branch=feature/login"), wantStatus: 200,
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				if got := decode[struct{ Environments []Environment }](t, body); len(got.Environments) != 1 || got.Environments[0].Id != previewID {
					t.Errorf("environments = %s", body)
				}
			}},
		{name: "none yet", viewer: maxReading, method: http.MethodGet, path: fixed(environmentsPath), wantStatus: 200,
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				if got := decode[struct{ Environments []Environment }](t, body); got.Environments == nil || len(got.Environments) != 0 {
					t.Errorf("environments = %s, want []", body)
				}
			}},
		{name: "someone who can't read it", viewer: withEnvironments(nia), method: http.MethodGet, path: fixed(environmentsPath), wantStatus: 404},
		{name: "anonymous", viewer: withEnvironments(nil), method: http.MethodGet, path: fixed(environmentsPath), wantStatus: 401, wantCode: "auth.unauthenticated"},
	})
}

func TestEnvironmentAPI(t *testing.T) {
	runOrgCases(t, []orgCase{
		{name: "one by ID", viewer: withEnvironments(maxReading), method: http.MethodGet, path: fixed(productionPath), wantStatus: 200,
			wantTag: `"1-`, check: func(t *testing.T, _ *orgFixture, body []byte) {
				if e := decode[Environment](t, body); e.Id != productionID || e.Latest == nil || e.Latest.Number != 2 {
					t.Errorf("environment = %s", body)
				}
			}},
		{name: "one the caller holds", viewer: withEnvironments(maxReading), method: http.MethodGet, path: fixed(productionPath),
			headerFor: held(maxReading, fixed(productionPath)), wantStatus: 304, wantTag: `"1-`},
		{name: "one whose newest delivery changed since", viewer: withEnvironments(maxReading), method: http.MethodGet, path: fixed(previewPath),
			headerFor: heldThenStarted(maxReading, fixed(previewPath)), wantStatus: 200, wantTag: `"1-`,
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				if e := decode[Environment](t, body); e.Latest == nil || e.Latest.State != "queued" {
					t.Errorf("environment = %s, want its newest delivery queued", body)
				}
			}},
		{name: "one that isn't there", viewer: withEnvironments(maxReading), method: http.MethodGet, path: fixed(environmentsPath + "/" + uuid.NewString()),
			wantStatus: 404, wantCode: "environment.not_found"},
		{name: "an ID that isn't one", viewer: withEnvironments(maxReading), method: http.MethodGet, path: fixed(environmentsPath + "/production"), wantStatus: 400},
		{name: "its deliveries, newest first", viewer: withEnvironments(maxReading), method: http.MethodGet, path: fixed(productionPath + "/deliveries"), wantStatus: 200,
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				got := decode[struct{ Deliveries []Delivery }](t, body)
				if len(got.Deliveries) != 2 || got.Deliveries[0].Number != 2 || got.Deliveries[1].Number != 1 {
					t.Fatalf("deliveries = %s", body)
				}
				d := got.Deliveries[1]
				if d.Environment != productionID || d.Target != "production" || d.Branch != "main" || d.Kind != "deliver" || d.Trigger != "push" ||
					d.Sha == nil || *d.Sha != shaOf("b") || d.State != "succeeded" || d.EndedAt == nil || d.QueuedAt.Location() != time.UTC {
					t.Errorf("delivery = %+v", d)
				}
			}},
		{name: "a page links the next", viewer: withEnvironments(maxReading), method: http.MethodGet, path: fixed(productionPath + "/deliveries?limit=1"), wantStatus: 200,
			wantHeader: http.Header{"Link": {`<?cursor=2&limit=1>; rel="next"`}}, check: func(t *testing.T, _ *orgFixture, body []byte) {
				if got := decode[struct{ Deliveries []Delivery }](t, body); len(got.Deliveries) != 1 || got.Deliveries[0].Number != 2 {
					t.Errorf("deliveries = %s", body)
				}
			}},
		{name: "the next page", viewer: withEnvironments(maxReading), method: http.MethodGet, path: fixed(productionPath + "/deliveries?limit=1&cursor=2"), wantStatus: 200,
			wantHeader: http.Header{"Link": {""}}, check: func(t *testing.T, _ *orgFixture, body []byte) {
				if got := decode[struct{ Deliveries []Delivery }](t, body); len(got.Deliveries) != 1 || got.Deliveries[0].Number != 1 {
					t.Errorf("deliveries = %s", body)
				}
			}},
		{name: "a cursor of nothing", viewer: withEnvironments(maxReading), method: http.MethodGet, path: fixed(productionPath + "/deliveries?cursor=next"), wantStatus: 400, wantCode: "cursor.invalid"},
		{name: "too many a page", viewer: withEnvironments(maxReading), method: http.MethodGet, path: fixed(productionPath + "/deliveries?limit=101"), wantStatus: 400},
		{name: "the deliveries of one that isn't there", viewer: withEnvironments(maxReading), method: http.MethodGet,
			path: fixed(environmentsPath + "/" + uuid.NewString() + "/deliveries"), wantStatus: 404, wantCode: "environment.not_found"},
	})
}

func TestEnvironmentActionsAPI(t *testing.T) {
	runOrgCases(t, []orgCase{
		{name: "a Developer redelivers", viewer: withEnvironments(maxDeveloping), method: http.MethodPost, path: fixed(productionPath + "/redeliver"), wantStatus: 202,
			wantHeader: http.Header{"Location": {"/v1/orgs/acme/repos/website/deliveries/4"}}, wantTag: `"1-`, check: func(t *testing.T, f *orgFixture, body []byte) {
				d := decode[Delivery](t, body)
				if d.Number != 4 || d.State != "queued" || d.Trigger != "redeliver" || d.Sha == nil || *d.Sha != shaOf("c") ||
					d.StartedBy == nil || d.StartedBy.Id != f.max.User.ID || d.StartedBy.DisplayName != "Max" {
					t.Errorf("delivery = %s", body)
				}
				if superseded := f.codeStore.DeliveryList[f.website.ID][1]; superseded.State != code.DeliveryCancelled || superseded.Reason != "superseded by #4" {
					t.Errorf("#2 = %+v, want it superseded", superseded)
				}
			}},
		{name: "a Reader may not redeliver", viewer: withEnvironments(maxReading), method: http.MethodPost, path: fixed(productionPath + "/redeliver"), wantStatus: 403},
		{name: "a Developer pauses one", viewer: withEnvironments(maxDeveloping), method: http.MethodPost, path: fixed(productionPath + "/pause"), wantStatus: 200,
			wantTag: `"2-`, check: func(t *testing.T, _ *orgFixture, body []byte) {
				if e := decode[Environment](t, body); !e.Paused || e.Latest == nil || e.Latest.State != "cancelled" || e.Latest.Reason != "paused by Max" {
					t.Errorf("environment = %s", body)
				}
			}},
		{name: "redelivering a paused one", viewer: withEnvironments(paused(maxDeveloping)), method: http.MethodPost, path: fixed(productionPath + "/redeliver"),
			wantStatus: 409, wantCode: "environment.paused"},
		{name: "pausing a paused one", viewer: withEnvironments(paused(maxDeveloping)), method: http.MethodPost, path: fixed(productionPath + "/pause"),
			wantStatus: 409, wantCode: "environment.paused"},
		{name: "a Developer resumes one", viewer: withEnvironments(paused(maxDeveloping)), method: http.MethodPost, path: fixed(productionPath + "/resume"), wantStatus: 200,
			wantTag: `"2-`, check: func(t *testing.T, _ *orgFixture, body []byte) {
				if e := decode[Environment](t, body); e.Paused || e.Latest == nil || e.Latest.Number != 4 || e.Latest.Trigger != "resume" {
					t.Errorf("environment = %s", body)
				}
			}},
		{name: "resuming one that isn't paused", viewer: withEnvironments(maxDeveloping), method: http.MethodPost, path: fixed(productionPath + "/resume"),
			wantStatus: 409, wantCode: "environment.not_paused"},
		{name: "a Reader may not pause", viewer: withEnvironments(maxReading), method: http.MethodPost, path: fixed(productionPath + "/pause"), wantStatus: 403},
		{name: "someone who can't read it", viewer: withEnvironments(nia), method: http.MethodPost, path: fixed(previewPath + "/pause"), wantStatus: 404},
	})
}

// heldThenStarted is held's header, read before website's delivery #3 starts: #3 is its preview's newest, so starting
// it changes how the preview reads and not the preview's version.
func heldThenStarted(viewer func(*orgFixture) *auth.Authenticated, path func(*orgFixture) string) func(*orgFixture) http.Header {
	return func(f *orgFixture) http.Header {
		header := held(viewer, path)(f)
		if _, err := f.codeStore.StartDelivery(context.Background(), uuid.New(), f.website.ID, 3); err != nil {
			panic(err)
		}
		return header
	}
}

// paused pauses website's production environment before the viewer's setup.
func paused(viewer func(*orgFixture) *auth.Authenticated) func(*orgFixture) *auth.Authenticated {
	return func(f *orgFixture) *auth.Authenticated {
		f.codeStore.EnvironmentList[f.website.ID][0].Paused = true
		return viewer(f)
	}
}

const deliveriesPath = "/v1/orgs/acme/repos/website/deliveries"

func TestDeliveryAPI(t *testing.T) {
	runOrgCases(t, []orgCase{
		{name: "one by number", viewer: withEnvironments(maxReading), method: http.MethodGet, path: fixed(deliveriesPath + "/3"), wantStatus: 200,
			wantTag: `"1-`, check: func(t *testing.T, _ *orgFixture, body []byte) {
				if d := decode[Delivery](t, body); d.Number != 3 || d.State != "waiting" || d.Environment != previewID || d.Branch != "feature/login" {
					t.Errorf("delivery = %s", body)
				}
			}},
		{name: "one the caller holds", viewer: withEnvironments(maxReading), method: http.MethodGet, path: fixed(deliveriesPath + "/3"),
			headerFor: held(maxReading, fixed(deliveriesPath+"/3")), wantStatus: 304, wantTag: `"1-`},
		{name: "one that isn't there", viewer: withEnvironments(maxReading), method: http.MethodGet, path: fixed(deliveriesPath + "/9"), wantStatus: 404, wantCode: "delivery.not_found"},
		{name: "a Developer starts a waiting one", viewer: withEnvironments(maxDeveloping), method: http.MethodPost, path: fixed(deliveriesPath + "/3/start"), wantStatus: 202,
			wantHeader: http.Header{"Location": {deliveriesPath + "/3"}}, wantTag: `"2-`, check: func(t *testing.T, f *orgFixture, body []byte) {
				if d := decode[Delivery](t, body); d.State != "queued" || d.Reason != "" || d.StartedBy == nil || d.StartedBy.Id != f.max.User.ID {
					t.Errorf("delivery = %s", body)
				}
			}},
		{name: "one that doesn't wait", viewer: withEnvironments(maxDeveloping), method: http.MethodPost, path: fixed(deliveriesPath + "/2/start"), wantStatus: 409, wantCode: "delivery.not_waiting"},
		{name: "a Reader may not start", viewer: withEnvironments(maxReading), method: http.MethodPost, path: fixed(deliveriesPath + "/3/start"), wantStatus: 403},
		{name: "starting one that isn't there", viewer: withEnvironments(maxDeveloping), method: http.MethodPost, path: fixed(deliveriesPath + "/9/start"), wantStatus: 404},
	})
}
