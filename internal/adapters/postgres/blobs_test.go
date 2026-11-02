package postgres

import (
	"bytes"
	"context"
	"crypto/rand"
	"io"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"gocloud.dev/blob"
	"gocloud.dev/blob/driver"
	"gocloud.dev/blob/drivertest"
	"gocloud.dev/gcerrors"

	"cobranch.dev/db/migrations"
	"cobranch.dev/internal/adapters/postgres/queries"
)

// blobDatabase returns a pool connected to a new database with Cobranch's schema, dropped when the test ends.
func blobDatabase(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := testDatabase(t)
	if err := newSchema(t, pool, migrations.FS, migrations.Go...).Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return pool
}

// blobHarness runs Go Cloud's conformance tests on one database, as a bucket's run on one bucket.
type blobHarness struct{ pool *pgxpool.Pool }

func (h blobHarness) MakeDriver(context.Context) (driver.Bucket, error) {
	return &blobStore{pool: h.pool, q: queries.New(h.pool)}, nil
}

// MakeDriverForNonexistentBucket has no bucket to miss: the store is a table of the database.
func (h blobHarness) MakeDriverForNonexistentBucket(context.Context) (driver.Bucket, error) {
	return nil, nil //nolint:nilnil // drivertest's way to say the concept doesn't apply
}
func (h blobHarness) HTTPClient() *http.Client         { return nil }
func (h blobHarness) SupportsCopy() bool               { return true }
func (h blobHarness) SupportsNonSlashDelimiters() bool { return true }
func (h blobHarness) Close()                           {}

func TestBlobStoreConformance(t *testing.T) {
	t.Parallel()
	pool := blobDatabase(t)
	drivertest.RunConformanceTests(t, func(context.Context, *testing.T) (drivertest.Harness, error) {
		return blobHarness{pool: pool}, nil
	}, nil)
}

// largeObjects counts the database's large objects.
func largeObjects(ctx context.Context, t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM pg_largeobject_metadata").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// randomBytes returns n random bytes.
func randomBytes(t *testing.T, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return b
}

// packAct acts on a bucket that holds large under "pack" and returns what "pack" should then hold, nil for nothing.
type packAct func(ctx context.Context, t *testing.T, b *blob.Bucket, large []byte) []byte

func keepPack(_ context.Context, _ *testing.T, _ *blob.Bucket, large []byte) []byte { return large }

func overwritePack(ctx context.Context, t *testing.T, b *blob.Bucket, _ []byte) []byte {
	if err := b.WriteAll(ctx, "pack", []byte("smaller"), nil); err != nil {
		t.Fatal(err)
	}
	return []byte("smaller")
}

func deletePack(ctx context.Context, t *testing.T, b *blob.Bucket, _ []byte) []byte {
	if err := b.Delete(ctx, "pack"); err != nil {
		t.Fatal(err)
	}
	return nil
}

func cancelWrite(ctx context.Context, t *testing.T, b *blob.Bucket, large []byte) []byte {
	canceled, cancel := context.WithCancel(ctx)
	w, err := b.NewWriter(canceled, "pack", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(randomBytes(t, 2*blobChunk)); err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := w.Close(); err == nil {
		t.Error("a canceled write closed without an error")
	}
	return large
}

func addOverPack(ctx context.Context, t *testing.T, b *blob.Bucket, large []byte) []byte {
	err := b.WriteAll(ctx, "pack", randomBytes(t, blobChunk+1), &blob.WriterOptions{IfNotExist: true})
	if gcerrors.Code(err) != gcerrors.FailedPrecondition {
		t.Errorf("adding over an object: %v, want FailedPrecondition", err)
	}
	return large
}

func copyPack(ctx context.Context, t *testing.T, b *blob.Bucket, large []byte) []byte {
	if err := b.Copy(ctx, "copy", "pack", nil); err != nil {
		t.Fatal(err)
	}
	if got, err := b.ReadAll(ctx, "copy"); err != nil || !bytes.Equal(got, large) {
		t.Errorf("the copy holds %d bytes (%v), want the %d written", len(got), err, len(large))
	}
	return large
}

func TestBlobStoreLargeObjects(t *testing.T) {
	t.Parallel()
	large := randomBytes(t, 2*blobChunk+blobChunk/2)
	tests := []struct {
		name        string
		act         packAct
		wantObjects int
	}{
		{name: "written once", act: keepPack, wantObjects: 1},
		{name: "overwritten, the old large object deleted", act: overwritePack, wantObjects: 1},
		{name: "deleted with its large object", act: deletePack, wantObjects: 0},
		{name: "a write canceled midway leaves no large object", act: cancelWrite, wantObjects: 1},
		{name: "a write that may only add leaves the object", act: addOverPack, wantObjects: 1},
		{name: "copied whole", act: copyPack, wantObjects: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			checkPackAct(context.Background(), t, blobDatabase(t), large, tt.act, tt.wantObjects)
		})
	}
}

// checkPackAct writes large under "pack", runs act, and checks what "pack" then holds and how many large objects
// the database keeps.
func checkPackAct(ctx context.Context, t *testing.T, pool *pgxpool.Pool, large []byte, act packAct, wantObjects int) {
	t.Helper()
	b := Bucket(pool)
	if err := b.WriteAll(ctx, "pack", large, nil); err != nil {
		t.Fatal(err)
	}
	want := act(ctx, t, b, large)
	got, err := b.ReadAll(ctx, "pack")
	switch {
	case want == nil && gcerrors.Code(err) != gcerrors.NotFound:
		t.Errorf("reading a deleted object: %v, want NotFound", err)
	case want != nil && (err != nil || !bytes.Equal(got, want)):
		t.Errorf("read %d bytes (%v), want %d", len(got), err, len(want))
	}
	if n := largeObjects(ctx, t, pool); n != wantObjects {
		t.Errorf("%d large objects, want %d", n, wantObjects)
	}
}

func TestBlobStoreRangeAcrossChunks(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	b := Bucket(blobDatabase(t))
	content := randomBytes(t, 3*blobChunk)
	if err := b.WriteAll(ctx, "pack", content, nil); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name           string
		offset, length int64
		want           []byte
	}{
		{name: "across a chunk's end", offset: blobChunk - 10, length: 20, want: content[blobChunk-10 : blobChunk+10]},
		{name: "to the end", offset: 2*blobChunk + 5, length: -1, want: content[2*blobChunk+5:]},
		{name: "past the end", offset: 3*blobChunk - 4, length: 100, want: content[3*blobChunk-4:]},
		{name: "beyond the end", offset: 4 * blobChunk, length: 10, want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := b.NewRangeReader(ctx, "pack", tt.offset, tt.length, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = r.Close() }()
			got, err := io.ReadAll(r)
			if err != nil || !bytes.Equal(got, tt.want) {
				t.Errorf("read %d bytes (%v), want %d", len(got), err, len(tt.want))
			}
		})
	}
}

func TestBlobStoreReadDeleted(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	b := Bucket(blobDatabase(t))
	if err := b.WriteAll(ctx, "pack", randomBytes(t, 2*blobChunk), nil); err != nil {
		t.Fatal(err)
	}
	r, err := b.NewReader(ctx, "pack", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	if err := b.Delete(ctx, "pack"); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(r); err == nil {
		t.Error("read an object deleted under its reader without an error")
	}
}

func TestKeyAfter(t *testing.T) {
	tests := []struct {
		prefix, want []byte
	}{
		{prefix: nil, want: nil},
		{prefix: []byte("git/"), want: []byte("git0")},
		{prefix: []byte("a\xff"), want: []byte("b")},
		{prefix: []byte("\xff\xff"), want: nil},
		{prefix: []byte("é"), want: []byte("\xc3\xaa")},
	}
	for _, tt := range tests {
		if got := keyAfter(tt.prefix); !bytes.Equal(got, tt.want) {
			t.Errorf("keyAfter(%q) = %q, want %q", tt.prefix, got, tt.want)
		}
	}
}
