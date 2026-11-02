package rest

import (
	"net/http"
	"strings"
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
		{name: "one the caller holds", viewer: withRuns(maxReading), method: http.MethodGet, path: fixed(runsPath + "/2"),
			header: http.Header{"If-None-Match": {`"1"`}}, wantStatus: 304, wantHeader: http.Header{"Etag": {`"1"`}}},
		{name: "one the caller holds an older version of", viewer: withRuns(maxReading), method: http.MethodGet, path: fixed(runsPath + "/2"),
			header: http.Header{"If-None-Match": {`"0"`}}, wantStatus: 200, wantHeader: http.Header{"Etag": {`"1"`}}},
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

// withJobs gives website a main at c whose /cobranch.yaml declares deploy, which members start by hand or by event
// deploy/requested, and ci/build, on pushes, before the viewer's setup, with withRuns' runs.
func withJobs(viewer func(*orgFixture) *auth.Authenticated) func(*orgFixture) *auth.Authenticated {
	return withRuns(func(f *orgFixture) *auth.Authenticated {
		f.codeStore.BranchList[f.website.ID] = append(f.codeStore.BranchList[f.website.ID], code.Branch{Name: "main", Head: shaOf("c")})
		f.codeStore.Defined[f.website.ID] = map[string][]jobs.Job{"main": {
			{Name: "deploy", Script: "make deploy", On: jobs.Triggers{Manual: &jobs.ManualTrigger{}, Event: &jobs.EventTrigger{Names: []string{"deploy/requested"}}}},
			{Name: "ci/build", Script: "make test", On: jobs.Triggers{Push: &jobs.PushTrigger{}}},
		}}
		return viewer(f)
	})
}

func TestRunTriggersAPI(t *testing.T) {
	const repoPath = "/v1/orgs/acme/repos/website"
	runOrgCases(t, []orgCase{
		{name: "a Developer re-runs an ended run", viewer: withRuns(maxDeveloping), method: http.MethodPost, path: fixed(runsPath + "/1/rerun"),
			wantStatus: 201, wantHeader: http.Header{"Location": {runsPath + "/4"}, "Etag": {`"1"`}}, check: func(t *testing.T, f *orgFixture, body []byte) {
				r := decode[Run](t, body)
				if r.Number != 4 || r.Attempt != 2 || r.RerunOf == nil || *r.RerunOf != 1 || r.State != RunStateQueued || r.StartedBy == nil || r.StartedBy.Id != f.max.User.ID {
					t.Errorf("run = %s", body)
				}
			}},
		{name: "re-running one that hasn't ended", viewer: withRuns(maxDeveloping), method: http.MethodPost, path: fixed(runsPath + "/3/rerun"),
			wantStatus: 409, wantCode: "run.active"},
		{name: "a Reader may not re-run", viewer: withRuns(maxReading), method: http.MethodPost, path: fixed(runsPath + "/1/rerun"), wantStatus: 403},
		{name: "a Developer starts a manual job", viewer: withJobs(maxDeveloping), method: http.MethodPost, path: fixed(repoPath + "/branches/main/jobs/deploy/runs"),
			wantStatus: 201, wantHeader: http.Header{"Location": {runsPath + "/4"}}, check: func(t *testing.T, _ *orgFixture, body []byte) {
				r := decode[Run](t, body)
				if r.Trigger.Kind != RunTriggerKindManual || r.Trigger.Name == nil || *r.Trigger.Name != "deploy" || r.Sha != shaOf("c") || r.Attempt != 1 {
					t.Errorf("run = %s", body)
				}
			}},
		{name: "a job with a slash in its name", viewer: withJobs(maxDeveloping), method: http.MethodPost, path: fixed(repoPath + "/branches/main/jobs/ci%2Fbuild/runs"),
			wantStatus: 409, wantCode: "job.not_manual"},
		{name: "a job nobody declares", viewer: withJobs(maxDeveloping), method: http.MethodPost, path: fixed(repoPath + "/branches/main/jobs/nope/runs"), wantStatus: 404},
		{name: "a Reader may not start a job", viewer: withJobs(maxReading), method: http.MethodPost, path: fixed(repoPath + "/branches/main/jobs/deploy/runs"), wantStatus: 403},
		{name: "a Developer posts an event", viewer: withJobs(maxDeveloping), method: http.MethodPost, path: fixed(repoPath + "/events"),
			body: `{"name":"deploy/requested","data":{"env":"production"}}`, wantStatus: 202,
			wantHeader: http.Header{"Location": {runsPath + "?sha=" + shaOf("c")}}, check: func(t *testing.T, _ *orgFixture, body []byte) {
				e := decode[PostedEvent](t, body)
				if len(e.Runs) != 1 || e.Runs[0].Definition.Name != "deploy" || e.Runs[0].Trigger.Data == nil {
					t.Errorf("event = %s", body)
				}
			}},
		{name: "an event no job follows", viewer: withJobs(maxDeveloping), method: http.MethodPost, path: fixed(repoPath + "/events"),
			body: `{"name":"other"}`, wantStatus: 202, wantHeader: http.Header{"Location": {runsPath}}, check: func(t *testing.T, _ *orgFixture, body []byte) {
				if e := decode[PostedEvent](t, body); len(e.Runs) != 0 {
					t.Errorf("event = %s", body)
				}
			}},
		{name: "an event on a branch that isn't there", viewer: withJobs(maxDeveloping), method: http.MethodPost, path: fixed(repoPath + "/events"),
			body: `{"name":"deploy/requested","branch":"gone"}`, wantStatus: 400, wantCode: "branch.unknown"},
		{name: "an event's name", viewer: withJobs(maxDeveloping), method: http.MethodPost, path: fixed(repoPath + "/events"),
			body: `{"name":"Deploy!"}`, wantStatus: 400, wantCode: "name.invalid"},
		{name: "too much data", viewer: withJobs(maxDeveloping), method: http.MethodPost, path: fixed(repoPath + "/events"),
			body: `{"name":"deploy/requested","data":"` + strings.Repeat("x", 64<<10) + `"}`, wantStatus: 400, wantCode: "data.invalid"},
		{name: "a Reader may not post", viewer: withJobs(maxReading), method: http.MethodPost, path: fixed(repoPath + "/events"),
			body: `{"name":"deploy/requested"}`, wantStatus: 403},
	})
}
