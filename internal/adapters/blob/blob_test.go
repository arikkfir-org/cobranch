package blob

import (
	"context"
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
