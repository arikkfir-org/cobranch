// Package blob opens Cobranch's object store, which holds every large or binary object, Git's packs first among them,
// as a bucket of Go Cloud's blob package, so the same code runs over each of its adapters: PostgreSQL's large objects
// by default, a cloud's bucket, or a local directory.
package blob

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"gocloud.dev/blob"
	// The drivers an operator's bucket URL may name.
	_ "gocloud.dev/blob/azureblob"
	_ "gocloud.dev/blob/fileblob"
	_ "gocloud.dev/blob/gcsblob"
	_ "gocloud.dev/blob/s3blob"

	"cobranch.dev/internal/adapters/postgres"
)

// InDatabase is the URL that names the installation's own database as the store, as an empty URL does.
const InDatabase = "postgres://"

// Open opens the store rawURL names: empty or InDatabase for the database pool connects to, where each object is a
// large object; s3://, gs:// or azblob:// for a cloud's bucket, its credentials from the environment as each cloud's
// SDK reads them (workload identity, or variables from a Secret); file:// for a directory. A file:// bucket writes
// each object's temporary file beside it, not in the system's temporary directory: in a pod, that and the bucket's
// directory are often volumes of their own, and a file can't be renamed from one to another.
func Open(ctx context.Context, rawURL string, pool *pgxpool.Pool) (*blob.Bucket, error) {
	if rawURL == "" || rawURL == InDatabase {
		return postgres.Bucket(pool), nil
	}
	if strings.HasPrefix(rawURL, "postgres") {
		return nil, fmt.Errorf("%s names the installation's own database, and takes nothing after it", InDatabase)
	}
	if strings.HasPrefix(rawURL, "file:") {
		u, err := url.Parse(rawURL)
		if err != nil {
			return nil, fmt.Errorf("parsing the bucket's file:// URL: %w", err)
		}
		if q := u.Query(); q.Get("no_tmp_dir") == "" {
			q.Set("no_tmp_dir", "true")
			u.RawQuery = q.Encode()
			rawURL = u.String()
		}
	}
	bucket, err := blob.OpenBucket(ctx, rawURL)
	if err != nil {
		return nil, fmt.Errorf("opening the bucket: %w", err)
	}
	return bucket, nil
}

// Stores is the object store as an installation configures it: the store objects live in and, while an installation
// moves them there, the store they move from.
type Stores struct {
	// Bucket is the store the Git server and maintenance use: the new one alone, or both while objects move, read
	// from the new one first and written to it alone.
	Bucket   *blob.Bucket
	to, from *blob.Bucket
}

// OpenStores opens the store rawURL names and, given fromURL, the store objects move from, as Open opens each: then
// Bucket reads either and writes the new one alone, until Move has copied the old one over and the operator stops
// naming it. A store can't move to itself, however its two URLs write it.
func OpenStores(ctx context.Context, rawURL, fromURL string, pool *pgxpool.Pool) (*Stores, error) {
	if fromURL != "" && Name(fromURL) == Name(rawURL) {
		return nil, fmt.Errorf("objects can't move from %s to itself", Name(rawURL))
	}
	to, err := Open(ctx, rawURL, pool)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", Name(rawURL), err)
	}
	if fromURL == "" {
		return &Stores{Bucket: to, to: to}, nil
	}
	from, err := Open(ctx, fromURL, pool)
	if err != nil {
		_ = to.Close() // nothing written to it yet
		return nil, fmt.Errorf("opening %s, which objects move from: %w", Name(fromURL), err)
	}
	same, err := sameStore(ctx, to, from)
	if err == nil && same {
		err = fmt.Errorf("objects can't move from %s to itself", Name(fromURL))
	}
	if err != nil {
		return nil, errors.Join(fmt.Errorf("moving from %s to %s: %w", Name(fromURL), Name(rawURL), err), to.Close(), from.Close())
	}
	return &Stores{Bucket: blob.NewBucket(&moving{to: to, from: from}), to: to, from: from}, nil
}

// probePrefix is where sameStore writes its probe objects, apart from every key Cobranch writes.
const probePrefix = ".cobranch-probe/"

// sameStore reports whether to and from reach one store's objects. Their URLs can't tell: a driver may ignore parts of
// one (a bucket's path, a directory's host) and heed others (a query's prefix or endpoint), so it writes an empty
// probe object through to, looks for it through from, and deletes it.
func sameStore(ctx context.Context, to, from *blob.Bucket) (bool, error) {
	key := probePrefix + rand.Text()
	if err := to.WriteAll(ctx, key, nil, nil); err != nil {
		return false, fmt.Errorf("writing a probe object to tell the stores apart: %w", err)
	}
	same, err := from.Exists(ctx, key)
	if err != nil {
		err = fmt.Errorf("looking for the probe object in the store objects move from: %w", err)
	}
	if delErr := to.Delete(ctx, key); delErr != nil {
		err = errors.Join(err, fmt.Errorf("deleting the probe object %s: %w", key, delErr))
	}
	return same, err
}

// Check checks that each store exists and its credentials reach it, as Check does.
func (s *Stores) Check(ctx context.Context) error {
	if err := Check(s.to)(ctx); err != nil {
		return err
	}
	if s.from == nil {
		return nil
	}
	if err := Check(s.from)(ctx); err != nil {
		return fmt.Errorf("the store objects move from: %w", err)
	}
	return nil
}

// Move copies into the new store what the old one holds and it lacks (the package's Move); with no old store, there
// is nothing to move.
func (s *Stores) Move(ctx context.Context) (Moved, error) {
	if s.from == nil {
		return Moved{}, nil
	}
	return Move(ctx, s.to, s.from)
}

// Moving reports whether objects are moving from another store.
func (s *Stores) Moving() bool { return s.from != nil }

// Close closes the stores.
func (s *Stores) Close() error {
	if s.from == nil {
		return s.to.Close() //nolint:wrapcheck // the bucket's own error, as Close's callers expect
	}
	return errors.Join(s.Bucket.Close(), s.to.Close(), s.from.Close())
}

// Name names the store rawURL names in logs and errors: the URL, or "the database" for the default.
func Name(rawURL string) string {
	if rawURL == "" || rawURL == InDatabase {
		return "the database"
	}
	return rawURL
}

// Check returns a readiness check that the store exists and its credentials reach it.
func Check(bucket *blob.Bucket) func(context.Context) error {
	return func(ctx context.Context) error {
		ok, err := bucket.IsAccessible(ctx)
		if err != nil {
			return fmt.Errorf("reaching the object store: %w", err)
		}
		if !ok {
			return errors.New("the object store does not exist")
		}
		return nil
	}
}
