package orgs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"slices"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// Notifier tells the servers that follow people's organizations when they may have changed.
type Notifier interface {
	// Watch returns a channel that receives soon after a change commits to a membership or a grant of one of the
	// principals ids names, or to one of the organizations it names or their roles, and a function that stops it.
	// Changes that come while the channel is full make one receive.
	Watch(ids ...uuid.UUID) (<-chan struct{}, func())
}

// Membership is an organization a viewer belongs to, with the permissions they hold there.
type Membership struct {
	Organization
	Permissions []string
}

// poll is how often a follower reads the viewer's organizations again without a wake, for a change it missed.
const poll = time.Minute

// WithNotifier has FollowMemberships read again as soon as n says a viewer's organizations may have changed; without
// it, a follower reads them once a minute.
func (s *Service) WithNotifier(n Notifier) *Service {
	s.notifier = n
	return s
}

// Memberships lists the organizations v belongs to, by name, each with the permissions v holds there. One where v
// holds no org.read is left out, as one v may not see.
func (s *Service) Memberships(ctx context.Context, v Viewer) (_ []Membership, err error) {
	ctx, span := tracer.Start(ctx, "orgs.Service.Memberships", trace.WithAttributes(attribute.String("enduser.id", v.ID.String())))
	defer span.End()
	defer func() { record(span, err) }()
	return s.memberships(ctx, v)
}

// FollowMemberships sends v's memberships, as Memberships lists them, at once, then again each time they change,
// until ctx ends or a read fails, and then closes the channel. It reads them again whenever a membership or a grant of
// v's, or one of v's organizations or its roles, may have changed, and once a minute for a change it missed. The
// Console follows the viewer's organizations this way, so one that someone creates, joins or leaves elsewhere shows.
func (s *Service) FollowMemberships(ctx context.Context, v Viewer) (_ <-chan []Membership, err error) {
	ctx, span := tracer.Start(ctx, "orgs.Service.FollowMemberships", trace.WithAttributes(attribute.String("enduser.id", v.ID.String())))
	defer span.End()
	defer func() { record(span, err) }()
	w := &watch{notifier: s.notifier, stop: func() {}}
	list, err := s.watched(ctx, v, w)
	if err != nil {
		w.stop()
		return nil, err
	}
	out := make(chan []Membership, 1)
	out <- list
	go s.follow(ctx, v, list, w, out)
	return out, nil
}

// follow sends v's memberships to out each time they differ from last, until ctx ends or a read fails, which it logs
// unless ctx ended, then stops w and closes out.
func (s *Service) follow(ctx context.Context, v Viewer, last []Membership, w *watch, out chan<- []Membership) {
	defer close(out)
	defer func() { w.stop() }()
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.wake:
		case <-ticker.C:
		}
		list, err := s.watched(ctx, v, w)
		if err != nil {
			if ctx.Err() == nil {
				slog.ErrorContext(ctx, "Following the viewer's organizations failed", "error", err)
			}
			return
		}
		if reflect.DeepEqual(list, last) {
			continue
		}
		select {
		case out <- list:
			last = list
		case <-ctx.Done():
			return
		}
	}
}

// watch is what a follower of v's memberships watches through the notifier: v and the organizations v belongs to.
type watch struct {
	notifier Notifier
	ids      []uuid.UUID
	wake     <-chan struct{} // never receives without a notifier
	stop     func()
}

// watched reads v's memberships, first watching v and every organization of v's that the read before showed, then
// again while a read shows others, so no change to what it answers goes unseen.
func (s *Service) watched(ctx context.Context, v Viewer, w *watch) ([]Membership, error) {
	ids := []uuid.UUID{v.ID}
	for {
		if !slices.Equal(ids, w.ids) {
			w.stop()
			w.ids = ids
			if w.notifier != nil {
				w.wake, w.stop = w.notifier.Watch(ids...)
			}
		}
		list, err := s.memberships(ctx, v)
		if err != nil {
			return nil, err
		}
		ids = []uuid.UUID{v.ID}
		for _, m := range list {
			ids = append(ids, m.ID)
		}
		if slices.Equal(ids, w.ids) {
			return list, nil
		}
	}
}

// memberships lists v's organizations with the permissions v holds at each, leaving out those without org.read.
func (s *Service) memberships(ctx context.Context, v Viewer) ([]Membership, error) {
	list, err := s.store.OrganizationsOf(ctx, v.ID)
	if err != nil {
		return nil, fmt.Errorf("listing the organizations: %w", err)
	}
	out := make([]Membership, 0, len(list))
	for _, o := range list {
		held, err := s.read(ctx, v, o.ID)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("reading the permissions at %s: %w", o.ID, err)
		}
		out = append(out, Membership{Organization: o, Permissions: held})
	}
	return out, nil
}
