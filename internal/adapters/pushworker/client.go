package pushworker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"cobranch.dev/internal/services/git"
)

// attempts is how many times the client asks the push worker before it gives up on a push.
const attempts = 4

// firstRetry is how long the client waits before it asks again; each wait after doubles it.
const firstRetry = 200 * time.Millisecond

// Client is the Git server's client of the push worker.
type Client struct {
	url   string
	token string
	http  *http.Client
}

// NewClient returns a client of the push worker at baseURL, bearing token.
func NewClient(baseURL, token string) *Client {
	return &Client{url: strings.TrimSuffix(baseURL, "/") + Path, token: token,
		http: &http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport)}}
}

// Record sends the push's manifest and returns the push worker's answer. When the call fails or the push worker
// answers 5xx, it asks again with the same push ID, which the push worker answers as it did the first time; it gives
// up after a few tries, or when ctx ends.
func (c *Client) Record(ctx context.Context, push git.Push) (git.Answer, error) {
	body, err := json.Marshal(toWire(push))
	if err != nil {
		return git.Answer{}, fmt.Errorf("encoding push %s: %w", push.ID, err)
	}
	wait := firstRetry
	var last error
	for attempt := range attempts {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return git.Answer{}, fmt.Errorf("asking the push worker about push %s: %w (after %w)", push.ID, ctx.Err(), last)
			case <-time.After(wait):
			}
			wait *= 2
		}
		answer, retry, err := c.call(ctx, body)
		if err == nil {
			return answer, nil
		}
		if !retry {
			return git.Answer{}, fmt.Errorf("asking the push worker about push %s: %w", push.ID, err)
		}
		last = err
	}
	return git.Answer{}, fmt.Errorf("asking the push worker about push %s, %d times: %w", push.ID, attempts, last)
}

// call posts the manifest once, and says whether a failure is worth asking again.
func (c *Client) call(ctx context.Context, body []byte) (git.Answer, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return git.Answer{}, false, fmt.Errorf("making the request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.http.Do(req)
	if err != nil {
		return git.Answer{}, ctx.Err() == nil, fmt.Errorf("calling the push worker: %w", err)
	}
	defer func() { _ = resp.Body.Close() }() // read whole below, or abandoned on an error
	if resp.StatusCode != http.StatusOK {
		text, _ := io.ReadAll(io.LimitReader(resp.Body, 1024)) // the status says enough when the body doesn't come
		return git.Answer{}, resp.StatusCode >= 500, fmt.Errorf("the push worker answered %s: %s", resp.Status, strings.TrimSpace(string(text)))
	}
	var answer answerJSON
	if err := json.NewDecoder(resp.Body).Decode(&answer); err != nil {
		return git.Answer{}, !errors.Is(err, context.Canceled), fmt.Errorf("reading the push worker's answer: %w", err)
	}
	return git.Answer(answer), false, nil
}
