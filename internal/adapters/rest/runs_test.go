package rest

import (
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/jobs"
)

// withRuns adds three runs of ci/build to website before the viewer's setup: #1 at b, succeeded; #2 at c, waiting for
// a member; #3 at c, queued.
func withRuns(viewer func(*orgFixture) *auth.Authenticated) func(*orgFixture) *auth.Authenticated {
	return func(f *orgFixture) *auth.Authenticated {
		job := code.Principal{ID: uuid.New(), DisplayName: "ci/build"}
		def := jobs.Job{Name: "ci/build", Script: "make test", Permissions: jobs.DefaultPermissions}
		at := time.Date(2026, 11, 1, 9, 0, 0, 0, time.FixedZone("IST", 2*3600))
		for _, r := range []code.Run{
			{SHA: shaOf("b"), State: jobs.StateSucceeded},
			{SHA: shaOf("c"), State: jobs.StateWaiting, Reason: "waits for a member to start it"},
			{SHA: shaOf("c"), State: jobs.StateQueued},
		} {
			r.Job, r.Ref, r.Definition, r.QueuedAt = job, "refs/heads/main", def, at
			r.Trigger = jobs.Event{Kind: jobs.EventPush, SHA: r.SHA, Ref: r.Ref}
			f.codeStore.AddRun(f.website.ID, r)
		}
		if viewer == nil {
			return nil
		}
		return viewer(f)
	}
}

const runsPath = "/v1/orgs/acme/repos/website/runs"

func TestListRunsAPI(t *testing.T) {
	runOrgCases(t, []orgCase{
		{name: "a Reader lists them, newest first", viewer: withRuns(maxReading), method: http.MethodGet, path: fixed(runsPath), wantStatus: 200,
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				got := decode[struct{ Runs []Run }](t, body)
				if len(got.Runs) != 3 || got.Runs[0].Number != 3 || got.Runs[2].Number != 1 {
					t.Fatalf("runs = %s", body)
				}
				r := got.Runs[1]
				if r.State != RunStateWaiting || r.Reason != "waits for a member to start it" || r.Job.DisplayName != "ci/build" ||
					r.Definition.Kind != "script" || r.Definition.Script == nil || *r.Definition.Script != "make test" ||
					r.Trigger.Kind != "push" || r.Trigger.Ref != "refs/heads/main" ||
					r.QueuedAt.Location() != time.UTC || r.Version != 1 {
					t.Errorf("run = %+v", r)
				}
			}},
		{name: "at a commit", viewer: withRuns(maxReading), method: http.MethodGet, path: fixed(runsPath + "?sha=" + shaOf("b")), wantStatus: 200,
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				if got := decode[struct{ Runs []Run }](t, body); len(got.Runs) != 1 || got.Runs[0].Number != 1 {
					t.Errorf("runs = %s", body)
				}
			}},
		{name: "a page links the next", viewer: withRuns(maxReading), method: http.MethodGet, path: fixed(runsPath + "?limit=2"), wantStatus: 200,
			wantHeader: http.Header{"Link": {`<?cursor=2&limit=2>; rel="next"`}}, check: func(t *testing.T, _ *orgFixture, body []byte) {
				if got := decode[struct{ Runs []Run }](t, body); len(got.Runs) != 2 || got.Runs[1].Number != 2 {
					t.Errorf("runs = %s", body)
				}
			}},
		{name: "the next page", viewer: withRuns(maxReading), method: http.MethodGet, path: fixed(runsPath + "?limit=2&cursor=2"), wantStatus: 200,
			wantHeader: http.Header{"Link": {""}}, check: func(t *testing.T, _ *orgFixture, body []byte) {
				if got := decode[struct{ Runs []Run }](t, body); len(got.Runs) != 1 || got.Runs[0].Number != 1 {
					t.Errorf("runs = %s", body)
				}
			}},
		{name: "a cursor of nothing", viewer: withRuns(maxReading), method: http.MethodGet, path: fixed(runsPath + "?cursor=next"), wantStatus: 400, wantCode: "cursor.invalid"},
		{name: "a commit that isn't one", viewer: withRuns(maxReading), method: http.MethodGet, path: fixed(runsPath + "?sha=main"), wantStatus: 400},
		{name: "too many a page", viewer: withRuns(maxReading), method: http.MethodGet, path: fixed(runsPath + "?limit=101"), wantStatus: 400},
		{name: "someone who can't read it", viewer: withRuns(nia), method: http.MethodGet, path: fixed(runsPath), wantStatus: 404},
		{name: "anonymous", viewer: withRuns(nil), method: http.MethodGet, path: fixed(runsPath), wantStatus: 401, wantCode: "auth.unauthenticated"},
	})
}

func TestRunAPI(t *testing.T) {
	runOrgCases(t, []orgCase{
		{name: "one by number", viewer: withRuns(maxReading), method: http.MethodGet, path: fixed(runsPath + "/2"), wantStatus: 200,
			wantHeader: http.Header{"Etag": {`"1"`}}, check: func(t *testing.T, _ *orgFixture, body []byte) {
				if r := decode[Run](t, body); r.Number != 2 || r.State != RunStateWaiting {
					t.Errorf("run = %s", body)
				}
			}},
		{name: "one that isn't there", viewer: withRuns(maxReading), method: http.MethodGet, path: fixed(runsPath + "/9"), wantStatus: 404},
		{name: "a Developer cancels one", viewer: withRuns(maxDeveloping), method: http.MethodPost, path: fixed(runsPath + "/3/cancel"), wantStatus: 200,
			wantHeader: http.Header{"Etag": {`"2"`}}, check: func(t *testing.T, _ *orgFixture, body []byte) {
				if r := decode[Run](t, body); r.State != RunStateCancelled || r.Reason != "cancelled by Max" || r.EndedAt == nil {
					t.Errorf("run = %s", body)
				}
			}},
		{name: "one that has ended", viewer: withRuns(maxDeveloping), method: http.MethodPost, path: fixed(runsPath + "/1/cancel"), wantStatus: 409, wantCode: "run.ended"},
		{name: "a Reader may not cancel", viewer: withRuns(maxReading), method: http.MethodPost, path: fixed(runsPath + "/3/cancel"), wantStatus: 403},
		{name: "a Developer starts a waiting one", viewer: withRuns(maxDeveloping), method: http.MethodPost, path: fixed(runsPath + "/2/start"), wantStatus: 202,
			wantHeader: http.Header{"Location": {runsPath + "/2"}, "Etag": {`"2"`}}, check: func(t *testing.T, f *orgFixture, body []byte) {
				if r := decode[Run](t, body); r.State != RunStateQueued || r.StartedBy == nil || r.StartedBy.Id != f.max.User.ID || r.StartedBy.DisplayName != "Max" {
					t.Errorf("run = %s", body)
				}
			}},
		{name: "one that doesn't wait", viewer: withRuns(maxDeveloping), method: http.MethodPost, path: fixed(runsPath + "/3/start"), wantStatus: 409, wantCode: "run.not_waiting"},
		{name: "a Reader may not start", viewer: withRuns(maxReading), method: http.MethodPost, path: fixed(runsPath + "/2/start"), wantStatus: 403},
		{name: "starting one that isn't there", viewer: withRuns(maxDeveloping), method: http.MethodPost, path: fixed(runsPath + "/9/start"), wantStatus: 404},
	})
}
