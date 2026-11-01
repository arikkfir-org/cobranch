package gitserver

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/google/uuid"
	gocloudblob "gocloud.dev/blob"

	"cobranch.dev/internal/adapters/blob"
	"cobranch.dev/internal/services/git"
)

// testUpload is the upload every test pack's files are in.
var testUpload = uuid.MustParse("01a19711-0000-7000-8000-000000000001")

// testPack is the test pack of that name.
func testPack(name string) git.Pack {
	return git.Pack{Name: name, Upload: testUpload}
}

// newTestCache is a cache of limit bytes over a file:// bucket that holds packs, each of its repositories' packs
// named there, 10 bytes in all.
func newTestCache(t *testing.T, limit int64, packs map[uuid.UUID][]string) (*cache, *gocloudblob.Bucket) {
	t.Helper()
	bucket, err := blob.Open(t.Context(), "file://"+t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bucket.Close() })
	for repo, names := range packs {
		for _, name := range names {
			for suffix, content := range map[string]string{".idx": "index", ".pack": "packs"} {
				if err := bucket.WriteAll(t.Context(), packKey(repo, testUpload, name+suffix), []byte(content), nil); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, incomingDir), 0o700); err != nil {
		t.Fatal(err)
	}
	return newCache(dir, limit, bucket), bucket
}

// linkInto links pack of repo into a new directory, and fails the test unless both its files are there.
func linkInto(t *testing.T, c *cache, repo uuid.UUID, pack string) {
	t.Helper()
	dir := t.TempDir()
	if err := c.link(t.Context(), repo, testPack(pack), dir); err != nil {
		t.Fatalf("link(%s): %v", pack, err)
	}
	for _, suffix := range packFiles {
		if _, err := os.Stat(filepath.Join(dir, pack+suffix)); err != nil {
			t.Errorf("link(%s) left no %s: %v", pack, suffix, err)
		}
	}
}

func TestCacheLink(t *testing.T) {
	a := uuid.New()
	c, bucket := newTestCache(t, 1<<20, map[uuid.UUID][]string{a: {"pack-1"}})
	linkInto(t, c, a, "pack-1")
	// Once cached, a pack no longer needs the bucket.
	for _, suffix := range packFiles {
		if err := bucket.Delete(t.Context(), packKey(a, testUpload, "pack-1"+suffix)); err != nil {
			t.Fatal(err)
		}
	}
	linkInto(t, c, a, "pack-1")
	if c.size != 10 || c.repos[a].size != 10 {
		t.Errorf("the cache counts %d bytes, %d of them the repository's; want 10", c.size, c.repos[a].size)
	}
	if err := c.link(t.Context(), a, testPack("pack-2"), t.TempDir()); err == nil {
		t.Error("link() of a pack the bucket lacks succeeded")
	}
}

func TestCacheEvicts(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	c, _ := newTestCache(t, 25, map[uuid.UUID][]string{a: {"pack-1", "pack-2"}, b: {"pack-3"}})
	linkInto(t, c, a, "pack-1")
	linkInto(t, c, a, "pack-2")
	linkInto(t, c, b, "pack-3")
	// a's 20 bytes and b's 10 pass the limit, so a, used least recently, goes; b, just added, stays.
	if _, ok := c.repos[a]; ok || c.size != 10 {
		t.Fatalf("the cache holds %d bytes and repository a: %t; want only b's 10", c.size, ok)
	}
	if _, err := os.Stat(c.path(a, "pack-1.pack")); !os.IsNotExist(err) {
		t.Errorf("a's pack is still on disk: %v", err)
	}
	// An evicted pack comes back from the bucket, and fits beside b's.
	linkInto(t, c, a, "pack-1")
	if c.repos[b] == nil || c.size != 20 {
		t.Errorf("the cache holds %d bytes and repository b: %t; want a's 10 and b's", c.size, c.repos[b] != nil)
	}
}

func TestCacheKeepsAPackLargerThanTheLimit(t *testing.T) {
	a := uuid.New()
	c, _ := newTestCache(t, 5, map[uuid.UUID][]string{a: {"pack-1"}})
	linkInto(t, c, a, "pack-1")
	if c.repos[a] == nil {
		t.Error("the cache evicted the repository it just added")
	}
}

func TestCacheAtOnce(t *testing.T) {
	a := uuid.New()
	c, _ := newTestCache(t, 1<<20, map[uuid.UUID][]string{a: {"pack-1"}})
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() { linkInto(t, c, a, "pack-1") })
	}
	wg.Wait()
	if c.size != 10 {
		t.Errorf("the cache counts %d bytes after eight links at once, want the pack's 10", c.size)
	}
}
