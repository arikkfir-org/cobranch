package gitserver

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

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
	bucket, err := blob.Open(t.Context(), "file://"+t.TempDir(), nil)
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

// TestCacheLinkAfterAPartialLink links a pack again after an eviction deleted its files halfway through a link,
// which had linked the index alone.
func TestCacheLinkAfterAPartialLink(t *testing.T) {
	a := uuid.New()
	c, _ := newTestCache(t, 1<<20, map[uuid.UUID][]string{a: {"pack-1"}})
	linkInto(t, c, a, "pack-1")
	dir := t.TempDir()
	if err := os.Link(c.path(a, "pack-1.idx"), filepath.Join(dir, "pack-1.idx")); err != nil {
		t.Fatal(err)
	}
	c.mu.Lock()
	c.limit = 0 // as another repository's pack, just added, would
	c.evict(uuid.Nil)
	c.limit = 1 << 20
	c.mu.Unlock()
	if _, ok := c.repos[a]; ok {
		t.Fatal("the eviction kept the repository")
	}
	if err := c.link(t.Context(), a, testPack("pack-1"), dir); err != nil {
		t.Fatalf("link() after a partial link = %v", err)
	}
	for _, suffix := range packFiles {
		if _, err := os.Stat(filepath.Join(dir, "pack-1"+suffix)); err != nil {
			t.Errorf("link() left no %s: %v", suffix, err)
		}
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

func TestParsePackKey(t *testing.T) {
	repo, upload := uuid.New(), uuid.New()
	tests := []struct {
		name   string
		key    string
		wantOK bool
	}{
		{name: "a pack's index", key: packKey(repo, upload, "pack-1.idx"), wantOK: true},
		{name: "a pack", key: packKey(repo, upload, "pack-1.pack"), wantOK: true},
		{name: "an upload without a file", key: packKey(repo, upload, "")},
		{name: "a file deeper down", key: packKey(repo, upload, "x/pack-1.pack")},
		{name: "a repository's directory", key: "git/" + repo.String() + "/pack-1.pack"},
		{name: "an upload that isn't one", key: "git/" + repo.String() + "/upload/pack-1.pack"},
		{name: "a repository that isn't one", key: "git/website/" + upload.String() + "/pack-1.pack"},
		{name: "another prefix", key: "blobs/" + repo.String() + "/" + upload.String() + "/pack-1.pack"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotRepo, gotUpload, ok := parsePackKey(tt.key)
			if ok != tt.wantOK || (ok && (gotRepo != repo || gotUpload != upload)) {
				t.Errorf("parsePackKey(%q) = %s, %s, %t, want %s, %s, %t", tt.key, gotRepo, gotUpload, ok, repo, upload, tt.wantOK)
			}
		})
	}
}

// TestCacheRetain drops the packs a snapshot no longer lists, once compaction superseded them, and keeps the ones
// it took after the snapshot was read.
func TestCacheRetain(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	c, _ := newTestCache(t, 1<<20, map[uuid.UUID][]string{a: {"pack-1", "pack-2", "pack-3"}, b: {"pack-4"}})
	linkInto(t, c, a, "pack-1")
	linkInto(t, c, a, "pack-2")
	linkInto(t, c, b, "pack-4")
	read := time.Now()
	linkInto(t, c, a, "pack-3")
	c.retain(a, []git.Pack{testPack("pack-2")}, read)
	c.retain(uuid.New(), nil, time.Now())
	for pack, want := range map[string]bool{"pack-1": false, "pack-2": true, "pack-3": true} {
		if got := c.repos[a].holds(pack); got != want {
			t.Errorf("the cache holds %s: %t, want %t", pack, got, want)
		}
		if _, err := os.Stat(c.path(a, pack+".pack")); (err == nil) != want {
			t.Errorf("%s is on disk: %v, want %t", pack, err, want)
		}
	}
	if !c.repos[b].holds("pack-4") || c.size != 30 || c.repos[a].size != 20 {
		t.Errorf("the cache counts %d bytes, %d of them a's, and holds b's pack: %t; want 30, 20 and true", c.size, c.repos[a].size, c.repos[b].holds("pack-4"))
	}
	// A request with an older snapshot links the dropped pack again, from the bucket.
	linkInto(t, c, a, "pack-1")
}
