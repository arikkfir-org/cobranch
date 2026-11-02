package runner_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/jobs"
	"cobranch.dev/internal/services/runner"
)

// webhookRun is a run of webhook job deploy, queued ago before now.
func webhookRun(ago time.Duration) runner.Run {
	job := jobs.Job{Name: "deploy", Permissions: jobs.DefaultPermissions,
		Webhook: &jobs.Webhook{URL: "https://hooks.example.com/cobranch", SigningSecret: id("5160")}}
	return runner.Run{ID: id("aaaa"), OrgID: id("0001"), RepoID: id("0002"), JobID: id("70b0"), Number: 7, Job: job.Name,
		OrgSlug: "acme", RepoSlug: "api", SHA: "abc123", Ref: "refs/heads/main", Event: json.RawMessage(`{"kind":"push"}`),
		Definition: job, QueuedAt: now.Add(-ago)}
}

func TestDeliver(t *testing.T) {
	claimed := "claimed #7 until 09:02:00"
	tests := []struct {
		name       string
		run        runner.Run
		postErr    error
		vault      vault
		pods       bool
		wantEvents []string
		wantPosts  int
	}{
		{name: "a receiver that answers 2xx", run: webhookRun(0), pods: true, wantEvents: []string{claimed, "running"}, wantPosts: 1},
		{name: "an installation that runs no pods", run: webhookRun(0), wantEvents: []string{claimed, "running"}, wantPosts: 1},
		{name: "the first failed post", run: webhookRun(0), postErr: errors.New("hooks.example.com answered 503 Service Unavailable"),
			wantEvents: []string{claimed, "deferred until 09:00:01: its webhook's receiver failed (hooks.example.com answered 503 Service Unavailable): trying again at 2026-11-02T09:00:01Z"},
			wantPosts:  1},
		{name: "a later failed post waits as long as all before it", run: webhookRun(15 * time.Second), postErr: errors.New("timeout"),
			wantEvents: []string{claimed, "deferred until 09:00:15: its webhook's receiver failed (timeout): trying again at 2026-11-02T09:00:15Z"},
			wantPosts:  1},
		{name: "the last try comes at its timeout", run: webhookRun(59*time.Minute + 50*time.Second), postErr: errors.New("timeout"),
			wantEvents: []string{claimed, "deferred until 09:00:10: its webhook's receiver failed (timeout): trying again at 2026-11-02T09:00:10Z"},
			wantPosts:  1},
		{name: "past its timeout", run: webhookRun(time.Hour),
			wantEvents: []string{claimed, "failed: its webhook's receiver answered no post with a 2xx within its timeout"}},
		{name: "an address webhooks may not reach", run: webhookRun(0), postErr: fmt.Errorf("posting to hooks.example.com: %w: 10.0.0.7, in 10.0.0.0/8", runner.ErrUnreachable),
			wantEvents: []string{claimed, "failed: its webhook: posting to hooks.example.com: webhooks may not reach the address: 10.0.0.7, in 10.0.0.0/8"},
			wantPosts:  1},
		{name: "a signing secret its job may not read", run: webhookRun(0), vault: vault{refused: id("5160")},
			wantEvents: []string{claimed, "failed: job deploy may not read secret 51600000-0000-7000-8000-000000000000: grant it Secret reader on it or on the repository"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &store{queued: []runner.Run{tt.run}, tokens: map[uuid.UUID]time.Time{}}
			if tt.vault.values == nil {
				tt.vault.values = map[uuid.UUID][]byte{id("5160"): []byte("signing-key")}
			}
			p := &poster{err: tt.postErr}
			set := settings
			set.Pods = tt.pods
			svc := runner.NewService(s, &cluster{}, tt.vault, archive{}, p, set)
			svc.SetClock(func() time.Time { return now })
			if ok, err := svc.Dispatch(context.Background()); !ok || err != nil {
				t.Fatalf("Dispatch() = %t, %v", ok, err)
			}
			if !slices.Equal(s.events, tt.wantEvents) {
				t.Errorf("events = %q, want %q", s.events, tt.wantEvents)
			}
			if len(p.posts) != tt.wantPosts {
				t.Fatalf("posted %d times, want %d", len(p.posts), tt.wantPosts)
			}
			if tt.wantPosts > 0 {
				checkPost(t, p.posts[0], s)
			}
		})
	}
}

// checkPost checks what a webhook run posted: to its job's URL, signed with its signing secret, naming the run and
// carrying the token the store recorded, which lasts the run's timeout and a while.
func checkPost(t *testing.T, got post, s *store) {
	t.Helper()
	if got.url != "https://hooks.example.com/cobranch" || got.headers["Content-Type"] != "application/json" {
		t.Errorf("posted to %s with %v", got.url, got.headers)
	}
	if want := runner.Sign([]byte("signing-key"), got.body); got.headers[runner.SignatureHeader] != want {
		t.Errorf("signature %q, want %q", got.headers[runner.SignatureHeader], want)
	}
	var body runner.Webhook
	if err := json.Unmarshal(got.body, &body); err != nil {
		t.Fatal(err)
	}
	if body.Run.ID != id("aaaa") || body.Run.Number != 7 || body.Job != "deploy" || body.Organization != "acme" || body.Repository != "api" ||
		body.SHA != "abc123" || body.Ref != "refs/heads/main" || string(body.Event) != `{"kind":"push"}` || body.URL != "https://api.example.com/v1" {
		t.Errorf("posted %s", got.body)
	}
	if !strings.HasPrefix(body.Token, "cbr_") || !body.TokenExpiresAt.Equal(now.Add(time.Hour+15*time.Minute)) ||
		!s.tokens[id("aaaa")].Equal(body.TokenExpiresAt) {
		t.Errorf("token %q until %s, the store's until %s", body.Token, body.TokenExpiresAt, s.tokens[id("aaaa")])
	}
}

func TestSignIsHMACSHA256(t *testing.T) {
	// RFC 4231, test case 2.
	got := runner.Sign([]byte("Jefe"), []byte("what do ya want for nothing?"))
	if want := "sha256=5bdcc146bf60754e6a042426089575c75a003f089d2739839dec58b964ec3843"; got != want {
		t.Errorf("Sign() = %q, want %q", got, want)
	}
}

func TestFollowExpiresWebhooks(t *testing.T) {
	late := runner.Running{ID: id("1a7e"), OrgID: id("0001"), RepoID: id("0002"), Number: 3, StartedAt: now.Add(-61 * time.Minute)}
	ownTimeout := runner.Running{ID: id("0a7e"), OrgID: id("0001"), RepoID: id("0002"), Number: 4, StartedAt: now.Add(-61 * time.Minute),
		Timeout: 2 * time.Hour}
	recent := runner.Running{ID: id("2ece"), OrgID: id("0001"), RepoID: id("0002"), Number: 5, StartedAt: now.Add(-10 * time.Minute)}
	s := &store{webhooks: []runner.Running{late, ownTimeout, recent}, tokens: map[uuid.UUID]time.Time{}}
	svc := runner.NewService(s, &cluster{}, vault{}, archive{}, &poster{}, settings)
	svc.SetClock(func() time.Time { return now })
	if err := svc.Follow(context.Background()); err != nil {
		t.Fatal(err)
	}
	if want := []string{"failed: its webhook's receiver didn't report within its timeout"}; !slices.Equal(s.events, want) {
		t.Errorf("events = %q, want %q", s.events, want)
	}
}
