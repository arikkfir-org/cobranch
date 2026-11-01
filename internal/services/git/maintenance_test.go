package git_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/git"
)

// repoStore is git.Store with a snapshot of each repository's own.
type repoStore struct {
	store
	snapshots map[uuid.UUID]git.Snapshot
	failing   map[uuid.UUID]error
}

func (s *repoStore) Snapshot(_ context.Context, repoID uuid.UUID) (git.Snapshot, error) {
	return s.snapshots[repoID], s.failing[repoID]
}

// compaction is what PackStore.Compact was asked to list.
type compaction struct {
	pack       git.Pack
	superseded []string
}

// packStore is git.PackStore in memory: it answers what its fields say and keeps what it was asked to change.
type packStore struct {
	crowded       []uuid.UUID
	crowdedErr    error
	threshold     int
	compactErr    map[uuid.UUID]error
	compacted     map[uuid.UUID]compaction
	superseded    []git.Superseded
	supersededErr error
	before        time.Time
	forgetErr     map[string]error
	forgotten     []git.Superseded
	uploads       []uuid.UUID
	uploadsErr    error
}

func (s *packStore) Crowded(_ context.Context, threshold int) ([]uuid.UUID, error) {
	s.threshold = threshold
	return s.crowded, s.crowdedErr
}

func (s *packStore) Compact(_ context.Context, repoID uuid.UUID, pack git.Pack, superseded []string) error {
	if err := s.compactErr[repoID]; err != nil {
		return err
	}
	if s.compacted == nil {
		s.compacted = map[uuid.UUID]compaction{}
	}
	s.compacted[repoID] = compaction{pack: pack, superseded: superseded}
	return nil
}

func (s *packStore) Superseded(_ context.Context, before time.Time) ([]git.Superseded, error) {
	s.before = before
	return s.superseded, s.supersededErr
}

func (s *packStore) Forget(_ context.Context, p git.Superseded) error {
	if err := s.forgetErr[p.Pack.Name]; err != nil {
		return err
	}
	s.forgotten = append(s.forgotten, p)
	return nil
}

func (s *packStore) Uploads(context.Context) ([]uuid.UUID, error) {
	return s.uploads, s.uploadsErr
}

// packs is git.Packs in memory: Repack answers each repository's pack, and Delete keeps the uploads it deleted.
type packs struct {
	repacked   map[uuid.UUID]git.Pack
	repackErr  map[uuid.UUID]error
	got        map[uuid.UUID]git.Snapshot
	uploads    []git.Upload
	uploadsErr error
	deleteErr  map[uuid.UUID]error
	deleted    []uuid.UUID
}

func (p *packs) Repack(_ context.Context, repoID uuid.UUID, snapshot git.Snapshot) (git.Pack, error) {
	if p.got == nil {
		p.got = map[uuid.UUID]git.Snapshot{}
	}
	p.got[repoID] = snapshot
	return p.repacked[repoID], p.repackErr[repoID]
}

func (p *packs) Uploads(context.Context) ([]git.Upload, error) {
	return p.uploads, p.uploadsErr
}

func (p *packs) Delete(_ context.Context, _ uuid.UUID, upload uuid.UUID) error {
	if err := p.deleteErr[upload]; err != nil {
		return err
	}
	p.deleted = append(p.deleted, upload)
	return nil
}

// snapshotOf is a snapshot of main at a, with a pack of each name, each in an upload of its own.
func snapshotOf(names ...string) git.Snapshot {
	s := git.Snapshot{Refs: []git.Ref{{Name: "refs/heads/main", SHA: a}}}
	for _, name := range names {
		s.Packs = append(s.Packs, git.Pack{Name: name, Upload: uuid.New()})
	}
	return s
}

func TestServiceCompact(t *testing.T) {
	one, two := uuid.New(), uuid.New()
	broken := errors.New("the bucket is down")
	merged := git.Pack{Name: "pack-merged", Upload: uuid.New(), Size: 300, Objects: 9}
	runCompactCases(t, []uuid.UUID{one, two}, []compactCase{
		{name: "every crowded repository", threshold: 2,
			snapshots:     map[uuid.UUID]git.Snapshot{one: snapshotOf("pack-1", "pack-2"), two: snapshotOf("pack-3", "pack-4", "pack-5")},
			repacked:      map[uuid.UUID]git.Pack{one: merged, two: merged},
			want:          2,
			wantRepacked:  []uuid.UUID{one, two},
			wantCompacted: map[uuid.UUID]compaction{one: {merged, []string{"pack-1", "pack-2"}}, two: {merged, []string{"pack-3", "pack-4", "pack-5"}}}},
		// Its packs were compacted since the repositories were listed.
		{name: "a repository no longer crowded", threshold: 3,
			snapshots: map[uuid.UUID]git.Snapshot{one: snapshotOf("pack-1", "pack-2"), two: snapshotOf("pack-3", "pack-4", "pack-5")},
			repacked:  map[uuid.UUID]git.Pack{two: merged},
			want:      1, wantRepacked: []uuid.UUID{two},
			wantCompacted: map[uuid.UUID]compaction{two: {merged, []string{"pack-3", "pack-4", "pack-5"}}}},
		// One pack held every object already: it stays, with the upload it lists.
		{name: "a pack the repack gives again", threshold: 2,
			snapshots: map[uuid.UUID]git.Snapshot{one: snapshotOf("pack-1", "pack-2"), two: snapshotOf("pack-3", "pack-4")},
			repacked:  map[uuid.UUID]git.Pack{one: {Name: "pack-2", Upload: merged.Upload}, two: merged},
			want:      2, wantRepacked: []uuid.UUID{one, two},
			wantCompacted: map[uuid.UUID]compaction{one: {git.Pack{Name: "pack-2", Upload: merged.Upload}, []string{"pack-1"}},
				two: {merged, []string{"pack-3", "pack-4"}}}},
		{name: "a compaction that lost a race", threshold: 2,
			snapshots:  map[uuid.UUID]git.Snapshot{one: snapshotOf("pack-1", "pack-2"), two: snapshotOf("pack-3", "pack-4")},
			repacked:   map[uuid.UUID]git.Pack{one: merged, two: merged},
			compactErr: map[uuid.UUID]error{one: git.ErrPacksChanged},
			want:       1, wantRepacked: []uuid.UUID{one, two},
			wantCompacted: map[uuid.UUID]compaction{two: {merged, []string{"pack-3", "pack-4"}}}},
		{name: "a repack that fails", threshold: 2,
			snapshots: map[uuid.UUID]git.Snapshot{one: snapshotOf("pack-1", "pack-2"), two: snapshotOf("pack-3", "pack-4")},
			repacked:  map[uuid.UUID]git.Pack{two: merged},
			repackErr: map[uuid.UUID]error{one: broken},
			want:      1, wantErr: true, wantRepacked: []uuid.UUID{one, two},
			wantCompacted: map[uuid.UUID]compaction{two: {merged, []string{"pack-3", "pack-4"}}}},
		{name: "a snapshot that fails", threshold: 2,
			snapshots: map[uuid.UUID]git.Snapshot{two: snapshotOf("pack-3", "pack-4")},
			failing:   map[uuid.UUID]error{one: broken},
			repacked:  map[uuid.UUID]git.Pack{two: merged},
			want:      1, wantErr: true, wantRepacked: []uuid.UUID{two},
			wantCompacted: map[uuid.UUID]compaction{two: {merged, []string{"pack-3", "pack-4"}}}},
		{name: "a listing that fails", threshold: 2,
			snapshots:  map[uuid.UUID]git.Snapshot{one: snapshotOf("pack-1", "pack-2")},
			repacked:   map[uuid.UUID]git.Pack{one: merged},
			compactErr: map[uuid.UUID]error{one: broken},
			want:       0, wantErr: true, wantRepacked: []uuid.UUID{one}},
		{name: "repositories that can't be found", threshold: 2, crowdedErr: broken, wantErr: true},
		{name: "a threshold that compacts nothing", threshold: 1,
			snapshots: map[uuid.UUID]git.Snapshot{one: snapshotOf("pack-1", "pack-2")}, wantErr: true},
		{name: "a service without packs", threshold: 2, withoutPacks: true,
			snapshots: map[uuid.UUID]git.Snapshot{one: snapshotOf("pack-1", "pack-2")}, wantErr: true},
	})
}

// compactCase is a case of TestServiceCompact: the repositories' snapshots and what each port answers, and what
// Compact should return, repack and list.
type compactCase struct {
	name          string
	threshold     int
	withoutPacks  bool
	snapshots     map[uuid.UUID]git.Snapshot
	failing       map[uuid.UUID]error
	crowdedErr    error
	repacked      map[uuid.UUID]git.Pack
	repackErr     map[uuid.UUID]error
	compactErr    map[uuid.UUID]error
	want          int
	wantErr       bool
	wantRepacked  []uuid.UUID
	wantCompacted map[uuid.UUID]compaction
}

// runCompactCases runs Compact for each case, over a store that lists crowded as the crowded repositories.
func runCompactCases(t *testing.T, crowded []uuid.UUID, cases []compactCase) {
	t.Helper()
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			ps := &packStore{crowded: crowded, crowdedErr: tt.crowdedErr, compactErr: tt.compactErr}
			p := &packs{repacked: tt.repacked, repackErr: tt.repackErr}
			s := git.NewService(&repoStore{snapshots: tt.snapshots, failing: tt.failing})
			if !tt.withoutPacks {
				s = s.WithPacks(ps, p)
			}
			got, err := s.Compact(t.Context(), tt.threshold)
			if got != tt.want || (err != nil) != tt.wantErr {
				t.Errorf("Compact() = %d, %v, want %d, error %t", got, err, tt.want, tt.wantErr)
			}
			checkRepacked(t, p, tt.snapshots, crowded, tt.wantRepacked)
			checkCompacted(t, ps, tt.wantCompacted)
		})
	}
}

// checkRepacked checks that Repack was asked for the repositories of ids in want, in that order, each with its
// snapshot.
func checkRepacked(t *testing.T, p *packs, snapshots map[uuid.UUID]git.Snapshot, ids, want []uuid.UUID) {
	t.Helper()
	var repacked []uuid.UUID
	for _, id := range ids {
		snapshot, ok := p.got[id]
		if !ok {
			continue
		}
		repacked = append(repacked, id)
		if want := snapshots[id]; !slices.Equal(snapshot.Packs, want.Packs) || !slices.Equal(snapshot.Refs, want.Refs) {
			t.Errorf("Repack() of %s got %+v, want its snapshot %+v", id, snapshot, want)
		}
	}
	if !slices.Equal(repacked, want) {
		t.Errorf("repacked %v, want %v", repacked, want)
	}
}

// checkCompacted checks that the store listed the compactions in want, and no other.
func checkCompacted(t *testing.T, ps *packStore, want map[uuid.UUID]compaction) {
	t.Helper()
	if len(ps.compacted) != len(want) {
		t.Errorf("compacted %+v, want %+v", ps.compacted, want)
	}
	for id, w := range want {
		if got := ps.compacted[id]; got.pack != w.pack || !slices.Equal(got.superseded, w.superseded) {
			t.Errorf("Compact() of %s listed %+v, want %+v", id, got, w)
		}
	}
}

func TestServiceSweep(t *testing.T) {
	repoID := uuid.New()
	broken := errors.New("the bucket is down")
	old, gone, kept := uuid.New(), uuid.New(), uuid.New()
	superseded := []git.Superseded{{RepoID: repoID, Pack: git.Pack{Name: "pack-1", Upload: old}}, {RepoID: repoID, Pack: git.Pack{Name: "pack-2", Upload: gone}}}
	listed, unlisted, recent := uuid.New(), uuid.New(), uuid.New()
	bucket := []git.Upload{
		{RepoID: repoID, ID: listed, Written: time.Now().Add(-2 * git.Grace)},
		{RepoID: repoID, ID: unlisted, Written: time.Now().Add(-2 * git.Grace)},
		// A push may still be recording it.
		{RepoID: repoID, ID: recent, Written: time.Now().Add(-git.Grace / 2)},
	}
	runSweepCases(t, []sweepCase{
		{name: "superseded packs and unlisted uploads", superseded: superseded, rowUploads: []uuid.UUID{listed, kept}, bucket: bucket,
			want: git.Swept{Superseded: 2, Unlisted: 1}, wantDeleted: []uuid.UUID{old, gone, unlisted}, wantForgotten: []string{"pack-1", "pack-2"}},
		{name: "nothing to sweep", rowUploads: []uuid.UUID{listed, unlisted, recent}, bucket: bucket},
		// Its row stays, for the next sweep, and the files of the rest go.
		{name: "a superseded pack's files that fail", superseded: superseded, rowUploads: []uuid.UUID{listed}, bucket: bucket,
			deleteErr: map[uuid.UUID]error{old: broken},
			want:      git.Swept{Superseded: 1, Unlisted: 1}, wantErr: true, wantDeleted: []uuid.UUID{gone, unlisted}, wantForgotten: []string{"pack-2"}},
		{name: "a row that fails", superseded: superseded, rowUploads: []uuid.UUID{listed}, bucket: bucket, forgetErr: map[string]error{"pack-1": broken},
			want: git.Swept{Superseded: 1, Unlisted: 1}, wantErr: true, wantDeleted: []uuid.UUID{old, gone, unlisted}, wantForgotten: []string{"pack-2"}},
		{name: "an unlisted upload that fails", rowUploads: []uuid.UUID{}, bucket: bucket, deleteErr: map[uuid.UUID]error{listed: broken},
			want: git.Swept{Unlisted: 1}, wantErr: true, wantDeleted: []uuid.UUID{unlisted}},
		{name: "superseded packs that can't be listed", supersededErr: broken, rowUploads: []uuid.UUID{listed}, bucket: bucket, wantErr: true},
		{name: "a bucket that can't be listed", superseded: superseded, bucketErr: broken,
			want: git.Swept{Superseded: 2}, wantErr: true, wantDeleted: []uuid.UUID{old, gone}, wantForgotten: []string{"pack-1", "pack-2"}},
		// Nothing unlisted is deleted when the rows can't say which uploads they list.
		{name: "rows that can't be listed", rowUploadsErr: broken, bucket: bucket, wantErr: true},
		{name: "a service without packs", withoutPacks: true, superseded: superseded, bucket: bucket, wantErr: true},
	})
}

// sweepCase is a case of TestServiceSweep: what each port answers, and what Sweep should return, delete and forget.
type sweepCase struct {
	name          string
	withoutPacks  bool
	superseded    []git.Superseded
	supersededErr error
	forgetErr     map[string]error
	rowUploads    []uuid.UUID
	rowUploadsErr error
	bucket        []git.Upload
	bucketErr     error
	deleteErr     map[uuid.UUID]error
	want          git.Swept
	wantErr       bool
	wantDeleted   []uuid.UUID
	wantForgotten []string
}

// runSweepCases runs Sweep for each case.
func runSweepCases(t *testing.T, cases []sweepCase) {
	t.Helper()
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			ps := &packStore{superseded: tt.superseded, supersededErr: tt.supersededErr, forgetErr: tt.forgetErr, uploads: tt.rowUploads, uploadsErr: tt.rowUploadsErr}
			p := &packs{uploads: tt.bucket, uploadsErr: tt.bucketErr, deleteErr: tt.deleteErr}
			s := git.NewService(&store{})
			if !tt.withoutPacks {
				s = s.WithPacks(ps, p)
			}
			start := time.Now()
			got, err := s.Sweep(t.Context())
			if got != tt.want || (err != nil) != tt.wantErr {
				t.Errorf("Sweep() = %+v, %v, want %+v, error %t", got, err, tt.want, tt.wantErr)
			}
			checkSwept(t, p, ps, tt.wantDeleted, tt.wantForgotten)
			if !tt.withoutPacks && (ps.before.Before(start.Add(-git.Grace)) || ps.before.After(time.Now().Add(-git.Grace))) {
				t.Errorf("Superseded() asked before %s, want Grace before the sweep", ps.before)
			}
		})
	}
}

// checkSwept checks that the bucket deleted the uploads in wantDeleted and the store forgot the packs named in
// wantForgotten, in order.
func checkSwept(t *testing.T, p *packs, ps *packStore, wantDeleted []uuid.UUID, wantForgotten []string) {
	t.Helper()
	if !slices.Equal(p.deleted, wantDeleted) {
		t.Errorf("deleted uploads %v, want %v", p.deleted, wantDeleted)
	}
	var forgotten []string
	for _, f := range ps.forgotten {
		forgotten = append(forgotten, f.Pack.Name)
	}
	if !slices.Equal(forgotten, wantForgotten) {
		t.Errorf("forgot %v, want %v", forgotten, wantForgotten)
	}
}
