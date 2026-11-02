package models

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"cobranch.dev/internal/services/harness"
)

// Retries of a provider's request.
const (
	// attempts is how many times a request is sent before its failure stands.
	attempts = 6
	// maxWait is the longest wait before a retry, whatever the provider asks.
	maxWait = time.Minute
	// maxAnswer is the most bytes of a provider's answer read.
	maxAnswer = 32 << 20
)

// caller sends a provider's requests, retrying those a retry may get past.
type caller struct {
	provider string
	client   *http.Client
	backoff  time.Duration
}

// post sends body as JSON to url with the headers, and decodes the provider's answer into out. It retries a failure to
// connect and an answer a retry may get past, the provider's overload or rate limit, waiting as long as it asks; an
// answer it would give again fails with harness.ErrModelRefused.
func (c caller) post(ctx context.Context, url string, headers map[string]string, body, out any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("writing the request to %s: %w", c.provider, err)
	}
	for attempt := 1; ; attempt++ {
		status, answer, after, err := c.send(ctx, url, headers, raw)
		switch {
		case err == nil && status/100 == 2:
			if err := json.Unmarshal(answer, out); err != nil {
				return fmt.Errorf("reading %s's answer: %w", c.provider, err)
			}
			return nil
		case ctx.Err() != nil:
			return fmt.Errorf("asking %s: %w", c.provider, ctx.Err())
		case attempt < attempts && (err != nil || retryable(status)):
			if err := sleep(ctx, c.wait(attempt, after)); err != nil {
				return fmt.Errorf("waiting to ask %s again: %w", c.provider, err)
			}
		case err != nil:
			return fmt.Errorf("asking %s: %w", c.provider, err)
		default:
			return c.refusal(status, answer)
		}
	}
}

// send sends one request, and answers the provider's status, its answer and how long it asks a retry to wait.
func (c caller) send(ctx context.Context, url string, headers map[string]string, body []byte) (int, []byte, time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return 0, nil, 0, fmt.Errorf("writing the request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return 0, nil, 0, fmt.Errorf("sending the request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }() // read whole, or abandoned on an error
	answer, err := io.ReadAll(io.LimitReader(resp.Body, maxAnswer))
	if err != nil {
		return 0, nil, 0, fmt.Errorf("reading the answer: %w", err)
	}
	after, _ := strconv.Atoi(resp.Header.Get("Retry-After")) // seconds, when given; a date or nothing waits the backoff
	return resp.StatusCode, answer, time.Duration(after) * time.Second, nil
}

// retryable reports whether a provider's answer with status may differ when asked again: a timeout, a rate limit, its
// overload or its own failure.
func retryable(status int) bool {
	switch status {
	case http.StatusRequestTimeout, http.StatusConflict, http.StatusTooEarly, http.StatusTooManyRequests, 529:
		return true
	}
	return status >= 500
}

// wait is how long to wait before the next attempt: what the provider asked, or the backoff doubled each attempt, and
// never more than maxWait.
func (c caller) wait(attempt int, asked time.Duration) time.Duration {
	if asked > 0 {
		return min(asked, maxWait)
	}
	return min(c.backoff<<(attempt-1), maxWait)
}

// sleep waits d, or until ctx ends.
func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return fmt.Errorf("waiting: %w", ctx.Err())
	case <-t.C:
		return nil
	}
}

// refusal is the failure of a request the provider answered with status and answer: harness.ErrModelRefused for one
// it would answer the same again, with its message.
func (c caller) refusal(status int, answer []byte) error {
	var problem struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	message := string(answer[:min(len(answer), 300)])
	if json.Unmarshal(answer, &problem) == nil && problem.Error.Message != "" {
		message = problem.Error.Message
	}
	if retryable(status) {
		return fmt.Errorf("%s answered %d: %s", c.provider, status, message)
	}
	return fmt.Errorf("%w (%s, %d): %s", harness.ErrModelRefused, c.provider, status, message)
}
