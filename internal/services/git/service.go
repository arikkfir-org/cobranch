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

	"cobranch.dev/internal/services/limits"
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
	// under a push that moves the default branch; checks each branch it moves against the branch's merge
	// requirements, as they were before it, returning a *RefusedError for the first it breaks unless the pusher may
	// bypass them, and then records event branch.bypassed; lists the pack (one the repository lists already keeps
	// its upload while active), returning a *RefusedError when its bytes bring the organization's active packs over
	// the push's GitDataLimit, which it weighs under the organization's lock for the limit; records the push, waiting for the push
	// worker, and its commits and merges; moves the refs; creates, moves and deletes the branches; replaces the merge
	// requirements when it moves the default branch; stores the branches' facts, and has every branch whose facts or
	// requirements it changed wait for it; records event repo.pushed; and notifies the push workers. ErrNotFound when
	// the repository is gone.
	Record(ctx context.Context, push Push) error
	// Process claims the push that has waited longest for the push worker and may be tried now, skipping any another
	// push worker holds, and processes it in one transaction: it resolves its commits' authors and committers to
	// principals by lowercased email (a user who holds it verified, or else the login-less user who holds it, or else
	// a new login-less user holding it unverified, named after the commit), records the merges it made for its pusher,
	// works out the status of the branches that wait for it, then marks the push processed. It returns
	// the push's ID, or uuid.Nil when none waits. A push it fails keeps waiting, with the error, for another try after
	// retry(attempts), attempts counting this one.
	Process(ctx context.Context, retry func(attempts int) time.Duration) (uuid.UUID, error)
}

// Quotas are the limits pushes are held to: limits.Service.
type Quotas interface {
	// Organization returns the limits that apply to the organization.
	Organization(ctx context.Context, orgID uuid.UUID) (limits.Limits, error)
	// WeighPush refuses, with a *limits.ExceededError, a push that would bring size bytes of Git data into the
	// organization over its limit.
	WeighPush(ctx context.Context, orgID uuid.UUID, size int64) (int64, error)
}

// Service serves Git hosting's domain: snapshots and the record of pushes for the Git server, what follows a push for
// the push worker, and the packs' compaction and deletion for the maintenance job (WithPacks). It checks no
// permission: the Git server checks the pusher's before a push reaches it, and maintenance acts for nobody.
type Service struct {
	store     Store
	packStore PackStore
	packs     Packs
	quotas    Quotas
}

// NewService returns a Service over store.
func NewService(store Store) *Service {
	return &Service{store: store}
}

// WithQuotas holds every push's pack to its organization's Git data limit, as q works it out, and returns the
// Service. Without it, pushes store any amount.
func (s *Service) WithQuotas(q Quotas) *Service {
	s.quotas = q
	return s
}

// Weigh refuses a pack of size bytes that the organization's Git data limit leaves no room for, before the Git server
// stores it: it returns the refusal's message, or nothing when the pack fits. Record weighs it again in the push's
// transaction, which alone is sure: another push may have stored more meanwhile.
func (s *Service) Weigh(ctx context.Context, orgID uuid.UUID, size int64) (string, error) {
	if s.quotas == nil {
		return "", nil
	}
	ctx, span := tracer.Start(ctx, "git.Service.Weigh")
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.organization.id", orgID.String()), attribute.Int64("cobranch.push.size", size))
	_, err := s.quotas.WeighPush(ctx, orgID, size)
	var exceeded *limits.ExceededError
	switch {
	case errors.As(err, &exceeded):
		return exceeded.Error(), nil
	case err != nil:
		span.RecordError(err)
		span.SetStatus(codes.Error, "weighing the pack")
		return "", fmt.Errorf("weighing a pack of %d bytes: %w", size, err)
	}
	return "", nil
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
	if err := s.limit(ctx, &push); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "reading the limits")
		return Answer{}, fmt.Errorf("reading the limits of push %s: %w", push.ID, err)
	}
	err := s.record(ctx, push)
	var stale *StaleError
	var refused *RefusedError
	switch {
	case errors.As(err, &stale):
		return Answer{Message: stale.Error(), Stale: true}, nil
	case errors.As(err, &refused):
		return Answer{Message: refused.Message}, nil
	case err != nil:
		span.RecordError(err)
		span.SetStatus(codes.Error, "recording the push")
		return Answer{}, fmt.Errorf("recording push %s: %w", push.ID, err)
	}
	return Answer{Accepted: true}, nil
}

// limit sets the Git data limit the store holds the push's pack to: its organization's, when the Service has quotas
// and the push brings a pack.
func (s *Service) limit(ctx context.Context, push *Push) error {
	if s.quotas == nil || push.Pack == nil {
		return nil
	}
	l, err := s.quotas.Organization(ctx, push.OrgID)
	if err != nil {
		return fmt.Errorf("reading the limits of organization %s: %w", push.OrgID, err)
	}
	push.GitDataLimit = l.GitDataPerOrganization
	return nil
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
		var refused *RefusedError
		if errors.As(err, &stale) || errors.As(err, &refused) || errors.Is(err, ErrNotFound) {
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
