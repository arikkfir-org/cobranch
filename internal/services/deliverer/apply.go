package deliverer

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/code"
)

// apply takes w's apply and delivers it, answering whether it took it: it doesn't when w neither rendered, nor is a
// queued resync, nor had its apply left by a controller. Then, for a commit's, it keeps w's render pod's logs and
// removes the pod, which Sweep does later when that fails.
func (s *Service) apply(ctx context.Context, w Working) (bool, error) {
	a, ok, err := s.store.TakeApply(ctx, w.ID, s.now().Add(s.settings.Lease))
	if err != nil {
		return false, fmt.Errorf("taking delivery %s's apply: %w", w.ID, err)
	}
	if !ok {
		return false, nil
	}
	o, err := s.outcome(ctx, a)
	if err != nil {
		return true, err
	}
	if err := ctx.Err(); err != nil {
		// The controller stops: whoever takes the apply once its lease lapses applies it again.
		return true, fmt.Errorf("applying delivery %s: %w", w.ID, err)
	}
	if err := s.store.Record(ctx, o); err != nil {
		return true, fmt.Errorf("recording delivery %s's apply: %w", w.ID, err)
	}
	if a.Kind == code.Resync {
		return true, nil
	}
	if _, err := s.keepLogs(ctx, w); err != nil {
		return true, err
	}
	return true, wrapIf(s.pods.RemoveRender(ctx, w.OrgID, w.ID), "removing delivery "+w.ID.String()+"'s render pod")
}

// outcome opens a's cluster and delivers a there, or fails it, saying why it can't: a resync that finds no render, or
// a cluster the controller can't reach.
func (s *Service) outcome(ctx context.Context, a Apply) (Outcome, error) {
	if !a.Rendered {
		return failure(a, a.Inventory, "no delivery of a commit to this environment succeeded, so there is no render to apply again"), nil
	}
	target, why, err := s.open(ctx, a.environment())
	switch {
	case err != nil:
		// Whoever takes the apply once its lease lapses tries again.
		return Outcome{}, err
	case why != "":
		return failure(a, a.Inventory, why), nil
	}
	return s.deliver(ctx, target, a), nil
}

// deliver applies a's render to its environment's namespace, which it prepares first, prunes what the render stopped
// holding, and reads the health of what it applied. An apply that fails partway adds what it applied to the
// environment's inventory and prunes nothing, and a prune that fails keeps what it didn't delete there, so nothing
// applied is ever forgotten.
func (s *Service) deliver(ctx context.Context, target Target, a Apply) Outcome {
	env := a.environment()
	if err := target.Prepare(ctx, env); err != nil {
		return failure(a, a.Inventory, "preparing namespace "+a.Namespace+" failed: "+unrefused(err))
	}
	applied, err := target.Apply(ctx, env, a.Manifests)
	switch {
	case errors.Is(err, ErrRefused):
		return failure(a, merged(a.Inventory, applied), "the cluster refused the render: "+unrefused(err))
	case err != nil:
		return failure(a, merged(a.Inventory, applied), "applying failed: "+err.Error())
	}
	if err := target.Prune(ctx, env, a.Inventory, applied); err != nil {
		return failure(a, merged(applied, a.Inventory), "applied, but deleting what the render no longer holds failed: "+err.Error())
	}
	read, health, err := target.Health(ctx, applied)
	if err != nil {
		read, health = applied, code.Unknown
	}
	return Outcome{Apply: a, Inventory: read, Health: health, State: code.DeliverySucceeded, Report: report(health, read)}
}

// unrefused is err's message without ErrRefused's name, which the reason says in its own words.
func unrefused(err error) string {
	return strings.TrimPrefix(err.Error(), ErrRefused.Error()+": ")
}

// failure is the outcome of a delivery that failed for reason, its environment then holding inventory.
func failure(a Apply, inventory []Object, reason string) Outcome {
	return Outcome{Apply: a, Inventory: inventory, Health: a.Health, State: code.DeliveryFailed, Reason: reason,
		Report: code.AnnotationInput{Status: code.AnnotationFailed, Summary: capitalize(reason)}}
}

// merged is first, each object as later holds it where later has it too, and then what later holds that first
// doesn't, in order.
func merged(first, later []Object) []Object {
	out := make([]Object, 0, len(first)+len(later))
	at := map[string]int{}
	for _, o := range first {
		at[o.key()] = len(out)
		out = append(out, o)
	}
	for _, o := range later {
		if i, ok := at[o.key()]; ok {
			out[i] = o
			continue
		}
		at[o.key()] = len(out)
		out = append(out, o)
	}
	return out
}

// report is the annotation of a delivery that succeeded, as its environment's health stands: Successful once Healthy,
// Failed with the first degraded object's message once Degraded, and In progress until either.
func report(health code.Health, objects []Object) code.AnnotationInput {
	switch health {
	case code.Healthy:
		return code.AnnotationInput{Status: code.AnnotationSuccessful, Summary: "Healthy"}
	case code.Degraded:
		summary := "Degraded"
		for _, o := range objects {
			if o.Health == code.Degraded {
				summary += ": " + o.String()
				if o.Message != "" {
					summary += ": " + o.Message
				}
				break
			}
		}
		return code.AnnotationInput{Status: code.AnnotationFailed, Summary: summary}
	}
	return code.AnnotationInput{Status: code.AnnotationInProgress, Summary: capitalize(string(health))}
}

// readHealth reads again the health of each environment that is Progressing and wasn't read within HealthEvery, and
// records it, its revision's annotation following it. It goes on past an environment it fails to read, and leaves one
// whose cluster it can't open as it stands.
func (s *Service) readHealth(ctx context.Context) error {
	envs, err := s.store.Progressing(ctx)
	if err != nil {
		return fmt.Errorf("listing the environments that are progressing: %w", err)
	}
	s.keepRead(envs)
	var errs []error
	for _, env := range envs {
		if !s.due(env.ID) {
			continue
		}
		if err := s.readEnvironment(ctx, env); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// readEnvironment reads one environment's health and records it.
func (s *Service) readEnvironment(ctx context.Context, env Environment) error {
	target, why, err := s.open(ctx, env)
	switch {
	case err != nil:
		return err
	case why != "":
		s.markRead(env.ID)
		return nil
	}
	objects, err := s.store.Objects(ctx, env.ID)
	if err != nil {
		return fmt.Errorf("listing environment %s's objects: %w", env.ID, err)
	}
	read, health, err := target.Health(ctx, objects)
	if err != nil {
		return fmt.Errorf("reading environment %s's health: %w", env.ID, err)
	}
	if err := s.store.SetHealth(ctx, env.ID, read, health, report(health, read)); err != nil {
		return fmt.Errorf("recording environment %s's health: %w", env.ID, err)
	}
	s.markRead(env.ID)
	return nil
}

// markRead records that the environment's health was just read, or looked at.
func (s *Service) markRead(id uuid.UUID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.read[id] = s.now()
}

// due reports whether the environment's health wasn't read within HealthEvery.
func (s *Service) due(id uuid.UUID) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	at, ok := s.read[id]
	return !ok || s.now().Sub(at) >= s.settings.HealthEvery
}

// keepRead forgets when it read the environments that are no longer Progressing.
func (s *Service) keepRead(envs []Environment) {
	s.mu.Lock()
	defer s.mu.Unlock()
	live := make(map[uuid.UUID]bool, len(envs))
	for _, e := range envs {
		live[e.ID] = true
	}
	for id := range s.read {
		if !live[id] {
			delete(s.read, id)
		}
	}
}

// capitalize starts s with a capital letter, as an annotation's summary does.
func capitalize(s string) string {
	r, size := utf8.DecodeRuneInString(s)
	if r == utf8.RuneError {
		return s
	}
	return string(unicode.ToUpper(r)) + s[size:]
}
