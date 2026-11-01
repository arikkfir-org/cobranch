package blob

import (
	"bytes"
	"context"
	"crypto/md5" //nolint:gosec // compares the stores' MD5s, as the move does
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"

	"gocloud.dev/blob"
	"gocloud.dev/blob/driver"
	"gocloud.dev/blob/drivertest"
	"gocloud.dev/blob/memblob"
	"gocloud.dev/gcerrors"
)

// movingHarness runs Go Cloud's conformance tests on a moving store whose old store is empty: every object is the new
// store's, so the tests see one bucket, through the moving store's reads, writes, copies, deletes and listings.
type movingHarness struct{}

func (movingHarness) MakeDriver(context.Context) (driver.Bucket, error) {
	return &moving{to: memblob.OpenBucket(nil), from: memblob.OpenBucket(nil)}, nil
}

// MakeDriverForNonexistentBucket has no bucket to miss: the stores are opened before the moving store is.
func (movingHarness) MakeDriverForNonexistentBucket(context.Context) (driver.Bucket, error) {
	return nil, nil //nolint:nilnil // drivertest's way to say the concept doesn't apply
}
func (movingHarness) HTTPClient() *http.Client         { return nil }
func (movingHarness) SupportsCopy() bool               { return true }
func (movingHarness) SupportsNonSlashDelimiters() bool { return true }
func (movingHarness) Close()                           {}

func TestMovingConformance(t *testing.T) {
	drivertest.RunConformanceTests(t, func(context.Context, *testing.T) (drivertest.Harness, error) {
		return movingHarness{}, nil
	}, nil)
}

// movingStores returns a moving store over two new stores, the new one holding toKeys and the old one fromKeys, each
// object's content its key and the store's name.
func movingStores(t *testing.T, toKeys, fromKeys []string) (bucket, to, from *blob.Bucket) {
	t.Helper()
	ctx := context.Background()
	to, from = memblob.OpenBucket(nil), memblob.OpenBucket(nil)
	for _, store := range []struct {
		bucket *blob.Bucket
		name   string
		keys   []string
	}{{to, "new", toKeys}, {from, "old", fromKeys}} {
		for _, key := range store.keys {
			if err := store.bucket.WriteAll(ctx, key, []byte(key+" in the "+store.name+" store"), nil); err != nil {
				t.Fatal(err)
			}
		}
	}
	bucket = blob.NewBucket(&moving{to: to, from: from})
	t.Cleanup(func() { _ = errors.Join(bucket.Close(), to.Close(), from.Close()) })
	return bucket, to, from
}

func TestMovingReadsAndDeletes(t *testing.T) {
	tests := []struct {
		name       string
		to, from   []string
		key        string
		want       string
		wantDelete gcerrors.ErrorCode
	}{
		{name: "the new store's", to: []string{"a"}, key: "a", want: "a in the new store"},
		{name: "the old store's", from: []string{"a"}, key: "a", want: "a in the old store"},
		{name: "both stores', the new first", to: []string{"a"}, from: []string{"a"}, key: "a", want: "a in the new store"},
		{name: "neither's", to: []string{"b"}, from: []string{"c"}, key: "a", wantDelete: gcerrors.NotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			bucket, to, from := movingStores(t, tt.to, tt.from)
			checkRead(ctx, t, bucket, tt.key, tt.want)
			if err := bucket.Delete(ctx, tt.key); gcerrors.Code(err) != tt.wantDelete {
				t.Fatalf("Delete() = %v, want code %v", err, tt.wantDelete)
			}
			for name, store := range map[string]*blob.Bucket{"new": to, "old": from} {
				if ok, err := store.Exists(ctx, tt.key); ok || err != nil {
					t.Errorf("the %s store holds %s after the delete: %t, %v", name, tt.key, ok, err)
				}
			}
		})
	}
}

// checkRead checks that key's object reads as want, whole and in part, or that no store holds it when want is empty.
func checkRead(ctx context.Context, t *testing.T, bucket *blob.Bucket, key, want string) {
	t.Helper()
	got, err := bucket.ReadAll(ctx, key)
	switch {
	case want == "":
		if gcerrors.Code(err) != gcerrors.NotFound {
			t.Errorf("ReadAll() = %q, %v; want not found", got, err)
		}
	case string(got) != want || err != nil:
		t.Errorf("ReadAll() = %q, %v; want %q", got, err, want)
	default:
		checkRange(ctx, t, bucket, key, want)
	}
}

// checkRange checks that a range of key's object, its attributes and its reader's, come from the store want is from.
func checkRange(ctx context.Context, t *testing.T, bucket *blob.Bucket, key, want string) {
	t.Helper()
	r, err := bucket.NewRangeReader(ctx, key, 2, 6, nil)
	if err != nil {
		t.Fatalf("NewRangeReader() = %v", err)
	}
	defer func() { _ = r.Close() }()
	got, err := io.ReadAll(r)
	if string(got) != want[2:8] || err != nil {
		t.Errorf("the range reads %q, %v; want %q", got, err, want[2:8])
	}
	if r.Size() != int64(len(want)) {
		t.Errorf("the reader's size is %d, want %d", r.Size(), len(want))
	}
	a, err := bucket.Attributes(ctx, key)
	if err != nil || a.Size != int64(len(want)) {
		t.Errorf("Attributes() = %+v, %v; want size %d", a, err, len(want))
	}
}

func TestMovingWrites(t *testing.T) {
	tests := []struct {
		name       string
		to, from   []string
		ifNotExist bool
		wantCode   gcerrors.ErrorCode
		wantFrom   bool
	}{
		{name: "a new key", ifNotExist: true},
		{name: "over the old store's", from: []string{"a"}, wantFrom: true},
		{name: "only adding, over the old store's", from: []string{"a"}, ifNotExist: true, wantCode: gcerrors.FailedPrecondition, wantFrom: true},
		{name: "only adding, over the new store's", to: []string{"a"}, ifNotExist: true, wantCode: gcerrors.FailedPrecondition},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			bucket, to, from := movingStores(t, tt.to, tt.from)
			err := bucket.WriteAll(ctx, "a", []byte("written"), &blob.WriterOptions{IfNotExist: tt.ifNotExist})
			if gcerrors.Code(err) != tt.wantCode {
				t.Fatalf("WriteAll() = %v, want code %v", err, tt.wantCode)
			}
			if ok, err := from.Exists(ctx, "a"); ok != tt.wantFrom || err != nil {
				t.Errorf("the old store holds it: %t, %v; want %t", ok, err, tt.wantFrom)
			}
			if tt.wantCode != gcerrors.OK {
				return
			}
			if got, err := to.ReadAll(ctx, "a"); string(got) != "written" || err != nil {
				t.Errorf("the new store holds %q, %v; want written", got, err)
			}
		})
	}
}

func TestMovingCopies(t *testing.T) {
	tests := []struct {
		name     string
		to, from []string
		want     string
		wantCode gcerrors.ErrorCode
	}{
		{name: "within the new store", to: []string{"src"}, want: "src in the new store"},
		{name: "from the old store into the new", from: []string{"src"}, want: "src in the old store"},
		{name: "the new store's first", to: []string{"src"}, from: []string{"src"}, want: "src in the new store"},
		{name: "neither's", wantCode: gcerrors.NotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			bucket, to, _ := movingStores(t, tt.to, tt.from)
			if err := bucket.Copy(ctx, "dst", "src", nil); gcerrors.Code(err) != tt.wantCode {
				t.Fatalf("Copy() = %v, want code %v", err, tt.wantCode)
			}
			if tt.wantCode != gcerrors.OK {
				return
			}
			if got, err := to.ReadAll(ctx, "dst"); string(got) != tt.want || err != nil {
				t.Errorf("the new store holds %q, %v; want %q", got, err, tt.want)
			}
		})
	}
}

// TestMovingLists lists keys spread over both stores, some in both, a page at a time, with and without a delimiter:
// each key once, in order, whatever the page size.
func TestMovingLists(t *testing.T) {
	var to, from []string
	for i := range 40 {
		key := fmt.Sprintf("git/%02d/pack", i)
		switch i % 4 {
		case 0:
			to = append(to, key)
		case 1:
			from = append(from, key)
		case 2:
			to, from = append(to, key), append(from, key)
		}
	}
	from = append(from, "git/zz", "logs/run")
	to = append(to, "git/00x", "uploads/avatar")
	bucket, _, _ := movingStores(t, to, from)
	all := slices.Compact(slices.Sorted(slices.Values(append(slices.Clone(to), from...))))
	tests := []struct {
		name      string
		prefix    string
		delimiter string
		want      []string
	}{
		{name: "everything", want: all},
		{name: "a prefix", prefix: "git/", want: slices.DeleteFunc(slices.Clone(all), func(k string) bool { return !strings.HasPrefix(k, "git/") })},
		{name: "directories", delimiter: "/", want: []string{"git/", "logs/", "uploads/"}},
		{name: "a directory's", prefix: "git/1", delimiter: "/", want: []string{"git/10/", "git/12/", "git/13/", "git/14/", "git/16/", "git/17/", "git/18/"}},
	}
	for _, tt := range tests {
		for _, size := range []int{1, 2, 3, 7, 1000} {
			t.Run(fmt.Sprintf("%s, %d a page", tt.name, size), func(t *testing.T) {
				got := listPages(t, bucket, size, &blob.ListOptions{Prefix: tt.prefix, Delimiter: tt.delimiter})
				if !slices.Equal(got, tt.want) {
					t.Errorf("listed %v, want %v", got, tt.want)
				}
			})
		}
	}
}

// listPages lists bucket a page of size at a time, and returns every key.
func listPages(t *testing.T, bucket *blob.Bucket, size int, opts *blob.ListOptions) []string {
	t.Helper()
	var keys []string
	token := blob.FirstPageToken
	for range 1000 {
		objs, next, err := bucket.ListPage(context.Background(), token, size, opts)
		if err != nil {
			t.Fatal(err)
		}
		if len(objs) > size {
			t.Fatalf("a page of %d holds %d", size, len(objs))
		}
		for _, obj := range objs {
			keys = append(keys, obj.Key)
		}
		if next == nil {
			return keys
		}
		token = next
	}
	t.Fatal("the listing never ends")
	return nil
}

func TestMove(t *testing.T) {
	tests := []struct {
		name      string
		to, from  []string
		conflict  string
		wantMoved Moved
		wantErr   bool
	}{
		{name: "nothing to move"},
		{name: "everything", from: []string{"git/a.pack", "git/a.idx", "logs/b"}, wantMoved: Moved{Copied: 3}},
		{name: "what the new store lacks", to: []string{"git/a.pack"}, from: []string{"git/a.pack", "git/a.idx"}, wantMoved: Moved{Copied: 1}},
		{name: "a key the stores disagree on", from: []string{"git/a.pack", "git/b.pack"}, conflict: "git/b.pack", wantMoved: Moved{Copied: 1, Left: 1}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			_, to, from := movingStores(t, tt.to, tt.from)
			if tt.conflict != "" {
				if err := to.WriteAll(ctx, tt.conflict, []byte("other"), nil); err != nil {
					t.Fatal(err)
				}
			}
			moved, err := Move(ctx, to, from)
			if moved != tt.wantMoved || (err != nil) != tt.wantErr {
				t.Fatalf("Move() = %+v, %v; want %+v, error %t", moved, err, tt.wantMoved, tt.wantErr)
			}
			for _, key := range slices.DeleteFunc(slices.Clone(tt.from), func(k string) bool { return k == tt.conflict }) {
				checkCopied(ctx, t, to, from, key)
			}
			again, err := Move(ctx, to, from)
			if want := (Moved{Left: tt.wantMoved.Left}); again != want || (err != nil) != tt.wantErr {
				t.Errorf("Move() again = %+v, %v; want %+v", again, err, want)
			}
		})
	}
}

// checkCopied checks that the new store holds key as the old one does, bytes, MD5 and content type, unless it held
// the key before the move.
func checkCopied(ctx context.Context, t *testing.T, to, from *blob.Bucket, key string) {
	t.Helper()
	want, err := from.Attributes(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	got, err := to.Attributes(ctx, key)
	if err != nil {
		t.Fatalf("the new store lacks %s: %v", key, err)
	}
	content, err := to.ReadAll(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasSuffix(string(content), "in the new store") {
		return
	}
	sum := md5.Sum(content) //nolint:gosec // see the import
	if string(content) != key+" in the old store" || !bytes.Equal(got.MD5, want.MD5) || !bytes.Equal(sum[:], want.MD5) || got.ContentType != want.ContentType {
		t.Errorf("the new store holds %s as %q, MD5 %x, %s; want the old store's, MD5 %x, %s", key, content, got.MD5, got.ContentType, want.MD5, want.ContentType)
	}
}

// TestOpenStores opens the store an installation names and, while its objects move, the store they move from: a
// moving store reads either, and a store can't move to itself.
func TestOpenStores(t *testing.T) {
	pool := database(t, true)
	oldDir, newDir := t.TempDir(), t.TempDir()
	tests := []struct {
		name       string
		url, from  string
		wantErr    bool
		wantMoving bool
	}{
		{name: "the database alone"},
		{name: "from a directory to the database", from: "file://" + oldDir, wantMoving: true},
		{name: "from the database to a directory", url: "file://" + newDir, from: InDatabase, wantMoving: true},
		{name: "from the database to itself", url: InDatabase, from: InDatabase, wantErr: true},
		{name: "from the database to itself, named two ways", from: InDatabase, wantErr: true},
		{name: "from a directory to itself", url: "file://" + oldDir, from: "file://" + oldDir, wantErr: true},
		{name: "from a directory to itself, named two ways", url: "file://" + oldDir, from: "file://" + oldDir + "/./?create_dir=true", wantErr: true},
		{name: "from a bucket to itself, its options aside", url: "s3://cobranch-objects", from: "s3://cobranch-objects/?region=eu-west-1", wantErr: true},
		{name: "from a store that won't open", from: "ftp://bucket", wantErr: true},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			stores, err := OpenStores(ctx, tt.url, tt.from, pool)
			if (err != nil) != tt.wantErr {
				t.Fatalf("OpenStores(%q, %q) = %v, want error %t", tt.url, tt.from, err, tt.wantErr)
			}
			if err != nil {
				return
			}
			defer func() {
				if err := stores.Close(); err != nil {
					t.Errorf("Close() = %v", err)
				}
			}()
			if stores.Moving() != tt.wantMoving {
				t.Errorf("Moving() = %t, want %t", stores.Moving(), tt.wantMoving)
			}
			checkStores(ctx, t, stores, fmt.Sprintf("git/%d.pack", i))
		})
	}
}

// checkStores checks that stores answer, keep what is written to them under key, and leave nothing to move after a move.
func checkStores(ctx context.Context, t *testing.T, stores *Stores, key string) {
	t.Helper()
	if err := stores.Check(ctx); err != nil {
		t.Errorf("Check() = %v", err)
	}
	if err := stores.Bucket.WriteAll(ctx, key, []byte("PACK"), nil); err != nil {
		t.Fatalf("WriteAll() = %v", err)
	}
	if got, err := stores.Bucket.ReadAll(ctx, key); string(got) != "PACK" || err != nil {
		t.Errorf("ReadAll() = %q, %v; want PACK", got, err)
	}
	if moved, err := stores.Move(ctx); moved.Left != 0 || err != nil {
		t.Errorf("Move() = %+v, %v; want nothing left", moved, err)
	}
}
