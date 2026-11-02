package deliverer

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/runner"
)

// tokenSlack is how long a delivery's token outlives its render's timeout: the pod's deadline counts from when its
// node takes it, and scheduling and pulling come first.
const tokenSlack = 15 * time.Minute

// Why a claimed delivery can't render, each a reason it fails with.
const (
	noCluster = "this installation delivers to no cluster of its own"
	noPods    = "this installation runs no pods, and a delivery renders in one"
	noGit     = "this installation serves no Git over HTTPS to check the commit out from"
)

// Dispatch claims the queued delivery that waited longest and starts its render pod, and reports whether one waited.
// A delivery that can't render fails, naming why; one the quota refuses waits for room; one that fails on the way is
// taken again once its claim lapses.
func (s *Service) Dispatch(ctx context.Context) (_ bool, err error) {
	ctx, span := tracer.Start(ctx, "deliverer.Service.Dispatch")
	defer span.End()
	defer func() { record(span, err) }()
	d, ok, err := s.store.Claim(ctx, s.now().Add(s.settings.Lease))
	if err != nil {
		return false, fmt.Errorf("claiming a queued delivery: %w", err)
	}
	if !ok {
		return false, nil
	}
	span.SetAttributes(deliveryAttrs(d.ID, d.OrgID, d.RepoID, d.Number)...)
	span.SetAttributes(attribute.String("cobranch.delivery.target", d.Target), attribute.String("cobranch.environment.id", d.EnvironmentID.String()))
	return true, s.start(ctx, d)
}

// start renders a claimed delivery: it reads what the cluster serves, prepares the organization's job namespace, and
// starts the delivery's render pod.
func (s *Service) start(ctx context.Context, d Delivery) error {
	switch {
	case !s.settings.Pods:
		return s.fail(ctx, d.ID, noPods)
	case s.target == nil:
		return s.fail(ctx, d.ID, noCluster)
	case s.settings.GitURL == "":
		return s.fail(ctx, d.ID, noGit)
	}
	version, apis, err := s.target.Capabilities(ctx)
	if err != nil {
		return fmt.Errorf("reading what the cluster serves for delivery %s: %w", d.ID, err)
	}
	render, err := s.render(ctx, d, version, apis)
	if err != nil {
		return err
	}
	if err := s.pods.Prepare(ctx, d.OrgID); err != nil {
		return fmt.Errorf("preparing delivery %s's job namespace: %w", d.ID, err)
	}
	switch err := s.pods.StartRender(ctx, render); {
	case errors.Is(err, runner.ErrNoRoom):
		return s.putOff(ctx, d.ID, "waits for room in the organization's quota")
	case errors.Is(err, runner.ErrRefused):
		return s.fail(ctx, d.ID, "the cluster refused its render pod: "+strings.TrimPrefix(err.Error(), runner.ErrRefused.Error()+": "))
	case err != nil:
		return fmt.Errorf("starting delivery %s's render pod: %w", d.ID, err)
	}
	marked, err := s.store.MarkRendering(ctx, d.ID)
	switch {
	case err != nil:
		return fmt.Errorf("marking delivery %s rendering: %w", d.ID, err)
	case !marked:
		// A newer delivery superseded it, or a member paused its environment, while its pod was being made.
		return wrapIf(s.pods.RemoveRender(ctx, d.OrgID, d.ID), "removing ended delivery "+d.ID.String()+"'s render pod")
	}
	return nil
}

// render makes the delivery's token and its render pod: `cobranch render` for its target and namespace, which main's
// /cobranch.yaml grants, at its commit, for what the cluster serves, uploading what it renders with the token.
func (s *Service) render(ctx context.Context, d Delivery, version string, apis []string) (Render, error) {
	token := auth.NewSecret(auth.PrefixDeliveryToken)
	if err := s.store.Token(ctx, d.ID, auth.Hash(token), auth.Hint(token), s.now().Add(s.settings.Timeout+tokenSlack)); err != nil {
		return Render{}, fmt.Errorf("recording delivery %s's token: %w", d.ID, err)
	}
	args := []string{"--target", d.Target, "--namespace", d.Namespace, "--branch", d.Branch, "--commit", d.SHA,
		"--repository", d.RepoSlug, "--organization", d.OrgSlug, "--kube-version", version}
	if len(apis) > 0 {
		args = append(args, "--api-versions", strings.Join(apis, ","))
	}
	repo := url.PathEscape(d.OrgSlug) + "/repos/" + url.PathEscape(d.RepoSlug)
	env := map[string]string{
		"COBRANCH_TOKEN":   token,
		"COBRANCH_SHA":     d.SHA,
		"COBRANCH_GIT_URL": strings.TrimSuffix(s.settings.GitURL, "/") + "/" + d.OrgSlug + "/" + d.RepoSlug + ".git",
		"COBRANCH_UPLOAD_URL": strings.TrimSuffix(s.settings.RESTURL, "/") + "/v1/orgs/" + repo + "/deliveries/" +
			strconv.FormatInt(d.Number, 10) + "/manifests",
	}
	return Render{Delivery: d, Args: args, Env: env, Masked: []string{token}, Timeout: s.settings.Timeout}, nil
}

// fail ends a claimed delivery Failed, naming why it couldn't render.
func (s *Service) fail(ctx context.Context, deliveryID uuid.UUID, reason string) error {
	if _, err := s.store.Finish(ctx, deliveryID, code.DeliveryFailed, reason); err != nil {
		return fmt.Errorf("failing delivery %s: %w", deliveryID, err)
	}
	return nil
}

// putOff puts a claimed delivery off for RoomRetry, saying why.
func (s *Service) putOff(ctx context.Context, deliveryID uuid.UUID, reason string) error {
	if err := s.store.Defer(ctx, deliveryID, s.now().Add(s.settings.RoomRetry), reason); err != nil {
		return fmt.Errorf("putting delivery %s off: %w", deliveryID, err)
	}
	return nil
}

// deliveryAttrs are the attributes of a span about a delivery.
func deliveryAttrs(deliveryID, orgID, repoID uuid.UUID, number int64) []attribute.KeyValue {
	return []attribute.KeyValue{attribute.String("cobranch.delivery.id", deliveryID.String()), attribute.Int64("cobranch.delivery.number", number),
		attribute.String("cobranch.org.id", orgID.String()), attribute.String("cobranch.repo.id", repoID.String())}
}

// record marks span failed with err, if any.
func record(span trace.Span, err error) {
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
}

// wrapIf wraps err with what was being done, or answers nil without one.
func wrapIf(err error, what string) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", what, err)
}
