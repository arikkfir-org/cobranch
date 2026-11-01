// Package blob opens Cobranch's one bucket, which holds every large or binary object, through Go Cloud's blob package,
// so the same code runs on any cloud's buckets or on a local directory.
package blob

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"gocloud.dev/blob"
	// The drivers an operator's bucket URL may name.
	_ "gocloud.dev/blob/azureblob"
	_ "gocloud.dev/blob/fileblob"
	_ "gocloud.dev/blob/gcsblob"
	_ "gocloud.dev/blob/s3blob"
)

// Open opens the bucket rawURL names: s3://, gs://, azblob:// or file://. Credentials come from the environment, as
// each cloud's SDK reads them (workload identity, or variables from a Secret). A file:// bucket writes each object's
// temporary file beside it, not in the system's temporary directory: in a pod, that and the bucket's directory are
// often volumes of their own, and a file can't be renamed from one to another.
func Open(ctx context.Context, rawURL string) (*blob.Bucket, error) {
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

// Check returns a readiness check that the bucket exists and its credentials reach it.
func Check(bucket *blob.Bucket) func(context.Context) error {
	return func(ctx context.Context) error {
		ok, err := bucket.IsAccessible(ctx)
		if err != nil {
			return fmt.Errorf("reaching the bucket: %w", err)
		}
		if !ok {
			return errors.New("the bucket does not exist")
		}
		return nil
	}
}
