// Package blob opens Cobranch's object store, which holds every large or binary object, Git's packs first among them,
// as a bucket of Go Cloud's blob package, so the same code runs over each of its adapters: PostgreSQL's large objects
// by default, a cloud's bucket, or a local directory.
package blob

import (
	"context"
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
