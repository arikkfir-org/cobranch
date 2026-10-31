package git

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

var tracer = otel.Tracer("cobranch.dev/internal/services/git")

// Store is what the database keeps of Git hosting.
type Store interface {
	// Snapshot reads the repository's refs and active packs in one statement, or ErrNotFound.
	Snapshot(ctx context.Context, repoID uuid.UUID) (Snapshot, error)
	// Record applies a push in one transaction that acts for its pusher: it locks the repository, so pushes into one
	// repository apply one at a time; does nothing for a push whose ID it recorded already; checks every ref's old
	// value, returning a *StaleError for the first that moved; lists the pack; records the push and its commits, each
	// author and committer resolved to a principal by lowercased email (a user who holds it verified, or else the
	// login-less user who holds it, or else a new login-less user holding it unverified, named after the commit);
	// moves the refs; creates, moves and deletes the branches; and records event repo.pushed. ErrNotFound when the
	// repository is gone.
	Record(ctx context.Context, push Push) error
}

// Service serves Git hosting's domain: snapshots for the Git server, and the record of pushes for the push worker.
// It checks no permission: the Git server checks the pusher's before a push reaches it, and the push worker trusts
// only the Git server.
type Service struct {
	store Store
}

// NewService returns a Service over store.
func NewService(store Store) *Service {
	return &Service{store: store}
}

// Snapshot reads what a fetch of the repository serves: its refs and active packs, at one moment.
func (s *Service) Snapshot(ctx context.Context, repoID uuid.UUID) (Snapshot, error) {
	ctx, span := tracer.Start(ctx, "git.Service.Snapshot")
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.repository.id", repoID.String()))
	snapshot, err := s.store.Snapshot(ctx, repoID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "reading the snapshot")
		return Snapshot{}, fmt.Errorf("reading the snapshot of repository %s: %w", repoID, err)
	}
	return snapshot, nil
}

// Record accepts or refuses a push whole, and records an accepted one before it answers: once it returns Accepted,
// the push's commits, refs and branches are committed. A push it refuses changes nothing. A push it already recorded,
// asked again by a Git server that lost the answer, is accepted again, as it was.
func (s *Service) Record(ctx context.Context, push Push) (Answer, error) {
	ctx, span := tracer.Start(ctx, "git.Service.Record")
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.push.id", push.ID.String()), attribute.String("cobranch.repository.id", push.RepoID.String()),
		attribute.Int("cobranch.push.refs", len(push.Updates)), attribute.Int("cobranch.push.commits", len(push.Commits)))
	if why := Refusal(push.Updates); why != "" {
		return Answer{Message: why}, nil
	}
	if len(push.Updates) == 0 {
		return Answer{Message: "the push moves no ref"}, nil
	}
	err := s.store.Record(ctx, push)
	var stale *StaleError
	switch {
	case errors.As(err, &stale):
		return Answer{Message: stale.Error()}, nil
	case err != nil:
		span.RecordError(err)
		span.SetStatus(codes.Error, "recording the push")
		return Answer{}, fmt.Errorf("recording push %s: %w", push.ID, err)
	}
	return Answer{Accepted: true}, nil
}
