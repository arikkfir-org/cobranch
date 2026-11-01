package git

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// Grace is how long a pack's files outlive its last listing, and an upload no pack lists its writing, before
// maintenance deletes them: a fetch may still be reading a superseded pack, and a push still recording its pack.
const Grace = 24 * time.Hour

// ErrPacksChanged is a compaction whose snapshot's packs are no longer all active: another compaction superseded one.
var ErrPacksChanged = errors.New("the repository's packs changed")

// Upload is one upload of a pack's files in the bucket, and when its last file was written.
type Upload struct {
	RepoID  uuid.UUID
	ID      uuid.UUID
	Written time.Time
}

// Superseded is a pack compaction replaced, with the upload its row lists.
type Superseded struct {
	RepoID uuid.UUID
	Pack   Pack
}

// PackStore is what the database keeps of packs, for maintenance: postgres.GitStore.
type PackStore interface {
	// Crowded lists the repositories that have at least threshold active packs.
	Crowded(ctx context.Context, threshold int) ([]uuid.UUID, error)
	// Compact lists pack as active and marks superseded the active packs named, in one transaction that locks the
	// repository; ErrPacksChanged when one of them is no longer active, and then it changes nothing. A pack the
	// repository lists already keeps its upload while active, as a push's would.
	Compact(ctx context.Context, repoID uuid.UUID, pack Pack, superseded []string) error
	// Superseded lists the packs superseded before the time.
	Superseded(ctx context.Context, before time.Time) ([]Superseded, error)
	// Forget deletes a superseded pack's row, unless a push made it active again, with another upload.
	Forget(ctx context.Context, s Superseded) error
	// Uploads lists every upload a pack's row names, whatever its state.
	Uploads(ctx context.Context) ([]uuid.UUID, error)
}

// Packs is the bucket's packs and the git CLI, for maintenance: gitserver.Packs.
type Packs interface {
	// Repack builds one pack of every object the snapshot's packs hold, reachable or not, and uploads it.
	Repack(ctx context.Context, repoID uuid.UUID, snapshot Snapshot) (Pack, error)
	// Uploads lists every upload the bucket holds, every repository's.
	Uploads(ctx context.Context) ([]Upload, error)
	// Delete deletes an upload's files; files already gone are no error.
	Delete(ctx context.Context, repoID, upload uuid.UUID) error
}

// WithPacks returns a copy of s that also compacts and deletes packs, through store's rows and the bucket's packs:
// the maintenance job's.
func (s *Service) WithPacks(store PackStore, packs Packs) *Service {
	c := *s
	c.packStore, c.packs = store, packs
	return &c
}

// Compact compacts the packs of every repository that has at least threshold active ones into one each, from a
// snapshot: packs pushed meanwhile stay active, untouched. It returns how many repositories it compacted, and goes on
// past a repository it fails, returning every failure joined.
func (s *Service) Compact(ctx context.Context, threshold int) (int, error) {
	ctx, span := tracer.Start(ctx, "git.Service.Compact")
	defer span.End()
	span.SetAttributes(attribute.Int("cobranch.git.compact_at", threshold))
	if s.packs == nil || threshold < 2 {
		return 0, fmt.Errorf("compacting at %d packs: %w", threshold, errNoPacks)
	}
	crowded, err := s.packStore.Crowded(ctx, threshold)
	if err != nil {
		return 0, failed(span, fmt.Errorf("finding the repositories to compact: %w", err))
	}
	compacted := 0
	var errs []error
	for _, repoID := range crowded {
		done, err := s.compact(ctx, repoID, threshold)
		if err != nil {
			errs = append(errs, err)
		}
		if done {
			compacted++
		}
	}
	span.SetAttributes(attribute.Int("cobranch.git.compacted", compacted), attribute.Int("cobranch.git.failed", len(errs)))
	if len(errs) > 0 {
		return compacted, failed(span, errors.Join(errs...))
	}
	return compacted, nil
}

// errNoPacks is a Service without packs asked to maintain them, or a threshold that compacts nothing.
var errNoPacks = errors.New("no packs to maintain")

// compact compacts one repository's packs, and reports whether it did: a repository whose packs another compaction
// replaced meanwhile is left as it is, and the pack it uploaded goes unlisted.
func (s *Service) compact(ctx context.Context, repoID uuid.UUID, threshold int) (bool, error) {
	ctx, span := tracer.Start(ctx, "git.Service.compact", trace.WithAttributes(attribute.String("cobranch.repository.id", repoID.String())))
	defer span.End()
	snapshot, err := s.store.Snapshot(ctx, repoID)
	if err != nil {
		return false, failed(span, fmt.Errorf("reading the snapshot of repository %s: %w", repoID, err))
	}
	if len(snapshot.Packs) < threshold {
		return false, nil
	}
	pack, err := s.packs.Repack(ctx, repoID, snapshot)
	if err != nil {
		return false, failed(span, fmt.Errorf("repacking repository %s: %w", repoID, err))
	}
	var superseded []string
	for _, p := range snapshot.Packs {
		if p.Name != pack.Name {
			superseded = append(superseded, p.Name)
		}
	}
	span.SetAttributes(attribute.Int("cobranch.git.packs", len(snapshot.Packs)), attribute.String("cobranch.git.pack", pack.Name))
	err = s.packStore.Compact(ctx, repoID, pack, superseded)
	if errors.Is(err, ErrPacksChanged) {
		return false, nil
	}
	if err != nil {
		return false, failed(span, fmt.Errorf("listing repository %s's compacted pack: %w", repoID, err))
	}
	return true, nil
}

// Swept counts what Sweep deleted.
type Swept struct {
	// Superseded is how many superseded packs' files and rows it deleted.
	Superseded int
	// Unlisted is how many uploads no pack lists it deleted: failed pushes', compactions that lost a race, and pushes
	// that brought a pack the repository listed already.
	Unlisted int
}

// Sweep deletes the files no fetch can list any more, Grace after they stopped being listed: superseded packs', then
// their rows, and uploads no row names. It goes on past a pack or an upload it fails, returning every failure joined.
func (s *Service) Sweep(ctx context.Context) (Swept, error) {
	ctx, span := tracer.Start(ctx, "git.Service.Sweep")
	defer span.End()
	if s.packs == nil {
		return Swept{}, fmt.Errorf("sweeping: %w", errNoPacks)
	}
	var swept Swept
	var errs []error
	before := time.Now().Add(-Grace)
	superseded, err := s.packStore.Superseded(ctx, before)
	if err != nil {
		return swept, failed(span, fmt.Errorf("listing superseded packs: %w", err))
	}
	for _, p := range superseded {
		// The files first: a row left behind is deleted on the next sweep, a file left behind never would be.
		if err := s.packs.Delete(ctx, p.RepoID, p.Pack.Upload); err != nil {
			errs = append(errs, fmt.Errorf("deleting pack %s of repository %s: %w", p.Pack.Name, p.RepoID, err))
			continue
		}
		if err := s.packStore.Forget(ctx, p); err != nil {
			errs = append(errs, fmt.Errorf("forgetting pack %s of repository %s: %w", p.Pack.Name, p.RepoID, err))
			continue
		}
		swept.Superseded++
	}
	unlisted, err := s.sweepUnlisted(ctx, before)
	swept.Unlisted = unlisted
	if err != nil {
		errs = append(errs, err)
	}
	span.SetAttributes(attribute.Int("cobranch.git.superseded", swept.Superseded), attribute.Int("cobranch.git.unlisted", swept.Unlisted))
	if len(errs) > 0 {
		return swept, failed(span, errors.Join(errs...))
	}
	return swept, nil
}

// sweepUnlisted deletes the uploads written before the time that no row names, and returns how many. It lists the
// bucket before the rows, so an upload a row comes to name after the listing is one written since.
func (s *Service) sweepUnlisted(ctx context.Context, before time.Time) (int, error) {
	uploads, err := s.packs.Uploads(ctx)
	if err != nil {
		return 0, fmt.Errorf("listing the bucket's uploads: %w", err)
	}
	ids, err := s.packStore.Uploads(ctx)
	if err != nil {
		return 0, fmt.Errorf("listing the uploads packs name: %w", err)
	}
	listed := make(map[uuid.UUID]bool, len(ids))
	for _, id := range ids {
		listed[id] = true
	}
	deleted := 0
	var errs []error
	for _, u := range uploads {
		if listed[u.ID] || !u.Written.Before(before) {
			continue
		}
		if err := s.packs.Delete(ctx, u.RepoID, u.ID); err != nil {
			errs = append(errs, fmt.Errorf("deleting upload %s of repository %s: %w", u.ID, u.RepoID, err))
			continue
		}
		deleted++
	}
	return deleted, errors.Join(errs...)
}

// failed records err on span and returns it.
func failed(span trace.Span, err error) error {
	span.RecordError(err)
	span.SetStatus(codes.Error, err.Error())
	return err
}
