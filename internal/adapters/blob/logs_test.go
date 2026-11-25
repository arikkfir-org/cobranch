package blob_test

import (
	"context"
	"errors"
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	gocloudblob "gocloud.dev/blob"
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

func TestLogsDropGone(t *testing.T) {
	kept, gone, other := uuid.New(), uuid.New(), uuid.New()
	failing := errors.New("the database is down")
	tests := []struct {
		name     string
		repos    []uuid.UUID // the repositories with logs
		live     []uuid.UUID
		liveErr  error
		want     int
		wantErr  bool
		wantKept []uuid.UUID
	}{
		{name: "no logs", want: 0},
		{name: "every repository there", repos: []uuid.UUID{kept, gone}, live: []uuid.UUID{kept, gone}, want: 0, wantKept: []uuid.UUID{kept, gone}},
		{name: "one gone", repos: []uuid.UUID{kept, gone}, live: []uuid.UUID{kept}, want: 2, wantKept: []uuid.UUID{kept}},
		{name: "all gone", repos: []uuid.UUID{kept, gone, other}, want: 6},
		{name: "the store fails", repos: []uuid.UUID{kept, gone}, liveErr: failing, wantErr: true, wantKept: []uuid.UUID{kept, gone}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			bucket := memblob.OpenBucket(nil)
			logs := blob.Logs{Bucket: bucket}
			putRunLogs(t, logs, tt.repos)
			if err := bucket.WriteAll(ctx, "logs/not-a-repository/x", []byte("left alone"), nil); err != nil {
				t.Fatal(err)
			}
			var asked []uuid.UUID
			n, err := logs.DropGone(ctx, func(_ context.Context, ids []uuid.UUID) ([]uuid.UUID, error) {
				asked = ids
				return tt.live, tt.liveErr
			})
			if n != tt.want || (err != nil) != tt.wantErr {
				t.Errorf("DropGone() = %d, %v, want %d deleted, error %t", n, err, tt.want, tt.wantErr)
			}
			if len(asked) != len(tt.repos) {
				t.Errorf("DropGone() asked about %v, want the %d repositories with logs", asked, len(tt.repos))
			}
			checkLogsLeft(t, bucket, tt.repos, tt.wantKept)
			if ok, err := bucket.Exists(ctx, "logs/not-a-repository/x"); !ok || err != nil {
				t.Errorf("Exists(a key of another shape) = %t, %v, want it kept", ok, err)
			}
		})
	}
}

// putRunLogs writes the logs of a run of each repository, of its checkout and its main step.
func putRunLogs(t *testing.T, logs blob.Logs, repos []uuid.UUID) {
	t.Helper()
	for _, repo := range repos {
		run := uuid.New()
		for _, container := range []string{"cobranch-checkout", "main"} {
			if err := logs.Put(context.Background(), repo, run, container, []byte("built")); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// checkLogsLeft checks that of the repositories, those kept have logs left and the others none.
func checkLogsLeft(t *testing.T, bucket *gocloudblob.Bucket, repos, kept []uuid.UUID) {
	t.Helper()
	for _, repo := range repos {
		obj, err := bucket.List(&gocloudblob.ListOptions{Prefix: "logs/" + repo.String() + "/"}).Next(context.Background())
		if left := err == nil && obj != nil; left != slices.Contains(kept, repo) {
			t.Errorf("repository %s's logs left = %t, want %t", repo, left, slices.Contains(kept, repo))
		}
	}
}
