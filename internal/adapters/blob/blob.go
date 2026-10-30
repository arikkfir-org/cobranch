// Package blob opens Cobranch's one bucket, which holds every large or binary object, through Go Cloud's blob package,
// so the same code runs on any cloud's buckets or on a local directory.
package blob

import (
	"context"
	"errors"
	"fmt"

	"gocloud.dev/blob"
	// The drivers an operator's bucket URL may name.
	_ "gocloud.dev/blob/azureblob"
	_ "gocloud.dev/blob/fileblob"
	_ "gocloud.dev/blob/gcsblob"
	_ "gocloud.dev/blob/s3blob"
)

// Open opens the bucket url names: s3://, gs://, azblob:// or file://. Credentials come from the environment, as each
// cloud's SDK reads them (workload identity, or variables from a Secret).
func Open(ctx context.Context, url string) (*blob.Bucket, error) {
	bucket, err := blob.OpenBucket(ctx, url)
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
