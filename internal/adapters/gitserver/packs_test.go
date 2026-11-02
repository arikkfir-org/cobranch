package gitserver_test

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	gocloudblob "gocloud.dev/blob"

	"cobranch.dev/internal/adapters/blob"
	"cobranch.dev/internal/adapters/gitserver"
	"cobranch.dev/internal/adapters/postgres"
	"cobranch.dev/internal/adapters/postgres/postgrestest"
	"cobranch.dev/internal/services/git"
)

// maintenance is the maintenance job's domain over the stack's database and bucket.
func (s *stack) maintenance() *git.Service {
	s.t.Helper()
	packs, err := gitserver.NewPacks(s.t.TempDir(), "", s.bucket)
	if err != nil {
		s.t.Fatal(err)
	}
	store := postgres.NewGitStore(s.pool)
	return git.NewService(store).WithPacks(store, packs)
}

// bucketKeys lists the bucket's keys under git/, sorted.
func (s *stack) bucketKeys() []string {
	s.t.Helper()
	var keys []string
	it := s.bucket.List(&gocloudblob.ListOptions{Prefix: "git/"})
	for {
		obj, err := it.Next(context.Background())
		if errors.Is(err, io.EOF) {
			slices.Sort(keys)
			return keys
		}
		if err != nil {
			s.t.Fatal(err)
		}
		keys = append(keys, obj.Key)
	}
}

// pushPacks pushes three commits to main and one to a branch it then deletes, from dir, each push a pack of its own,
// and returns main's head and the deleted branch's.
func pushPacks(t *testing.T, dir string) (head, feature string) {
	t.Helper()
	for _, file := range []string{"README.md", "index.html", "style.css"} {
		head = commit(t, dir, file, "# "+file+"\n")
		mustGit(t, dir, "push", "-q", "origin", "HEAD:refs/heads/main")
	}
	mustGit(t, dir, "checkout", "-q", "-b", "feature")
	feature = commit(t, dir, "draft.md", "# Draft\n")
	mustGit(t, dir, "push", "-q", "origin", "HEAD:refs/heads/feature")
	mustGit(t, dir, "push", "-q", "origin", ":refs/heads/feature")
	mustGit(t, dir, "checkout", "-q", "main")
	return head, feature
}

// checkServes checks that a clone from base is at head, and that a fetch brings feature's commit, which no ref names:
// a push that started before its branch's deletion may build on it, so compaction keeps it.
func (s *stack) checkServes(name, base, head, feature string) {
	s.t.Helper()
	other := clone(s.t, s.websiteURL(base, gwen))
	if got := strings.TrimSpace(mustGit(s.t, other, "rev-parse", "HEAD")); got != head {
		s.t.Errorf("%s: a clone is at %s, want %s", name, got, head)
	}
	if out, err := gitCmd(s.t, other, "fetch", "-q", "origin", feature); err != nil {
		s.t.Errorf("%s: fetching the deleted branch's commit: %v\n%s", name, err, out)
	}
}

// checkCached checks that the Git server of dataDir caches the website's pack and nothing else.
func checkCached(t *testing.T, dataDir, pack string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(dataDir, "cache", website.String()))
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	for _, e := range entries {
		files = append(files, e.Name())
	}
	if want := []string{pack + ".idx", pack + ".pack"}; !slices.Equal(files, want) {
		t.Errorf("the cache holds %v, want the compacted pack's %v", files, want)
	}
}

// aGraceLater makes the superseded packs' rows superseded more than Grace ago, and adds an upload no row lists,
// written as long ago, as a push that failed to record leaves.
func (s *stack) aGraceLater() {
	s.t.Helper()
	ctx := context.Background()
	stray := "git/" + website.String() + "/" + uuid.New().String() + "/pack-stray.pack"
	if err := s.bucket.WriteAll(ctx, stray, []byte("stray"), nil); err != nil {
		s.t.Fatal(err)
	}
	old := time.Now().Add(-2 * git.Grace)
	var err error
	if s.bucketDir != "" {
		err = os.Chtimes(filepath.Join(s.bucketDir, filepath.FromSlash(stray)), old, old)
	} else {
		_, err = s.pool.Exec(ctx, "UPDATE blobs SET written_at = $1 WHERE key = $2", old, []byte(stray))
	}
	if err != nil {
		s.t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, "UPDATE packs SET superseded_at = $1 WHERE state = 'superseded'", old); err != nil {
		s.t.Fatal(err)
	}
}

// TestCompactAndSweep compacts the website's packs, from four pushes and a deleted branch, into one, and sweeps what
// it replaced a Grace later, in each object store: a fresh Git server and one that cached the old packs serve the
// repository throughout, the deleted branch's commit included. In the database, no large object outlives its object.
func TestCompactAndSweep(t *testing.T) {
	t.Parallel()
	for _, store := range []string{inDatabase, inFiles} {
		t.Run(store, func(t *testing.T) {
			t.Parallel()
			compactAndSweep(t, newStackIn(t, store))
		})
	}
}

func compactAndSweep(t *testing.T, s *stack) {
	ctx := context.Background()
	cached := t.TempDir()
	base := s.serve(cached)
	dir := clone(t, s.websiteURL(base, olivia))
	head, feature := pushPacks(t, dir)
	domain := s.maintenance()
	for _, want := range []int{1, 0} {
		if got, err := domain.Compact(ctx, 2); got != want || err != nil {
			t.Fatalf("Compact() = %d, %v, want %d", got, err, want)
		}
	}
	if got := query[int](t, s.pool, "SELECT count(*) FROM packs WHERE repo_id = $1 AND state = 'superseded'", website); got != 4 {
		t.Errorf("%d superseded packs, want the four pushes'", got)
	}
	merged := query[string](t, s.pool, "SELECT name FROM packs WHERE repo_id = $1 AND state = 'active'", website)
	s.checkServes("a fresh server", s.serve(t.TempDir()), head, feature)
	s.checkServes("the server that cached the old packs", base, head, feature)
	checkCached(t, cached, merged)

	next := commit(t, dir, "about.html", "<h1>About</h1>\n")
	mustGit(t, dir, "push", "-q", "origin", "HEAD:refs/heads/main")
	before := s.bucketKeys()
	if swept, err := domain.Sweep(ctx); swept != (git.Swept{}) || err != nil {
		t.Errorf("Sweep() within Grace = %+v, %v, want nothing swept", swept, err)
	}
	if got := s.bucketKeys(); !slices.Equal(got, before) {
		t.Errorf("Sweep() within Grace left %v, want %v", got, before)
	}

	s.aGraceLater()
	if swept, err := domain.Sweep(ctx); swept != (git.Swept{Superseded: 4, Unlisted: 1}) || err != nil {
		t.Errorf("Sweep() = %+v, %v, want the four superseded packs and the stray upload", swept, err)
	}
	if got := query[int](t, s.pool, "SELECT count(*) FROM packs WHERE repo_id = $1", website); got != 2 {
		t.Errorf("%d packs listed, want the compacted pack and the last push's", got)
	}
	keys := s.bucketKeys()
	if len(keys) != 4 || !slices.ContainsFunc(keys, func(k string) bool { return strings.HasSuffix(k, "/"+merged+".pack") }) {
		t.Errorf("the bucket holds %v, want the compacted pack's files and the last push's", keys)
	}
	s.checkServes("a server after the sweep", s.serve(t.TempDir()), next, feature)
	if s.bucketDir == "" {
		if got := query[int](t, s.pool, "SELECT count(*) FROM pg_largeobject_metadata"); got != len(keys) {
			t.Errorf("the database holds %d large objects, want one per object, %d", got, len(keys))
		}
	}
}

// TestRestoredDatabaseServesTheRepository copies the database, as restoring a backup of it would, after pushes and a
// compaction, and serves the copy from a Git server with an empty cache: the database alone holds the repository
// whole, every pack included, the deleted branch's commit too.
func TestRestoredDatabaseServesTheRepository(t *testing.T) {
	t.Parallel()
	s := newStack(t)
	dir := clone(t, s.websiteURL(s.base, olivia))
	_, feature := pushPacks(t, dir)
	if got, err := s.maintenance().Compact(context.Background(), 2); got != 1 || err != nil {
		t.Fatalf("Compact() = %d, %v, want 1", got, err)
	}
	head := commit(t, dir, "about.html", "<h1>About</h1>\n")
	mustGit(t, dir, "push", "-q", "origin", "HEAD:refs/heads/main")

	restoredURL := postgrestest.Copy(t, s.databaseURL)
	pool := connect(t, restoredURL)
	restored := &stack{t: t, databaseURL: restoredURL, pool: pool, store: postgres.NewGitStore(pool), bucket: openStore(t, "", pool)}
	restored.checkServes("the restored database", restored.serve(t.TempDir()), head, feature)
}

// TestMoveBetweenStores moves an installation's packs from a directory into the database while it runs: a Git server
// over both serves the directory's packs and pushes into the database, maintenance compacts through both and copies
// the directory over, and a Git server over the database alone then serves the repository whole. The directory keeps
// every object: nothing deletes the old store.
func TestMoveBetweenStores(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStackIn(t, inFiles)
	dir := clone(t, s.websiteURL(s.base, olivia))
	_, feature := pushPacks(t, dir)
	oldKeys := s.bucketKeys()

	stores, err := blob.OpenStores(ctx, "", "file://"+s.bucketDir, s.pool)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stores.Close() })
	moving := &stack{t: t, databaseURL: s.databaseURL, pool: s.pool, store: s.store, bucket: stores.Bucket}
	base := moving.serve(t.TempDir())
	mustGit(t, dir, "remote", "set-url", "origin", moving.websiteURL(base, olivia))
	head := commit(t, dir, "about.html", "<h1>About</h1>\n")
	mustGit(t, dir, "push", "-q", "origin", "HEAD:refs/heads/main")
	moving.checkServes("a server over both stores", base, head, feature)
	if got, err := moving.maintenance().Compact(ctx, 2); got != 1 || err != nil {
		t.Fatalf("Compact() over both stores = %d, %v, want 1", got, err)
	}
	moving.checkServes("a server over both stores, after compaction", moving.serve(t.TempDir()), head, feature)

	if moved, err := stores.Move(ctx); moved.Copied != len(oldKeys) || moved.Left != 0 || err != nil {
		t.Fatalf("Move() = %+v, %v; want the directory's %d objects copied", moved, err, len(oldKeys))
	}
	alone := &stack{t: t, databaseURL: s.databaseURL, pool: s.pool, store: s.store, bucket: openStore(t, "", s.pool)}
	if keys := alone.bucketKeys(); !isSubset(oldKeys, keys) {
		t.Errorf("the database holds %v, want the directory's %v among them", keys, oldKeys)
	}
	alone.checkServes("a server over the database alone", alone.serve(t.TempDir()), head, feature)
	if got := s.bucketKeys(); !slices.Equal(got, oldKeys) {
		t.Errorf("the directory holds %v after the move, want %v as before", got, oldKeys)
	}
}

// isSubset reports whether every key of sub is in keys, both sorted.
func isSubset(sub, keys []string) bool {
	for _, k := range sub {
		if _, ok := slices.BinarySearch(keys, k); !ok {
			return false
		}
	}
	return true
}
