package git

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

var tracer = otel.Tracer("cobranch.dev/internal/services/git")

// Store is what the database keeps of Git hosting.
type Store interface {
	// Snapshot reads the repository's refs and active packs in one statement; a repository with neither, or none at
	// all, has an empty one.
	Snapshot(ctx context.Context, repoID uuid.UUID) (Snapshot, error)
	// Record applies a push in one transaction that acts for its pusher: it locks the repository, so pushes into one
	// repository apply one at a time; does nothing for a push whose ID it recorded already; checks every ref's old
	// value, returning a *StaleError for the first that moved; checks the branches the facts were counted from, keeping
	// only facts that still hold and returning a *StaleError (During) when the default branch moved, or any branch did
	// under a push that moves the default branch; lists the pack (one the repository lists already keeps
	// its upload while active); records the push, waiting for the push
	// worker, and its commits; moves the refs; creates, moves and deletes the branches; replaces the merge
	// requirements when it moves the default branch; stores the branches' facts, and has every branch whose facts or
	// requirements it changed wait for it; records event repo.pushed; and notifies the push workers. ErrNotFound when
	// the repository is gone.
	Record(ctx context.Context, push Push) error
	// Process claims the push that has waited longest for the push worker and may be tried now, skipping any another
	// push worker holds, and processes it in one transaction: it resolves its commits' authors and committers to
	// principals by lowercased email (a user who holds it verified, or else the login-less user who holds it, or else
	// a new login-less user holding it unverified, named after the commit), works out the status of the branches that
	// wait for it, then marks the push processed. It returns
	// the push's ID, or uuid.Nil when none waits. A push it fails keeps waiting, with the error, for another try after
	// retry(attempts), attempts counting this one.
	Process(ctx context.Context, retry func(attempts int) time.Duration) (uuid.UUID, error)
}

// Service serves Git hosting's domain: snapshots and the record of pushes for the Git server, what follows a push for
// the push worker, and the packs' compaction and deletion for the maintenance job (WithPacks). It checks no
// permission: the Git server checks the pusher's before a push reaches it, and maintenance acts for nobody.
type Service struct {
	store     Store
	packStore PackStore
	packs     Packs
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
// the push's commits, refs and branches are committed, and the push waits for the push worker. A push it refuses
// changes nothing. A push it already recorded, asked again after the store failed and maybe lost the answer, is
// accepted again, as it was.
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
	err := s.record(ctx, push)
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

// recordAttempts is how many times Record asks the store before it gives up on a push, waiting recordRetry before the
// first retry and twice as long before each after.
const (
	recordAttempts = 4
	recordRetry    = 100 * time.Millisecond
)

// record has the store record the push, and asks again with the same push ID when the store failed: it may have
// committed the push and lost the answer, and then records nothing twice.
func (s *Service) record(ctx context.Context, push Push) error {
	wait := recordRetry
	var err error
	for attempt := range recordAttempts {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return fmt.Errorf("%w (after %w)", ctx.Err(), err)
			case <-time.After(wait):
			}
			wait *= 2
		}
		err = s.store.Record(ctx, push)
		if err == nil {
			return nil
		}
		var stale *StaleError
		if errors.As(err, &stale) || errors.Is(err, ErrNotFound) {
			return fmt.Errorf("the store refused it: %w", err)
		}
	}
	return fmt.Errorf("%d times: %w", recordAttempts, err)
}

// Process processes the push that has waited longest for the push worker, and reports whether one waited: false once
// none does, so the push worker waits for the next. A push it fails waits for another try, sooner after its first
// failures than after many.
func (s *Service) Process(ctx context.Context) (bool, error) {
	ctx, span := tracer.Start(ctx, "git.Service.Process")
	defer span.End()
	id, err := s.store.Process(ctx, RetryAfter)
	if id != uuid.Nil {
		span.SetAttributes(attribute.String("cobranch.push.id", id.String()))
	}
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "processing a push")
		return id != uuid.Nil, fmt.Errorf("processing push %s: %w", id, err)
	}
	return id != uuid.Nil, nil
}

// RetryAfter is how long a push the push worker failed attempts times waits for its next try: a second, doubling
// with each failure, at most an hour.
func RetryAfter(attempts int) time.Duration {
	if attempts > 12 {
		return time.Hour
	}
	return min(time.Second<<max(attempts-1, 0), time.Hour)
}
