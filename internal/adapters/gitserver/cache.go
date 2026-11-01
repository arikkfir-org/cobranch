package gitserver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"gocloud.dev/blob"
	"golang.org/x/sync/singleflight"

	"cobranch.dev/internal/services/git"
)

// packFiles are the files of a pack, each named <pack><suffix>.
var packFiles = []string{".idx", ".pack"}

// packsPrefix is where the bucket keeps every repository's packs.
const packsPrefix = "git/"

// packKey is where the bucket keeps a file of one upload of a repository's pack.
func packKey(repoID, upload uuid.UUID, file string) string {
	return packsPrefix + repoID.String() + "/" + upload.String() + "/" + file
}

// parsePackKey reads the repository and the upload from a key packKey made, and reports whether it is one.
func parsePackKey(key string) (repoID, upload uuid.UUID, ok bool) {
	parts := strings.Split(strings.TrimPrefix(key, packsPrefix), "/")
	if !strings.HasPrefix(key, packsPrefix) || len(parts) != 3 || parts[2] == "" {
		return uuid.Nil, uuid.Nil, false
	}
	repoID, repoErr := uuid.Parse(parts[0])
	upload, uploadErr := uuid.Parse(parts[1])
	return repoID, upload, repoErr == nil && uploadErr == nil
}

// cache keeps packs from the bucket on the pod's disk, a directory per repository, and evicts the least recently used
// repositories' packs once they pass its limit. It keeps a pack by its name, whichever upload it came from: a pack's
// name is its content's hash. A fetch hard-links the packs it serves from the cache, so eviction may
// delete any file at any time: a fetch in flight keeps the packs it linked.
type cache struct {
	dir    string
	limit  int64
	bucket *blob.Bucket
	flight singleflight.Group

	mu    sync.Mutex
	size  int64
	repos map[uuid.UUID]*cachedRepo
}

// cachedRepo is what the cache holds of one repository.
type cachedRepo struct {
	size  int64
	used  time.Time
	packs map[string]cachedPack
}

// holds reports whether r, which may be nil, holds pack.
func (r *cachedRepo) holds(pack string) bool {
	if r == nil {
		return false
	}
	_, ok := r.packs[pack]
	return ok
}

// cachedPack is one pack the cache holds: how many bytes its files hold, and when the cache took it.
type cachedPack struct {
	size  int64
	added time.Time
}

func newCache(dir string, limit int64, bucket *blob.Bucket) *cache {
	return &cache{dir: dir, limit: limit, bucket: bucket, repos: map[uuid.UUID]*cachedRepo{}}
}

func (c *cache) path(repoID uuid.UUID, file string) string {
	return filepath.Join(c.dir, repoID.String(), file)
}

// link hard-links pack's files into dir, fetching them from the bucket first when the cache lacks them, and again
// when an eviction deleted them in between.
func (c *cache) link(ctx context.Context, repoID uuid.UUID, pack git.Pack, dir string) error {
	for attempt := 0; ; attempt++ {
		if err := c.ensure(ctx, repoID, pack); err != nil {
			return err
		}
		err := linkFiles(func(file string) string { return c.path(repoID, file) }, pack.Name, dir)
		if err == nil || !errors.Is(err, fs.ErrNotExist) || attempt > 0 {
			return err
		}
	}
}

// linkFiles hard-links pack's files from where from says into dir. A file dir holds already is the same pack's, which
// an attempt that an eviction cut short linked: a pack's name is its content's hash.
func linkFiles(from func(file string) string, pack, dir string) error {
	for _, suffix := range packFiles {
		if err := os.Link(from(pack+suffix), filepath.Join(dir, pack+suffix)); err != nil && !errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("linking %s%s: %w", pack, suffix, err)
		}
	}
	return nil
}

// ensure makes the cache hold pack, downloading it once however many requests want it at once.
func (c *cache) ensure(ctx context.Context, repoID uuid.UUID, pack git.Pack) error {
	c.mu.Lock()
	r := c.repos[repoID]
	if r.holds(pack.Name) {
		r.used = time.Now()
		c.mu.Unlock()
		return nil
	}
	c.mu.Unlock()
	_, err, _ := c.flight.Do(repoID.String()+"/"+pack.Name, func() (any, error) {
		return nil, c.download(context.WithoutCancel(ctx), repoID, pack)
	})
	if err != nil {
		return fmt.Errorf("fetching pack %s of repository %s: %w", pack.Name, repoID, err)
	}
	return nil
}

// download fetches pack's files from the bucket into the cache's incoming directory, then moves them in.
func (c *cache) download(ctx context.Context, repoID uuid.UUID, pack git.Pack) error {
	incoming, err := os.MkdirTemp(filepath.Join(c.dir, incomingDir), "pack-")
	if err != nil {
		return fmt.Errorf("making a directory to download into: %w", err)
	}
	defer func() { _ = os.RemoveAll(incoming) }() // emptied by the move, or left from a failure
	var size int64
	for _, suffix := range packFiles {
		n, err := fetch(ctx, c.bucket, packKey(repoID, pack.Upload, pack.Name+suffix), filepath.Join(incoming, pack.Name+suffix))
		if err != nil {
			return err
		}
		size += n
	}
	return c.add(repoID, pack.Name, size, func(file string) string { return filepath.Join(incoming, file) }, os.Rename)
}

// fetch copies the bucket's object key into file, and returns its size.
func fetch(ctx context.Context, bucket *blob.Bucket, key, file string) (int64, error) {
	r, err := bucket.NewReader(ctx, key, nil)
	if err != nil {
		return 0, fmt.Errorf("reading %s from the bucket: %w", key, err)
	}
	defer func() { _ = r.Close() }() // read to its end below
	f, err := os.Create(file)
	if err != nil {
		return 0, fmt.Errorf("creating %s: %w", file, err)
	}
	n, err := io.Copy(f, r)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return 0, fmt.Errorf("downloading %s: %w", key, err)
	}
	return n, nil
}

// add moves or links pack's files, where from says they are, into the cache with put (os.Rename or os.Link), counts
// them, and evicts other repositories' packs while the cache is past its limit.
func (c *cache) add(repoID uuid.UUID, pack string, size int64, from func(file string) string, put func(from, to string) error) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	r := c.repos[repoID]
	if r.holds(pack) {
		r.used = time.Now()
		return nil
	}
	if err := os.MkdirAll(filepath.Join(c.dir, repoID.String()), 0o700); err != nil {
		return fmt.Errorf("making the cache's directory: %w", err)
	}
	for _, suffix := range packFiles {
		if err := put(from(pack+suffix), c.path(repoID, pack+suffix)); err != nil {
			return fmt.Errorf("caching %s%s: %w", pack, suffix, err)
		}
	}
	if r == nil {
		r = &cachedRepo{packs: map[string]cachedPack{}}
		c.repos[repoID] = r
	}
	r.packs[pack], r.size, r.used = cachedPack{size: size, added: time.Now()}, r.size+size, time.Now()
	c.size += size
	c.evict(repoID)
	return nil
}

// retain deletes the repository's packs that the cache took before read and that packs, a snapshot read then, doesn't
// list: compaction superseded them, and no later snapshot lists them again. One a push was still recording at the read
// is downloaded again by the first request whose snapshot lists it, as is one a request with an older snapshot links.
func (c *cache) retain(repoID uuid.UUID, packs []git.Pack, read time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	r := c.repos[repoID]
	if r == nil {
		return
	}
	listed := make(map[string]bool, len(packs))
	for _, p := range packs {
		listed[p.Name] = true
	}
	for name, p := range r.packs {
		if listed[name] || !p.added.Before(read) {
			continue
		}
		for _, suffix := range packFiles {
			// A file left behind is a file the next eviction of the directory, or the pod's end, deletes.
			_ = os.Remove(c.path(repoID, name+suffix))
		}
		delete(r.packs, name)
		r.size -= p.size
		c.size -= p.size
	}
}

// evict deletes the least recently used repositories' packs, all but keep's, until the cache fits its limit.
func (c *cache) evict(keep uuid.UUID) {
	for c.size > c.limit {
		var oldest uuid.UUID
		for id, r := range c.repos {
			if id != keep && (oldest == uuid.Nil || r.used.Before(c.repos[oldest].used)) {
				oldest = id
			}
		}
		if oldest == uuid.Nil {
			return
		}
		// A file left behind is a file the next eviction of the directory, or the pod's end, deletes.
		_ = os.RemoveAll(filepath.Join(c.dir, oldest.String()))
		c.size -= c.repos[oldest].size
		delete(c.repos, oldest)
	}
}
