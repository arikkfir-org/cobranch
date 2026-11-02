package blob_test

import (
	"context"
	"maps"
	"testing"
	"time"

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

func TestLogsRead(t *testing.T) {
	ctx := context.Background()
	logs := blob.Logs{Bucket: memblob.OpenBucket(nil)}
	repo, run, other := uuid.New(), uuid.New(), uuid.New()
	for container, log := range map[string]string{"cobranch-checkout": "checked out", "main": "built"} {
		if err := logs.Put(ctx, repo, run, container, []byte(log)); err != nil {
			t.Fatal(err)
		}
	}
	if err := logs.Put(ctx, repo, other, "main", []byte("another run's")); err != nil {
		t.Fatal(err)
	}
	got, err := logs.Read(ctx, repo, run)
	if want := map[string][]byte{"cobranch-checkout": []byte("checked out"), "main": []byte("built")}; err != nil ||
		!maps.EqualFunc(got, want, func(a, b []byte) bool { return string(a) == string(b) }) {
		t.Errorf("Read() = %q, %v, want the run's two logs", got, err)
	}
	if got, err := logs.Read(ctx, repo, uuid.New()); err != nil || len(got) != 0 {
		t.Errorf("Read() of a run that kept none = %q, %v, want none", got, err)
	}
}

func TestLogsExpire(t *testing.T) {
	ctx := context.Background()
	bucket := memblob.OpenBucket(nil)
	logs := blob.Logs{Bucket: bucket}
	repo, run := uuid.New(), uuid.New()
	if err := logs.Put(ctx, repo, run, "main", []byte("built")); err != nil {
		t.Fatal(err)
	}
	if err := bucket.WriteAll(ctx, "packs/a.pack", []byte("pack"), nil); err != nil {
		t.Fatal(err)
	}
	if n, err := logs.Expire(ctx, time.Now().Add(-time.Hour)); n != 0 || err != nil {
		t.Errorf("Expire() before the log was written = %d, %v, want none", n, err)
	}
	if n, err := logs.Expire(ctx, time.Now().Add(time.Hour)); n != 1 || err != nil {
		t.Errorf("Expire() after the log was written = %d, %v, want it", n, err)
	}
	if ok, err := bucket.Exists(ctx, "packs/a.pack"); !ok || err != nil {
		t.Errorf("Exists(a pack) = %t, %v, want it kept", ok, err)
	}
	if got, err := logs.Read(ctx, repo, run); err != nil || len(got) != 0 {
		t.Errorf("Read() of an expired run = %q, %v, want none", got, err)
	}
}
