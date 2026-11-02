// Package webhook posts webhook runs to their receivers over HTTPS, never into the cluster: it connects only to
// addresses outside the ranges runs may not reach, unless the operator allowed them, checked at each address it dials
// rather than at the name it resolved, and it follows no redirect
// (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/jobs.md, "Webhooks").
package webhook

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"syscall"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"cobranch.dev/internal/services/runner"
)

var tracer = otel.Tracer("cobranch.dev/internal/adapters/webhook")

// timeout is how long one post may take, connecting included: a receiver that takes longer has failed it.
const timeout = 10 * time.Second

// Poster posts webhook runs (runner.Poster).
type Poster struct {
	client *http.Client
}

var _ runner.Poster = (*Poster)(nil)

// New returns a Poster that never connects to an address in except, the ranges runs may not reach, unless allowed, the
// private ranges the operator lets webhooks reach, holds it too; the unspecified addresses it never reaches.
func New(except, allowed []string) (*Poster, error) {
	deny, err := prefixes(except)
	if err != nil {
		return nil, fmt.Errorf("reading the ranges webhooks may not reach: %w", err)
	}
	allow, err := prefixes(allowed)
	if err != nil {
		return nil, fmt.Errorf("reading the ranges webhooks may reach: %w", err)
	}
	dialer := &net.Dialer{Timeout: timeout, Control: func(_, address string, _ syscall.RawConn) error {
		return check(address, deny, allow)
	}}
	transport := &http.Transport{DialContext: dialer.DialContext, TLSHandshakeTimeout: timeout, ForceAttemptHTTP2: true,
		MaxIdleConns: 10, IdleConnTimeout: 90 * time.Second}
	// No Proxy: a proxy would connect for it, past the address check.
	return &Poster{client: &http.Client{Transport: transport, Timeout: timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

// prefixes parses CIDRs.
func prefixes(cidrs []string) ([]netip.Prefix, error) {
	out := make([]netip.Prefix, 0, len(cidrs))
	for _, c := range cidrs {
		p, err := netip.ParsePrefix(c)
		if err != nil {
			return nil, fmt.Errorf("range %q: %w", c, err)
		}
		out = append(out, p.Masked())
	}
	return out, nil
}

// check refuses an address the Poster may not connect to: one in deny and not in allow, or an unspecified one.
func check(address string, deny, allow []netip.Prefix) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("reading address %q: %w", address, err)
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return fmt.Errorf("reading address %q: %w", address, err)
	}
	ip = ip.Unmap()
	if ip.IsUnspecified() {
		return fmt.Errorf("%w: %s", runner.ErrUnreachable, ip)
	}
	for _, p := range allow {
		if p.Contains(ip) {
			return nil
		}
	}
	for _, p := range deny {
		if p.Contains(ip) {
			return fmt.Errorf("%w: %s, in %s", runner.ErrUnreachable, ip, p)
		}
	}
	return nil
}

// Post posts body to an https target with headers, and answers nil once the receiver answers 2xx (runner.Poster).
func (p *Poster) Post(ctx context.Context, target string, body []byte, headers map[string]string) (err error) {
	ctx, span := tracer.Start(ctx, "webhook.Poster.Post")
	defer span.End()
	defer func() {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
	}()
	u, err := url.Parse(target)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return fmt.Errorf("%w: not an https URL", runner.ErrUnreachable)
	}
	span.SetAttributes(attribute.String("server.address", u.Hostname()))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("making the post: %w", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		if errors.Is(err, runner.ErrUnreachable) {
			return fmt.Errorf("posting to %s: %w", u.Host, unreachable(err))
		}
		return fmt.Errorf("posting to %s: %w", u.Host, err)
	}
	// The body is drained below, so a failed close leaves nothing to report.
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10)) // drained so the connection is reused; what's left is the receiver's
	span.SetAttributes(attribute.Int("http.response.status_code", resp.StatusCode))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("%s answered %s", u.Host, resp.Status)
	}
	return nil
}

// unreachable is the address check's error within err, so a run's reason names the address rather than the dial.
func unreachable(err error) error {
	var op *net.OpError
	if errors.As(err, &op) && op.Err != nil {
		return op.Err
	}
	return err
}
