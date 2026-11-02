package deliverer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"

	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/runner"
)

// lateSlack is how long past its timeout a render pod may stay unended before the controller fails its delivery
// itself: the pod's own deadline counts from when its node takes it, and one never scheduled has none.
const lateSlack = 5 * time.Minute

// Follow applies the renders that render pods uploaded, and the applies a controller left, follows the teardowns that
// delete, and fails the deliveries whose render pods failed, vanished or outlived their timeouts, keeping their logs and
// removing their pods; then it reads the health of the environments that are Progressing. A delivery it fails to
// follow is followed again on the next pass; Follow goes on with the rest and answers what failed.
func (s *Service) Follow(ctx context.Context) (err error) {
	ctx, span := tracer.Start(ctx, "deliverer.Service.Follow")
	defer span.End()
	defer func() { record(span, err) }()
	working, err := s.store.Working(ctx)
	if err != nil {
		return fmt.Errorf("listing the deliveries that render and apply: %w", err)
	}
	span.SetAttributes(attribute.Int("cobranch.deliveries.count", len(working)))
	var errs []error
	byOrg := map[uuid.UUID][]Working{}
	for _, w := range working {
		switch {
		case w.State == code.DeliveryDeleting:
			// A teardown has no render pod to look at.
			errs = append(errs, s.tearDown(ctx, w.ID))
		case w.Kind == code.Resync:
			// Neither has a resync: it only applies, and one a controller left is applied again.
			_, err := s.apply(ctx, w)
			errs = append(errs, err)
		default:
			byOrg[w.OrgID] = append(byOrg[w.OrgID], w)
		}
	}
	for orgID, ws := range byOrg {
		pods, err := s.pods.Renders(ctx, orgID)
		if err != nil {
			errs = append(errs, fmt.Errorf("listing organization %s's render pods: %w", orgID, err))
			continue
		}
		for _, w := range ws {
			pod, ok := pods[w.ID]
			if err := s.follow(ctx, w, pod, ok); err != nil {
				errs = append(errs, err)
			}
		}
	}
	errs = append(errs, s.readHealth(ctx))
	return errors.Join(errs...)
}

// follow applies w once its render is in, or once a controller left its apply, and otherwise fails it as its render
// pod, which ok says it has, ended: Failed with the pod, or when the pod vanished, outlived its timeout or ended without
// uploading.
func (s *Service) follow(ctx context.Context, w Working, pod runner.Pod, ok bool) error {
	ran := s.now().Sub(w.StartedAt)
	switch {
	case w.Rendered || w.State == code.DeliveryApplying:
		_, err := s.apply(ctx, w)
		return err
	case !ok && ran < s.settings.Vanished:
		return nil
	case !ok:
		return s.end(ctx, w, because("its render pod vanished"))
	case pod.Outcome == runner.Succeeded:
		// It may have uploaded since Working read: apply, and fail it only when it didn't.
		applied, err := s.apply(ctx, w)
		if err != nil || applied {
			return err
		}
		return s.end(ctx, w, because("its render pod ended without uploading a render"))
	case pod.Outcome == runner.Failed:
		return s.end(ctx, w, func(logs map[string][]byte) string { return renderFailure(logs, pod.Reason) })
	case ran > s.settings.Timeout+lateSlack:
		return s.end(ctx, w, because("rendering timed out after "+s.settings.Timeout.String()+" without its pod running"))
	}
	return nil
}

// because is a reason that its logs don't change.
func because(reason string) func(map[string][]byte) string {
	return func(map[string][]byte) string { return reason }
}

// renderFailure is why a failed render pod failed: the last line its failed container wrote, which `cobranch render`
// and the checkout end with their error, or the pod's reason without one.
func renderFailure(logs map[string][]byte, reason string) string {
	for container, log := range logs {
		if !strings.HasPrefix(reason, "container "+container+" ") {
			continue
		}
		lines := bytes.Split(bytes.TrimSpace(log), []byte("\n"))
		if last := strings.TrimSpace(string(lines[len(lines)-1])); last != "" {
			return "rendering failed: " + strings.TrimPrefix(last, "cobranch render: ")
		}
	}
	return "rendering failed: " + reason
}

// end keeps the delivery's render pod's logs, fails the delivery with the reason they give unless it ended already,
// and removes its pod.
func (s *Service) end(ctx context.Context, w Working, reason func(logs map[string][]byte) string) error {
	logs, err := s.keepLogs(ctx, w)
	if err != nil {
		return err
	}
	if _, err := s.store.Finish(ctx, w.ID, code.DeliveryFailed, reason(logs)); err != nil {
		return fmt.Errorf("failing delivery %s: %w", w.ID, err)
	}
	return wrapIf(s.pods.RemoveRender(ctx, w.OrgID, w.ID), "removing delivery "+w.ID.String()+"'s render pod")
}

// keepLogs writes each of the delivery's render pod's containers' logs to the archive, and answers them.
func (s *Service) keepLogs(ctx context.Context, w Working) (map[string][]byte, error) {
	logs, err := s.pods.RenderLogs(ctx, w.OrgID, w.ID, s.settings.LogLimit)
	if err != nil {
		return nil, fmt.Errorf("reading delivery %s's render logs: %w", w.ID, err)
	}
	containers := make([]string, 0, len(logs))
	for c := range logs {
		containers = append(containers, c)
	}
	slices.Sort(containers)
	for _, c := range containers {
		if err := s.archive.Put(ctx, w.RepoID, w.ID, c, logs[c]); err != nil {
			return nil, fmt.Errorf("keeping delivery %s's %s log: %w", w.ID, c, err)
		}
	}
	return logs, nil
}

// Sweep cleans up after what Follow doesn't see: it removes the render pods of deliveries that ended otherwise (a newer
// one superseded them, a member paused their environment), keeping their logs, and of deliveries that are gone, and
// deletes the tokens of deliveries that ended or expired, and each environment's renders past the newest KeepRenders.
// It answers what failed, having gone on with the rest.
func (s *Service) Sweep(ctx context.Context) (err error) {
	ctx, span := tracer.Start(ctx, "deliverer.Service.Sweep")
	defer span.End()
	defer func() { record(span, err) }()
	dropped, err := s.store.DropTokens(ctx)
	if err != nil {
		return fmt.Errorf("deleting the tokens of ended deliveries: %w", err)
	}
	span.SetAttributes(attribute.Int64("cobranch.delivery_tokens.dropped", dropped))
	renders, err := s.store.DropRenders(ctx, s.settings.KeepRenders)
	if err != nil {
		return fmt.Errorf("deleting old renders: %w", err)
	}
	span.SetAttributes(attribute.Int64("cobranch.delivery_renders.dropped", renders))
	orgs, err := s.pods.Namespaces(ctx)
	if err != nil {
		return fmt.Errorf("listing the job namespaces: %w", err)
	}
	var errs []error
	for _, orgID := range orgs {
		errs = append(errs, s.sweep(ctx, orgID))
	}
	return errors.Join(errs...)
}

// sweep removes the organization's render pods of deliveries that ended or are gone, keeping the ended ones' logs. A
// queued delivery's pod stays: a controller is starting it.
func (s *Service) sweep(ctx context.Context, orgID uuid.UUID) error {
	pods, err := s.pods.Renders(ctx, orgID)
	if err != nil || len(pods) == 0 {
		return wrapIf(err, "listing organization "+orgID.String()+"'s render pods")
	}
	ids := make([]uuid.UUID, 0, len(pods))
	for id := range pods {
		ids = append(ids, id)
	}
	slices.SortFunc(ids, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	standing, err := s.store.Deliveries(ctx, ids)
	if err != nil {
		return fmt.Errorf("finding organization %s's deliveries: %w", orgID, err)
	}
	var errs []error
	for _, id := range ids {
		d, ok := standing[id]
		switch {
		case ok && (d.State == code.DeliveryQueued || d.State == code.DeliveryRendering):
			continue
		case ok:
			if _, err := s.keepLogs(ctx, Working{ID: id, OrgID: orgID, RepoID: d.RepoID}); err != nil {
				errs = append(errs, err)
				continue
			}
		}
		if err := s.pods.RemoveRender(ctx, orgID, id); err != nil {
			errs = append(errs, fmt.Errorf("removing delivery %s's render pod: %w", id, err))
		}
	}
	return errors.Join(errs...)
}
