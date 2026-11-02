package rest

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
)

// logArchive is code.ArchivedLogs over a map of runs' logs, by run ID.
type logArchive map[uuid.UUID]map[string][]byte

func (a logArchive) Read(_ context.Context, _, runID uuid.UUID) (map[string][]byte, error) {
	return a[runID], nil
}

// withLogs keeps logs of #1, which withRuns adds, as it ended: its checkout's, and its script's, which isn't UTF-8.
func withLogs(viewer func(*orgFixture) *auth.Authenticated) func(*orgFixture) *auth.Authenticated {
	return withRuns(func(f *orgFixture) *auth.Authenticated {
		first := &f.codeStore.RunList[f.website.ID][0]
		started := first.QueuedAt
		first.StartedAt = &started
		f.code.WithRunLogs(logArchive{first.ID: {"main": []byte("built \xff\n"), "cobranch-checkout": []byte("checked out\n")}}, nil, 0)
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
				if len(got.Logs) != 2 || got.Logs[0] != (RunLog{Container: "cobranch-checkout", Text: "checked out\n"}) ||
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
