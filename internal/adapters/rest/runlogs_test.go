package rest

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/jobs"
)

// logArchive is code.ArchivedLogs over a map of runs' logs, by run ID.
type logArchive map[uuid.UUID]map[string][]byte

func (a logArchive) Read(_ context.Context, _, runID uuid.UUID) (map[string][]byte, error) {
	return a[runID], nil
}

// withLogs keeps the steps and logs of #1, which withRuns adds, as it ended: its checkout's, its lines with their
// times, and its script's, kept without them, which isn't UTF-8.
func withLogs(viewer func(*orgFixture) *auth.Authenticated) func(*orgFixture) *auth.Authenticated {
	return withRuns(func(f *orgFixture) *auth.Authenticated {
		first := &f.codeStore.RunList[f.website.ID][0]
		started := first.QueuedAt
		first.StartedAt = &started
		f.codeStore.Steps[first.ID] = []jobs.Step{{Name: "cobranch-checkout", State: jobs.StepSucceeded, StartedAt: &started, EndedAt: &started},
			{Name: "main", State: jobs.StepFailed, ExitCode: 2, Reason: "OOMKilled", StartedAt: &started}}
		f.code.WithRunLogs(logArchive{first.ID: {"main": []byte("built \xff\n"), "cobranch-checkout": []byte(checkedOutAt + " checked out\n")}}, nil, 0)
		if viewer == nil {
			return nil
		}
		return viewer(f)
	})
}

func TestRunLogsAPI(t *testing.T) {
	runOrgCases(t, []orgCase{
		{name: "a Reader reads a run's logs, the checkout's first", viewer: withLogs(maxReading), method: http.MethodGet, path: fixed(runsPath + "/1/logs"),
			wantStatus: 200, check: func(t *testing.T, _ *orgFixture, body []byte) {
				got := decode[RunLogs](t, body)
				if len(got.Logs) != 2 || got.Logs[0].Container != "cobranch-checkout" || got.Logs[0].Text != "checked out\n" || got.Logs[0].Times == nil ||
					len(*got.Logs[0].Times) != 1 || (*got.Logs[0].Times)[0].Format(time.RFC3339Nano) != checkedOutAt ||
					got.Logs[1] != (RunLog{Container: "main", Text: "built �\n"}) {
					t.Errorf("logs = %s", body)
				}
			}},
		{name: "none of a run that hasn't started", viewer: withLogs(maxReading), method: http.MethodGet, path: fixed(runsPath + "/3/logs"),
			wantStatus: 200, check: func(t *testing.T, _ *orgFixture, body []byte) {
				if got := decode[RunLogs](t, body); got.Logs == nil || len(got.Logs) != 0 {
					t.Errorf("logs = %s, want an empty list", body)
				}
			}},
		{name: "a run that isn't there", viewer: withLogs(maxReading), method: http.MethodGet, path: fixed(runsPath + "/9/logs"), wantStatus: 404},
		{name: "someone who can't read it", viewer: withLogs(nia), method: http.MethodGet, path: fixed(runsPath + "/1/logs"), wantStatus: 404},
		{name: "a container's, as UTF-8", viewer: withLogs(maxReading), method: http.MethodGet, path: fixed(runsPath + "/1/logs/main"), wantStatus: 200,
			wantHeader: http.Header{"Content-Type": {"text/plain; charset=utf-8"}, "X-Content-Type-Options": {"nosniff"}},
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				if string(body) != "built \uFFFD\n" {
					t.Errorf("log = %q", body)
				}
			}},
		{name: "a container that hasn't logged", viewer: withLogs(maxReading), method: http.MethodGet, path: fixed(runsPath + "/1/logs/sidecar"),
			wantStatus: 404, wantCode: "log.not_found"},
		{name: "anonymous", viewer: withLogs(nil), method: http.MethodGet, path: fixed(runsPath + "/1/logs/main"), wantStatus: 401},
	})
}

// checkedOutAt is when #1's checkout logged its one line, in UTC.
const checkedOutAt = "2026-11-01T07:00:03.5Z"

func TestRunStepsAPI(t *testing.T) {
	runOrgCases(t, []orgCase{
		{name: "a Reader reads an ended run's steps as they were kept", viewer: withLogs(maxReading), method: http.MethodGet, path: fixed(runsPath + "/1/steps"),
			wantStatus: 200, check: func(t *testing.T, _ *orgFixture, body []byte) {
				got := decode[RunSteps](t, body).Steps
				if len(got) != 2 || got[0].Name != "cobranch-checkout" || got[0].State != RunStepStateSucceeded || got[0].EndedAt == nil ||
					got[1].Name != "main" || got[1].State != RunStepStateFailed || got[1].ExitCode != 2 || got[1].Reason != "OOMKilled" ||
					got[1].StartedAt == nil || got[1].StartedAt.Location() != time.UTC || got[1].EndedAt != nil {
					t.Errorf("steps = %s", body)
				}
			}},
		{name: "none of a run that hasn't started", viewer: withLogs(maxReading), method: http.MethodGet, path: fixed(runsPath + "/3/steps"),
			wantStatus: 200, check: func(t *testing.T, _ *orgFixture, body []byte) {
				if got := decode[RunSteps](t, body); got.Steps == nil || len(got.Steps) != 0 {
					t.Errorf("steps = %s, want an empty list", body)
				}
			}},
		{name: "a run that isn't there", viewer: withLogs(maxReading), method: http.MethodGet, path: fixed(runsPath + "/9/steps"), wantStatus: 404},
		{name: "someone who can't read it", viewer: withLogs(nia), method: http.MethodGet, path: fixed(runsPath + "/1/steps"), wantStatus: 404},
		{name: "anonymous", viewer: withLogs(nil), method: http.MethodGet, path: fixed(runsPath + "/1/steps"), wantStatus: 401},
	})
}
