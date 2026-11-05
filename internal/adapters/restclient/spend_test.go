package restclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/agents"
	"cobranch.dev/internal/services/harness"
)

// spendAPI is the REST API's report of a task's spend, as the token cba_valid alone, with its answers scripted.
type spendAPI struct {
	mu  sync.Mutex
	got []TaskUsage
	// status and body are its next answer; 0 records the report and answers what the task spent.
	status int
	body   TaskSpend
}

func (a *spendAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	var in TaskUsage
	switch {
	case r.Method != http.MethodPost || r.URL.Path != "/v1/me/task/spend":
		problem(w, http.StatusNotFound, "route.not_found", "No such route.")
	case r.Header.Get("Authorization") != "Bearer cba_valid":
		problem(w, http.StatusUnauthorized, "auth.unauthenticated", "The token is not valid.")
	case json.NewDecoder(r.Body).Decode(&in) != nil:
		problem(w, http.StatusBadRequest, "body.invalid", "Not a call.")
	case a.status == http.StatusConflict:
		problem(w, a.status, "task.ended", "The task has ended.")
	case a.status != 0:
		problem(w, a.status, "server.error", "It failed.")
	default:
		a.got = append(a.got, in)
		answer(w, http.StatusOK, a.body)
	}
}

func TestRecordSpend(t *testing.T) {
	call := agents.Usage{ID: uuid.New(), Model: "test-model", Input: 100_000, Output: 10_000}
	tt := []struct {
		name    string
		status  int
		body    TaskSpend
		token   string
		want    agents.Spend
		wantErr error
		wantAny bool
	}{
		{name: "under its cap", body: TaskSpend{Spent: "0.45", InputTokens: 100_000, OutputTokens: 10_000, Cap: ptr("1.00")},
			want: agents.Spend{Spent: 450_000, Input: 100_000, Output: 10_000, Cap: 1_000_000}},
		{name: "with no cap", body: TaskSpend{Spent: "0.45", InputTokens: 100_000, OutputTokens: 10_000},
			want: agents.Spend{Spent: 450_000, Input: 100_000, Output: 10_000}},
		{name: "the call that ends it", body: TaskSpend{Spent: "1.01", InputTokens: 100_000, OutputTokens: 10_000, Cap: ptr("1.00"), Ended: true},
			want: agents.Spend{Spent: 1_010_000, Input: 100_000, Output: 10_000, Cap: 1_000_000, Ended: true}},
		{name: "once it ended", status: http.StatusConflict, wantErr: agents.ErrEnded},
		{name: "a token that acts no more", token: "cba_spent", wantErr: harness.ErrRefused},
		{name: "a failure", status: http.StatusBadRequest, wantAny: true},
		{name: "an amount it can't read", body: TaskSpend{Spent: "lots"}, wantAny: true},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			a := &spendAPI{status: tc.status, body: tc.body}
			srv := httptest.NewServer(a)
			defer srv.Close()
			file := filepath.Join(t.TempDir(), "token")
			write(t, file, "cba_valid\n")
			if tc.token != "" {
				write(t, file, tc.token)
			}
			c, err := newClient(srv.URL, file, &retrier{client: srv.Client(), backoff: time.Millisecond})
			if err != nil {
				t.Fatal(err)
			}
			got, err := c.RecordSpend(context.Background(), call)
			switch {
			case tc.wantAny && err == nil, !tc.wantAny && !errors.Is(err, tc.wantErr), got != tc.want:
				t.Fatalf("RecordSpend() = %+v, %v; want %+v, %v", got, err, tc.want, tc.wantErr)
			case err == nil && (len(a.got) != 1 || a.got[0] != TaskUsage{Id: call.ID, Model: "test-model", InputTokens: 100_000, OutputTokens: 10_000}):
				t.Errorf("the API was sent %+v, want the call", a.got)
			}
		})
	}
}
