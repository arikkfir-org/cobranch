package postgres

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/git"
)

// pushPacks records a push into repo of each pack named, every one a branch of its own at shaA, and returns the packs.
func (f *gitFixture) pushPacks(t *testing.T, repo uuid.UUID, names ...string) []git.Pack {
	t.Helper()
	packs := make([]git.Pack, 0, len(names))
	for _, name := range names {
		var commits []git.Commit
		if count(t, f.pool, "SELECT count(*) FROM commits WHERE repo_id = $1", repo) == 0 {
			commits = append(commits, commit(shaA, person("Olivia", "olivia@example.com")))
		}
		push := pushOf(repo, f.olivia, name, []git.RefUpdate{{Name: "refs/heads/" + name, Old: git.ZeroSHA, New: shaA}}, commits...)
		if err := f.g.Record(context.Background(), push); err != nil {
			t.Fatalf("Record() of %s = %v", name, err)
		}
		packs = append(packs, *push.Pack)
	}
	return packs
}

// activePacks is the names of repo's active packs, sorted.
func (f *gitFixture) activePacks(t *testing.T, repo uuid.UUID) []string {
	t.Helper()
	snapshot, err := f.g.Snapshot(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(snapshot.Packs))
	for _, p := range snapshot.Packs {
		names = append(names, p.Name)
	}
	slices.Sort(names)
	return names
}

func TestGitStoreCrowded(t *testing.T) {
	t.Parallel()
	f := newGitFixture(t)
	ctx := context.Background()
	f.pushPacks(t, f.website.ID, "pack-1", "pack-2", "pack-3")
	f.pushPacks(t, f.secret.ID, "pack-4")
	if _, err := f.pool.Exec(ctx, "UPDATE packs SET state = 'superseded', superseded_at = now() WHERE name = 'pack-3'"); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		threshold int
		want      []uuid.UUID
	}{
		{threshold: 1, want: sortedIDs(f.website.ID, f.secret.ID)},
		{threshold: 2, want: []uuid.UUID{f.website.ID}},
		// A superseded pack doesn't count.
		{threshold: 3, want: nil},
	}
	for _, tt := range tests {
		got, err := f.g.Crowded(ctx, tt.threshold)
		if err != nil || !slices.Equal(got, tt.want) {
			t.Errorf("Crowded(%d) = %v, %v, want %v", tt.threshold, got, err, tt.want)
		}
	}
}

func sortedIDs(ids ...uuid.UUID) []uuid.UUID {
	slices.SortFunc(ids, func(a, b uuid.UUID) int { return slices.Compare(a[:], b[:]) })
	return ids
}

func TestGitStoreCompact(t *testing.T) {
	t.Parallel()
	f := newGitFixture(t)
	ctx := context.Background()
	f.pushPacks(t, f.website.ID, "pack-1", "pack-2", "pack-3")
	merged := git.Pack{Name: "pack-merged", Upload: uuid.Must(uuid.NewV7()), Size: 300, Objects: 3}
	steps := []struct {
		name       string
		repo       uuid.UUID
		pack       git.Pack
		superseded []string
		wantErr    error
		want       []string
		wantUpload uuid.UUID
	}{
		{name: "compacting", repo: f.website.ID, pack: merged, superseded: []string{"pack-1", "pack-2", "pack-3"},
			want: []string{"pack-merged"}, wantUpload: merged.Upload},
		// Another compaction superseded pack-1 first: nothing changes, the new pack included.
		{name: "losing a race", repo: f.website.ID, pack: git.Pack{Name: "pack-other", Upload: uuid.Must(uuid.NewV7())}, superseded: []string{"pack-1"},
			wantErr: git.ErrPacksChanged, want: []string{"pack-merged"}, wantUpload: merged.Upload},
		// The repack gave a pack the repository lists already: it keeps the upload fetches may be reading.
		{name: "a pack listed already", repo: f.website.ID, pack: git.Pack{Name: "pack-merged", Upload: uuid.Must(uuid.NewV7())},
			want: []string{"pack-merged"}, wantUpload: merged.Upload},
		{name: "a repository that's gone", repo: uuid.New(), pack: merged, wantErr: git.ErrNotFound, want: []string{"pack-merged"}, wantUpload: merged.Upload},
	}
	for _, step := range steps {
		if err := f.g.Compact(ctx, step.repo, step.pack, step.superseded); !errors.Is(err, step.wantErr) {
			t.Errorf("%s: Compact() = %v, want %v", step.name, err, step.wantErr)
		}
		snapshot, err := f.g.Snapshot(ctx, f.website.ID)
		if err != nil || len(snapshot.Packs) != 1 || snapshot.Packs[0] != (git.Pack{Name: step.want[0], Upload: step.wantUpload}) {
			t.Errorf("%s: Snapshot() = %+v, %v, want %v in upload %s", step.name, snapshot.Packs, err, step.want, step.wantUpload)
		}
	}
	if got := count(t, f.pool, "SELECT count(*) FROM packs WHERE repo_id = $1 AND state = 'superseded' AND superseded_at IS NOT NULL", f.website.ID); got != 3 {
		t.Errorf("%d superseded packs, want the three compacted", got)
	}
}

func TestGitStoreSupersededAndForget(t *testing.T) {
	t.Parallel()
	f := newGitFixture(t)
	ctx := context.Background()
	pushed := f.pushPacks(t, f.website.ID, "pack-1", "pack-2")
	if err := f.g.Compact(ctx, f.website.ID, git.Pack{Name: "pack-merged", Upload: uuid.Must(uuid.NewV7())}, []string{"pack-1", "pack-2"}); err != nil {
		t.Fatal(err)
	}
	if got, err := f.g.Superseded(ctx, time.Now().Add(-time.Hour)); err != nil || len(got) != 0 {
		t.Errorf("Superseded() before the compaction = %+v, %v, want none", got, err)
	}
	got, err := f.g.Superseded(ctx, time.Now().Add(time.Second))
	want := []git.Superseded{{RepoID: f.website.ID, Pack: git.Pack{Name: "pack-1", Upload: pushed[0].Upload}},
		{RepoID: f.website.ID, Pack: git.Pack{Name: "pack-2", Upload: pushed[1].Upload}}}
	slices.SortFunc(got, func(a, b git.Superseded) int { return strings.Compare(a.Pack.Name, b.Pack.Name) })
	if err != nil || !slices.Equal(got, want) {
		t.Errorf("Superseded() = %+v, %v, want %+v", got, err, want)
	}
	// A push brings pack-2 again, in an upload of its own: its row is active again, and stays.
	again := pushOf(f.website.ID, f.olivia, "pack-2", []git.RefUpdate{{Name: "refs/heads/again", Old: git.ZeroSHA, New: shaA}})
	if err := f.g.Record(ctx, again); err != nil {
		t.Fatal(err)
	}
	steps := []struct {
		name string
		pack git.Superseded
		want int
	}{
		{name: "a pack listed in another upload", pack: git.Superseded{RepoID: f.website.ID, Pack: git.Pack{Name: "pack-1", Upload: uuid.New()}}, want: 3},
		{name: "a superseded pack", pack: want[0], want: 2},
		{name: "a pack a push made active again", pack: want[1], want: 2},
	}
	for _, step := range steps {
		if err := f.g.Forget(ctx, step.pack); err != nil {
			t.Errorf("%s: Forget() = %v", step.name, err)
		}
		if got := count(t, f.pool, "SELECT count(*) FROM packs WHERE repo_id = $1", f.website.ID); got != step.want {
			t.Errorf("%s: %d packs, want %d", step.name, got, step.want)
		}
	}
	uploads, err := f.g.Uploads(ctx)
	slices.SortFunc(uploads, func(a, b uuid.UUID) int { return slices.Compare(a[:], b[:]) })
	merged := f.activePacks(t, f.website.ID)
	if err != nil || len(uploads) != 2 || !slices.Contains(uploads, again.Pack.Upload) || !slices.Equal(merged, []string{"pack-2", "pack-merged"}) {
		t.Errorf("Uploads() = %v, %v, want pack-merged's and %s, with %v active", uploads, err, again.Pack.Upload, merged)
	}
}
