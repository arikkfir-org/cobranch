package blob

import (
	"context"
	"os"
	"path/filepath"
	"testing"
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
			bucket, err := Open(ctx, tt.url)
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
			bucket, err := Open(ctx, "file://"+bucketDir+tt.query)
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
