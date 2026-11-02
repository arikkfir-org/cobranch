package blob_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"gocloud.dev/blob/memblob"

	"cobranch.dev/internal/adapters/blob"
)

func TestLogs(t *testing.T) {
	ctx := context.Background()
	bucket := memblob.OpenBucket(nil)
	repo, run := uuid.New(), uuid.New()
	logs := blob.Logs{Bucket: bucket}
	for _, log := range []string{"first", "second"} {
		if err := logs.Put(ctx, repo, run, "main", []byte(log)); err != nil {
			t.Fatal(err)
		}
	}
	key := "logs/" + repo.String() + "/" + run.String() + "/main"
	if got, err := bucket.ReadAll(ctx, key); err != nil || string(got) != "second" {
		t.Errorf("ReadAll(%s) = %q, %v, want the last log written", key, got, err)
	}
	if attrs, err := bucket.Attributes(ctx, key); err != nil || attrs.ContentType != "text/plain; charset=utf-8" {
		t.Errorf("Attributes() = %+v, %v, want plain text", attrs, err)
	}
}
