package graphql

import (
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
// queued, and its preview of feature/login, whose #3 of c waits for a member, before next's setup.
func withEnvironments(next func(*orgFixture) *auth.Authenticated) func(*orgFixture) *auth.Authenticated {
	return func(f *orgFixture) *auth.Authenticated {
		at := time.Date(2026, 11, 2, 9, 0, 0, 0, time.FixedZone("IST", 2*3600))
		f.codeStore.Now = at
		cluster := uuid.MustParse("6f1c2b8e-3d4a-4e5f-9a1b-2c3d4e5f6a7b")
		f.codeStore.AddEnvironment(f.website.ID, code.Environment{ID: productionID, Target: code.Principal{ID: uuid.New(), DisplayName: "production"},
			Branch: "main", Namespace: "shop", Revision: shaOf("b"), SyncedAt: &at, Sync: code.OutOfSync, Health: code.Healthy, CreatedAt: at})
		f.codeStore.AddEnvironment(f.website.ID, code.Environment{ID: previewID, Target: code.Principal{ID: uuid.New(), DisplayName: "previews"},
			Branch: "feature/login", Cluster: &cluster, Namespace: "shop-feature-login", Sync: code.OutOfSync, Health: code.Unknown, CreatedAt: at})
		for _, d := range []code.Delivery{
			{EnvironmentID: productionID, SHA: shaOf("b"), State: code.DeliverySucceeded, EndedAt: &at},
			{EnvironmentID: productionID, SHA: shaOf("c"), State: code.DeliveryQueued},
			{EnvironmentID: previewID, SHA: shaOf("c"), State: code.DeliveryWaiting, Reason: "waits for a member to start it"},
		} {
			d.Kind, d.Trigger, d.QueuedAt = code.DeliverCommit, code.TriggeredByPush, at
			f.codeStore.AddDelivery(f.website.ID, d)
		}
		return next(f)
	}
}

func TestEnvironmentQueries(t *testing.T) {
	repo := func(fields string) func(*orgFixture) string {
		return fixed(`{ organization(slug: "acme") { repository(slug: "website") { ` + fields + ` } } }`)
	}
	reader := withEnvironments(maxHolding(auth.RoleReader))
	runOrgCases(t, []orgCase{
		{name: "a Reader lists them, by target", viewer: reader,
			document: repo(`environments { target { displayName } branch namespace cluster revision syncedAt sync health paused latest { number state } version }`),
			want: []string{`"environments":[{"target":{"displayName":"previews"},"branch":"feature/login","namespace":"shop-feature-login",` +
				`"cluster":"6f1c2b8e-3d4a-4e5f-9a1b-2c3d4e5f6a7b","revision":null,"syncedAt":null,"sync":"OUT_OF_SYNC","health":"UNKNOWN","paused":false,` +
				`"latest":{"number":3,"state":"WAITING"},"version":1},{"target":{"displayName":"production"},"branch":"main","namespace":"shop","cluster":null,` +
				`"revision":"` + shaOf("b") + `","syncedAt":"2026-11-02T07:00:00Z","sync":"OUT_OF_SYNC","health":"HEALTHY","paused":false,` +
				`"latest":{"number":2,"state":"QUEUED"},"version":1}]`}},
		{name: "a branch's", viewer: reader, document: repo(`branch(name: "feature/login") { environments { id } }`),
			want: []string{`"environments":[{"id":"` + previewID.String() + `"}]`}},
		{name: "a branch without any", viewer: reader, document: repo(`branch(name: "feature") { environments { id } }`), want: []string{`"environments":[]`}},
		{name: "its deliveries, a page and the next", viewer: reader,
			document: repo(`environment(id: "` + productionID.String() + `") { first: deliveries(first: 1) { deliveries { number } nextCursor } ` +
				`next: deliveries(first: 1, after: "2") { deliveries { number kind sha trigger state endedAt environmentId target branch } nextCursor } }`),
			want: []string{`"first":{"deliveries":[{"number":2}],"nextCursor":"2"}`,
				`"next":{"deliveries":[{"number":1,"kind":"DELIVER","sha":"` + shaOf("b") + `","trigger":"PUSH","state":"SUCCEEDED","endedAt":"2026-11-02T07:00:00Z",` +
					`"environmentId":"` + productionID.String() + `","target":"production","branch":"main"}],"nextCursor":null}`}},
		{name: "one that isn't there", viewer: reader, document: repo(`environment(id: "` + uuid.NewString() + `") { id }`), want: []string{`"environment":null`}},
		{name: "an ID that isn't one", viewer: reader, document: repo(`environment(id: "production") { id }`), want: []string{`"environment":null`}},
		{name: "a delivery by number", viewer: reader, document: repo(`delivery(number: 3) { number state reason branch startedBy { displayName } }`),
			want: []string{`"delivery":{"number":3,"state":"WAITING","reason":"waits for a member to start it","branch":"feature/login","startedBy":null}`}},
		{name: "a delivery that isn't there", viewer: reader, document: repo(`delivery(number: 9) { number }`), want: []string{`"delivery":null`}},
		{name: "a cursor of nothing", viewer: reader, document: repo(`environment(id: "` + productionID.String() + `") { deliveries(after: "next") { nextCursor } }`),
			wantCode: badUserInput, wantReason: "cursor.invalid"},
		{name: "too many a page", viewer: reader, document: repo(`environment(id: "` + productionID.String() + `") { deliveries(first: 101) { nextCursor } }`),
			wantCode: badUserInput, wantReason: "limit.invalid"},
	})
}

func TestEnvironmentMutations(t *testing.T) {
	onEnvironment := func(mutation string, id uuid.UUID, fields string) func(*orgFixture) string {
		return func(f *orgFixture) string {
			return `mutation { ` + mutation + `(repositoryId: "` + f.website.ID.String() + `", environmentId: "` + id.String() + `") { ` + fields + ` } }`
		}
	}
	start := func(number string) func(*orgFixture) string {
		return func(f *orgFixture) string {
			return `mutation { startDelivery(repositoryId: "` + f.website.ID.String() + `", number: ` + number + `) { number state reason startedBy { displayName } version } }`
		}
	}
	developer, reader := withEnvironments(maxHolding(auth.RoleDeveloper)), withEnvironments(maxHolding(auth.RoleReader))
	runOrgCases(t, []orgCase{
		{name: "a Developer redelivers", viewer: developer, document: onEnvironment("redeliver", productionID, "number state trigger sha startedBy { displayName }"),
			want: []string{`"redeliver":{"number":4,"state":"QUEUED","trigger":"REDELIVER","sha":"` + shaOf("c") + `","startedBy":{"displayName":"Max"}}`}},
		{name: "a Reader may not redeliver", viewer: reader, document: onEnvironment("redeliver", productionID, "number"), wantCode: forbidden},
		{name: "a Developer pauses one", viewer: developer, document: onEnvironment("pauseEnvironment", productionID, "paused latest { state reason } version"),
			want: []string{`"pauseEnvironment":{"paused":true,"latest":{"state":"CANCELLED","reason":"paused by Max"},"version":2}`}},
		{name: "pausing a paused one", viewer: withEnvironments(paused(maxHolding(auth.RoleDeveloper))), document: onEnvironment("pauseEnvironment", productionID, "paused"),
			wantCode: conflict, wantReason: "environment.paused"},
		{name: "a Developer resumes one", viewer: withEnvironments(paused(maxHolding(auth.RoleDeveloper))),
			document: onEnvironment("resumeEnvironment", productionID, "paused latest { number trigger }"),
			want:     []string{`"resumeEnvironment":{"paused":false,"latest":{"number":4,"trigger":"RESUME"}}`}},
		{name: "resuming one that isn't paused", viewer: developer, document: onEnvironment("resumeEnvironment", productionID, "paused"),
			wantCode: conflict, wantReason: "environment.not_paused"},
		{name: "one that isn't there", viewer: developer, document: onEnvironment("pauseEnvironment", uuid.New(), "paused"), wantCode: notFound, wantReason: "environment.not_found"},
		{name: "a Developer starts a waiting delivery", viewer: developer, document: start("3"),
			want: []string{`"startDelivery":{"number":3,"state":"QUEUED","reason":"","startedBy":{"displayName":"Max"},"version":2}`}},
		{name: "one that doesn't wait", viewer: developer, document: start("2"), wantCode: conflict, wantReason: "delivery.not_waiting"},
		{name: "a Reader may not start", viewer: reader, document: start("3"), wantCode: forbidden},
		{name: "a Member who doesn't read it finds nothing", viewer: withEnvironments(maxim), document: start("3"), wantCode: notFound, wantReason: "delivery.not_found"},
	})
}

// paused pauses website's production environment before next's setup.
func paused(next func(*orgFixture) *auth.Authenticated) func(*orgFixture) *auth.Authenticated {
	return func(f *orgFixture) *auth.Authenticated {
		f.codeStore.EnvironmentList[f.website.ID][0].Paused = true
		return next(f)
	}
}

// withPlans gives withEnvironments' preview its URL and what it renders, production what it holds and a resync, and
// website its targets, before next's setup; the preview's cluster is website's secret previews-gke.
func withPlans(next func(*orgFixture) *auth.Authenticated) func(*orgFixture) *auth.Authenticated {
	return withEnvironments(func(f *orgFixture) *auth.Authenticated {
		created := time.Date(2026, 11, 2, 9, 0, 0, 0, time.FixedZone("IST", 2*3600))
		e := &f.codeStore.EnvironmentList[f.website.ID][1]
		e.URL, e.Renders = "https://feature-login.example.com", []code.Render{{Kind: "helm", Path: "deploy/chart"}, {Kind: "kustomize", Path: "deploy/previews"}}
		f.codeStore.Clusters = []code.ClusterSecret{{ID: *e.Cluster, OrgID: f.acme.ID, RepoID: &f.website.ID, Name: "previews-gke"}}
		f.codeStore.Objects[productionID] = []code.EnvironmentObject{
			{Group: "apps", Version: "v1", Kind: "Deployment", Namespace: "shop", Name: "api", Health: code.Progressing, Message: "Ready: 1/2",
				Ready: "1/2", CreatedAt: &created, Delivery: 1},
			{Version: "v1", Kind: "Service", Namespace: "shop", Name: "api", Health: code.Healthy}}
		f.codeStore.Targets[f.website.ID] = []code.Target{{Name: "previews", Branches: []string{"feature/**"}, Cluster: e.Cluster,
			Namespace: "shop-${branch.slug}", URL: "https://${branch.slug}.example.com"}}
		f.codeStore.AddDelivery(f.website.ID, code.Delivery{EnvironmentID: productionID, Kind: code.Resync, SHA: shaOf("b"),
			Trigger: code.TriggeredBySchedule, State: code.DeliverySucceeded})
		return next(f)
	})
}

func TestEnvironmentPlanQueries(t *testing.T) {
	repo := func(fields string) func(*orgFixture) string {
		return fixed(`{ organization(slug: "acme") { repository(slug: "website") { ` + fields + ` } } }`)
	}
	reader, maintainer := withPlans(maxHolding(auth.RoleReader)), withPlans(maxHolding(auth.RoleMaintainer))
	preview, production := `environment(id: "`+previewID.String()+`")`, `environment(id: "`+productionID.String()+`")`
	runOrgCases(t, []orgCase{
		{name: "a Maintainer sees its URL, renders and cluster's name", viewer: maintainer, document: repo(preview + ` { url clusterName renders { kind path } }`),
			want: []string{`"environment":{"url":"https://feature-login.example.com","clusterName":"previews-gke",` +
				`"renders":[{"kind":"HELM","path":"deploy/chart"},{"kind":"KUSTOMIZE","path":"deploy/previews"}]}`}},
		{name: "a Reader sees no cluster's name", viewer: reader, document: repo(preview + ` { cluster clusterName }`),
			want: []string{`"environment":{"cluster":"6f1c2b8e-3d4a-4e5f-9a1b-2c3d4e5f6a7b","clusterName":null}`}},
		{name: "one without a URL or renders", viewer: reader, document: repo(production + ` { url renders { path } }`),
			want: []string{`"environment":{"url":null,"renders":[]}`}},
		{name: "what it holds", viewer: reader, document: repo(production + ` { objects { group version kind namespace name health message ready createdAt delivery } }`),
			want: []string{`"objects":[{"group":"apps","version":"v1","kind":"Deployment","namespace":"shop","name":"api","health":"PROGRESSING",` +
				`"message":"Ready: 1/2","ready":"1/2","createdAt":"2026-11-02T07:00:00Z","delivery":1},` +
				`{"group":"","version":"v1","kind":"Service","namespace":"shop","name":"api","health":"HEALTHY","message":"","ready":null,"createdAt":null,"delivery":null}]`}},
		{name: "its deliveries of some kinds", viewer: reader,
			document: repo(production + ` { commits: deliveries(kinds: [DELIVER, TEARDOWN]) { deliveries { number } } resyncs: deliveries(kinds: [RESYNC]) { deliveries { number kind } } }`),
			want:     []string{`"commits":{"deliveries":[{"number":2},{"number":1}]}`, `"resyncs":{"deliveries":[{"number":4,"kind":"RESYNC"}]}`}},
		{name: "the targets", viewer: maintainer, document: repo(`deliveryTargets { name branches cluster clusterName namespace url }`),
			want: []string{`"deliveryTargets":[{"name":"previews","branches":["feature/**"],"cluster":"6f1c2b8e-3d4a-4e5f-9a1b-2c3d4e5f6a7b",` +
				`"clusterName":"previews-gke","namespace":"shop-${branch.slug}","url":"https://${branch.slug}.example.com"}]`}},
		{name: "no targets yet", viewer: maxHolding(auth.RoleReader), document: repo(`deliveryTargets { name }`), want: []string{`"deliveryTargets":[]`}},
	})
}
