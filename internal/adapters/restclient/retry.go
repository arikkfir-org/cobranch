package restclient

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Retries of a call of the API, which are safe to repeat: the harness reads, and appends entries that name their
// numbers, so a repeat answers the entry already there, and the model proxy reports calls by their IDs, which count
// once. A call sent once, such as a review's submission, is the exception.
const (
	attempts       = 6
	defaultBackoff = time.Second
	maxBackoff     = 30 * time.Second
)

// retrier sends a request through client, again after a failure to connect or an answer a retry may get past: the API
// rolling out, overloaded or limiting the rate of the task's calls.
type retrier struct {
	client  *http.Client
	backoff time.Duration
}

// retrying returns client, nil for a default one, retrying what a retry may get past.
func retrying(client *http.Client) *retrier {
	if client == nil {
		client = &http.Client{}
	}
	return &retrier{client: client, backoff: defaultBackoff}
}

// onceKey marks the context of a request sent once (once).
type onceKey struct{}

// once is ctx for a request that acts each time the API takes it, so a retry after an answer that got lost would act
// twice: it is sent again only after an answer the API gave without acting, too many requests.
func once(ctx context.Context) context.Context {
	return context.WithValue(ctx, onceKey{}, true)
}

// Do sends req until an answer stands or its attempts run out, waiting longer before each (HttpRequestDoer).
func (r *retrier) Do(req *http.Request) (*http.Response, error) {
	wait, single := r.backoff, req.Context().Value(onceKey{}) != nil
	for attempt := 1; ; attempt++ {
		resp, err := r.client.Do(req)
		if attempt == attempts || !retry(resp, err, single) || req.Context().Err() != nil {
			if err != nil {
				return nil, fmt.Errorf("calling the API: %w", err)
			}
			return resp, nil
		}
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body) // drained for the connection's reuse; a failure only costs it
			_ = resp.Body.Close()                 // as above
		}
		if req, err = again(req); err != nil {
			return nil, err
		}
		select {
		case <-req.Context().Done():
			return nil, fmt.Errorf("waiting to call the API again: %w", req.Context().Err())
		case <-time.After(wait):
		}
		wait = min(wait*2, maxBackoff)
	}
}

// retry reports whether to send a request again after its answer resp, or its failure err: for a request sent once,
// only after an answer the API gave without acting on it.
func retry(resp *http.Response, err error, single bool) bool {
	switch {
	case single:
		return err == nil && resp.StatusCode == http.StatusTooManyRequests
	case err != nil:
		return true
	}
	return retryable(resp.StatusCode)
}

// retryable reports whether an answer of status may differ when asked again.
func retryable(status int) bool {
	switch status {
	case http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	}
	return false
}

// again is req to send once more, with its body anew.
func again(req *http.Request) (*http.Request, error) {
	if req.Body == nil || req.GetBody == nil {
		return req, nil
	}
	body, err := req.GetBody()
	if err != nil {
		return nil, fmt.Errorf("reading the request's body again: %w", err)
	}
	next := req.Clone(req.Context())
	next.Body = body
	return next, nil
}
