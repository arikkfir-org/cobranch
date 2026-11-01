package pushworker_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/adapters/pushworker"
	"cobranch.dev/internal/services/git"
)

// recorder answers what its fields say and keeps the last push it got.
type recorder struct {
	answer git.Answer
	err    error
	got    git.Push
	calls  atomic.Int32
}

func (r *recorder) Record(_ context.Context, push git.Push) (git.Answer, error) {
	r.calls.Add(1)
	r.got = push
	return r.answer, r.err
}

// push is a manifest with every field set, so a round trip shows what the wire loses.
func push() git.Push {
	at := time.Date(2026, 10, 31, 10, 0, 0, 0, time.FixedZone("+0200", 7200))
	return git.Push{ID: uuid.New(), RepoID: uuid.New(), PusherID: uuid.New(), Transport: git.TransportHTTPS, ReceivedAt: at.UTC(),
		Updates: []git.RefUpdate{{Name: "refs/heads/main", Old: git.ZeroSHA, New: strings.Repeat("a", 40)}},
		Pack:    &git.Pack{Name: "pack-" + strings.Repeat("b", 40), Size: 1234, Objects: 3},
		Commits: []git.Commit{{SHA: strings.Repeat("a", 40), Tree: strings.Repeat("c", 40), Parents: []string{}, Message: "Add README.md\n", Signature: "sig",
			Author:    git.Person{Name: "Ada", Email: "ada@example.com", At: at, Zone: "+0200"},
			Committer: git.Person{Name: "Olivia", Email: "olivia@example.com", At: at, Zone: "+0200"}}},
	}
}

func TestRoundTrip(t *testing.T) {
	r := &recorder{answer: git.Answer{Accepted: true}}
	srv := httptest.NewServer(pushworker.Handler("internal", r))
	defer srv.Close()
	want := push()
	answer, err := pushworker.NewClient(srv.URL+"/", "internal").Record(t.Context(), want)
	if err != nil || !answer.Accepted {
		t.Fatalf("Record() = %+v, %v", answer, err)
	}
	got := r.got
	if !got.ReceivedAt.Equal(want.ReceivedAt) || !got.Commits[0].Author.At.Equal(want.Commits[0].Author.At) {
		t.Errorf("the times arrived as %v and %v", got.ReceivedAt, got.Commits[0].Author.At)
	}
	got.ReceivedAt, got.Commits[0].Author.At, got.Commits[0].Committer.At = want.ReceivedAt, want.Commits[0].Author.At, want.Commits[0].Committer.At
	if !reflect.DeepEqual(got, want) {
		t.Errorf("the push worker got %+v, want %+v", got, want)
	}
}

func TestHandler(t *testing.T) {
	broken := errors.New("the database is down")
	tests := []struct {
		name, auth, body string
		answer           git.Answer
		err              error
		wantStatus       int
		wantBody         string
	}{
		{name: "accepted", auth: "Bearer internal", body: `{"id": "` + uuid.NewString() + `"}`, answer: git.Answer{Accepted: true},
			wantStatus: http.StatusOK, wantBody: `{"accepted":true}`},
		{name: "refused", auth: "bearer internal", body: `{}`, answer: git.Answer{Message: "moved"}, wantStatus: http.StatusOK, wantBody: `{"accepted":false,"message":"moved"}`},
		{name: "no token", body: `{}`, wantStatus: http.StatusUnauthorized},
		{name: "another token", auth: "Bearer intern", body: `{}`, wantStatus: http.StatusUnauthorized},
		{name: "a token as Basic", auth: "Basic internal", body: `{}`, wantStatus: http.StatusUnauthorized},
		{name: "not a manifest", auth: "Bearer internal", body: `[]`, wantStatus: http.StatusBadRequest},
		{name: "a field it doesn't know", auth: "Bearer internal", body: `{"force": true}`, wantStatus: http.StatusBadRequest},
		{name: "a push it couldn't record", auth: "Bearer internal", body: `{}`, err: broken, wantStatus: http.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &recorder{answer: tt.answer, err: tt.err}
			req := httptest.NewRequest(http.MethodPost, pushworker.Path, strings.NewReader(tt.body))
			if tt.auth != "" {
				req.Header.Set("Authorization", tt.auth)
			}
			w := httptest.NewRecorder()
			pushworker.Handler("internal", r).ServeHTTP(w, req)
			if w.Code != tt.wantStatus || (tt.wantBody != "" && strings.TrimSpace(w.Body.String()) != tt.wantBody) {
				t.Errorf("answered %d %q, want %d %q", w.Code, w.Body, tt.wantStatus, tt.wantBody)
			}
			if refused := tt.wantStatus == http.StatusUnauthorized || tt.wantStatus == http.StatusBadRequest; refused && r.calls.Load() != 0 {
				t.Errorf("the recorder was called %d times, want none", r.calls.Load())
			}
		})
	}
}

func TestHandlerWithoutAToken(t *testing.T) {
	// An empty token configured must never let an empty bearer in.
	req := httptest.NewRequest(http.MethodPost, pushworker.Path, strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer ")
	w := httptest.NewRecorder()
	pushworker.Handler("", &recorder{}).ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("answered %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestClientRetries(t *testing.T) {
	tests := []struct {
		name      string
		statuses  []int
		wantCalls int32
		wantErr   bool
	}{
		{name: "a 5xx, then the answer", statuses: []int{http.StatusBadGateway, http.StatusOK}, wantCalls: 2},
		{name: "a 4xx, which asking again won't fix", statuses: []int{http.StatusBadRequest}, wantCalls: 1, wantErr: true},
		{name: "5xx every time", statuses: []int{500, 500, 500, 500}, wantCalls: 4, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				n := calls.Add(1)
				status := tt.statuses[min(int(n), len(tt.statuses))-1]
				w.WriteHeader(status)
				if status == http.StatusOK {
					_, _ = w.Write([]byte(`{"accepted":true}`))
				}
			}))
			defer srv.Close()
			answer, err := pushworker.NewClient(srv.URL, "internal").Record(t.Context(), push())
			if (err != nil) != tt.wantErr || calls.Load() != tt.wantCalls || (!tt.wantErr && !answer.Accepted) {
				t.Errorf("Record() = %+v, %v after %d calls, want %d calls", answer, err, calls.Load(), tt.wantCalls)
			}
		})
	}
}

func TestClientStopsWithItsContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := pushworker.NewClient(srv.URL, "internal").Record(ctx, push()); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Record() = %v, want %v", err, context.DeadlineExceeded)
	}
	if took := time.Since(start); took > time.Second {
		t.Errorf("Record() took %v after its context ended", took)
	}
}
