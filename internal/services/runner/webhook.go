package runner

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/jobs"
)

// SignatureHeader carries a webhook post's signature: sha256= and the hex HMAC-SHA256 of its body, keyed with the
// value of the job's signing secret.
const SignatureHeader = "X-Cobranch-Signature"

// firstRetry is how long a webhook run waits after its first failed post; each wait after is about as long as all the
// waits before it, so they double.
const firstRetry = time.Second

// ErrUnreachable is a webhook whose address webhooks may not reach: one in a range runs may not reach, such as the
// cluster's own or a cloud's metadata server. No retry changes that, so its run fails, naming the address.
var ErrUnreachable = errors.New("webhooks may not reach the address")

// Poster posts webhook runs to their receivers: the webhook adapter's.
type Poster interface {
	// Post posts body to url with headers, without following a redirect, and answers nil once the receiver answers
	// 2xx; an error otherwise, ErrUnreachable for an address webhooks may not reach.
	Post(ctx context.Context, url string, body []byte, headers map[string]string) error
}

// Webhook is what a webhook run posts its receiver: what the run is, its token, and where to report with it.
type Webhook struct {
	Run            WebhookRun      `json:"run"`
	Job            string          `json:"job"`
	Organization   string          `json:"organization"`
	Repository     string          `json:"repository"`
	SHA            string          `json:"sha"`
	Ref            string          `json:"ref"`
	Event          json.RawMessage `json:"event"`
	Token          string          `json:"token"`
	TokenExpiresAt time.Time       `json:"tokenExpiresAt"`
	// URL is the REST API's, which the receiver reports to with the token.
	URL string `json:"url"`
}

// WebhookRun names the run a webhook posts.
type WebhookRun struct {
	ID     uuid.UUID `json:"id"`
	Number int64     `json:"number"`
}

// deliver posts a claimed webhook run to its receiver, signed with its job's signing secret and carrying a token of
// its own: a 2xx marks it running, until its receiver reports through its job's annotation or its timeout ends it. A
// failed post is tried again, a second later and then about twice as long after each, until the run's timeout, counted
// from when it was queued, has passed.
func (s *Service) deliver(ctx context.Context, run Run) error {
	hook, timeout := run.Definition.Webhook, s.timeout(run.Definition)
	if !s.now().Before(run.QueuedAt.Add(timeout)) {
		return s.fail(ctx, run.ID, "its webhook's receiver answered no post with a 2xx within its timeout")
	}
	values, refusal, err := s.receive(ctx, run, []uuid.UUID{hook.SigningSecret})
	if err != nil || refusal != "" {
		return errors.Join(err, s.failIf(ctx, run.ID, refusal))
	}
	token := auth.NewSecret(auth.PrefixRunToken)
	expires := s.now().Add(timeout + tokenSlack)
	if err := s.store.Token(ctx, run.ID, auth.Hash(token), auth.Hint(token), expires); err != nil {
		return fmt.Errorf("recording run %s's token: %w", run.ID, err)
	}
	body, err := json.Marshal(Webhook{Run: WebhookRun{ID: run.ID, Number: run.Number}, Job: run.Job, Organization: run.OrgSlug,
		Repository: run.RepoSlug, SHA: run.SHA, Ref: run.Ref, Event: eventOrNull(run.Event), Token: token, TokenExpiresAt: expires,
		URL: s.settings.RESTURL})
	if err != nil {
		return fmt.Errorf("encoding run %s's webhook: %w", run.ID, err)
	}
	headers := map[string]string{"Content-Type": "application/json", SignatureHeader: Sign(values[hook.SigningSecret], body)}
	switch err := s.poster.Post(ctx, hook.URL, body, headers); {
	case errors.Is(err, ErrUnreachable):
		return s.fail(ctx, run.ID, "its webhook: "+err.Error())
	case err != nil:
		return s.retry(ctx, run, timeout, err)
	}
	if _, err := s.store.MarkRunning(ctx, run.ID); err != nil {
		// A run a member cancelled meanwhile stays cancelled: its token goes with the next sweep.
		return fmt.Errorf("marking run %s running: %w", run.ID, err)
	}
	return nil
}

// retry puts a webhook run whose post failed off until its next try: as long as it has waited since it was queued, at
// least firstRetry, and no later than its timeout, when the next claim fails it.
func (s *Service) retry(ctx context.Context, run Run, timeout time.Duration, cause error) error {
	now := s.now()
	next := now.Add(max(now.Sub(run.QueuedAt), firstRetry))
	if deadline := run.QueuedAt.Add(timeout); next.After(deadline) {
		next = deadline
	}
	reason := fmt.Sprintf("its webhook's receiver failed (%s): trying again at %s", cause, next.UTC().Format(time.RFC3339))
	if err := s.store.Defer(ctx, run.ID, next, reason); err != nil {
		return fmt.Errorf("putting run %s off: %w", run.ID, err)
	}
	return nil
}

// Sign is a webhook post's signature: sha256= and the hex HMAC-SHA256 of body keyed with secret, which a receiver
// computes again to know the post came from Cobranch.
func Sign(secret, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write(body) // a hash.Hash never fails a write
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// eventOrNull is a run's event as JSON: null when it recorded none.
func eventOrNull(event json.RawMessage) json.RawMessage {
	if len(event) == 0 {
		return json.RawMessage("null")
	}
	return event
}

// expireWebhooks fails the running webhook runs whose receivers didn't report within their timeouts, counted from
// when they were delivered.
func (s *Service) expireWebhooks(ctx context.Context) error {
	running, err := s.store.RunningWebhooks(ctx)
	if err != nil {
		return fmt.Errorf("listing the running webhook runs: %w", err)
	}
	var errs []error
	for _, r := range running {
		if s.now().Before(r.StartedAt.Add(s.timeout(jobs.Job{Timeout: jobs.Duration(r.Timeout)}))) {
			continue
		}
		if _, err := s.store.Finish(ctx, r.ID, jobs.StateFailed, "its webhook's receiver didn't report within its timeout"); err != nil {
			errs = append(errs, fmt.Errorf("ending run %s: %w", r.ID, err))
		}
	}
	return errors.Join(errs...)
}

// failIf fails a run naming why, unless there is no why.
func (s *Service) failIf(ctx context.Context, runID uuid.UUID, reason string) error {
	if reason == "" {
		return nil
	}
	return s.fail(ctx, runID, reason)
}
