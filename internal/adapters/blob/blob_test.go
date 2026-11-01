package blob

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"gocloud.dev/blob"

	"cobranch.dev/db/migrations"
	"cobranch.dev/internal/adapters/postgres"
	"cobranch.dev/internal/adapters/postgres/postgrestest"
)

func TestOpenAndCheck(t *testing.T) {
	dir := t.TempDir()
	tests := []struct {
		name         string
		url          string
		wantOpenErr  bool
		wantCheckErr bool
	}{
		{name: "a directory", url: "file://" + dir},
		{name: "a missing directory", url: "file://" + filepath.Join(dir, "missing"), wantOpenErr: true},
		{name: "an unknown scheme", url: "ftp://bucket", wantOpenErr: true},
		{name: "not a URL", url: "bucket", wantOpenErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			bucket, err := Open(ctx, tt.url, nil)
			if (err != nil) != tt.wantOpenErr {
				t.Fatalf("Open(%q) error = %v, want error %t", tt.url, err, tt.wantOpenErr)
			}
			if err != nil {
				return
			}
			defer func() { _ = bucket.Close() }()
			if err := Check(bucket)(ctx); (err != nil) != tt.wantCheckErr {
				t.Errorf("Check() = %v, want error %t", err, tt.wantCheckErr)
			}
		})
	}
}

// TestWriteBesideTheObjects writes to a file:// bucket whose directory the system's temporary directory can't reach,
// as in a pod whose /tmp and bucket are volumes of their own, across which a file can't be renamed.
func TestWriteBesideTheObjects(t *testing.T) {
	dir := t.TempDir()
	tests := []struct {
		name, query string
	}{
		{name: "a plain URL"},
		{name: "a URL with other options", query: "?metadata=skip"},
		{name: "a URL that asks for it already", query: "?no_tmp_dir=true"},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			bucketDir := filepath.Join(dir, "bucket", string(rune('a'+i)))
			if err := os.MkdirAll(bucketDir, 0o700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("TMPDIR", filepath.Join(dir, "elsewhere"))
			bucket, err := Open(ctx, "file://"+bucketDir+tt.query, nil)
			if err != nil {
				t.Fatalf("Open() = %v", err)
			}
			defer func() { _ = bucket.Close() }()
			if err := bucket.WriteAll(ctx, "git/pack-1.pack", []byte("PACK"), nil); err != nil {
				t.Fatalf("WriteAll() = %v", err)
			}
			if got, err := os.ReadFile(filepath.Join(bucketDir, "git", "pack-1.pack")); err != nil || string(got) != "PACK" {
				t.Errorf("the object holds %q, %v; want PACK", got, err)
			}
		})
	}
}

// TestOpenInDatabase opens the default store, the database's large objects, which is ready once the migrations made
// its table.
func TestOpenInDatabase(t *testing.T) {
	tests := []struct {
		name         string
		url          string
		migrated     bool
		wantCheckErr bool
	}{
		{name: "no URL", url: "", migrated: true},
		{name: "the database named", url: InDatabase, migrated: true},
		{name: "before the migrations", url: "", wantCheckErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pool := database(t, tt.migrated)
			ctx := context.Background()
			bucket, err := Open(ctx, tt.url, pool)
			if err != nil {
				t.Fatalf("Open(%q) = %v", tt.url, err)
			}
			defer func() { _ = bucket.Close() }()
			if err := Check(bucket)(ctx); (err != nil) != tt.wantCheckErr {
				t.Errorf("Check() = %v, want error %t", err, tt.wantCheckErr)
			}
			if !tt.wantCheckErr {
				roundTrip(ctx, t, bucket)
			}
		})
	}
}

// database connects to a new database, closed when the test ends, with Cobranch's schema if migrated.
func database(t *testing.T, migrated bool) *pgxpool.Pool {
	t.Helper()
	url := postgrestest.Database(t)
	ctx := context.Background()
	pool, err := postgres.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if !migrated {
		return pool
	}
	schema, err := postgres.NewSchema(pool, migrations.FS, migrations.Go...)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = schema.Close() }() // holds nothing the test wrote
	if err := schema.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return pool
}

// roundTrip writes an object to bucket and reads it back.
func roundTrip(ctx context.Context, t *testing.T, bucket *blob.Bucket) {
	t.Helper()
	if err := bucket.WriteAll(ctx, "git/pack-1.pack", []byte("PACK"), nil); err != nil {
		t.Fatalf("WriteAll() = %v", err)
	}
	if got, err := bucket.ReadAll(ctx, "git/pack-1.pack"); err != nil || string(got) != "PACK" {
		t.Errorf("the object holds %q, %v; want PACK", got, err)
	}
}

func TestName(t *testing.T) {
	for url, want := range map[string]string{"": "the database", InDatabase: "the database", "gs://objects": "gs://objects"} {
		if got := Name(url); got != want {
			t.Errorf("Name(%q) = %q, want %q", url, got, want)
		}
	}
}
