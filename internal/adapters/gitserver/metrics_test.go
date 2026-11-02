package gitserver

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestOwnBytes(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]int // name to size
		links []string       // files that another path also links
		syms  []string       // symlinks to the first file
		want  int64
	}{
		{name: "nothing", want: 0},
		{name: "its own files", files: map[string]int{"HEAD": 21, "objects/pack/a.pack": 1000}, want: 1021},
		{name: "a pack the cache holds", files: map[string]int{"HEAD": 21, "objects/pack/a.pack": 1000}, links: []string{"objects/pack/a.pack"}, want: 21},
		{name: "a symlink", files: map[string]int{"HEAD": 21}, syms: []string{"link"}, want: 21},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			first := writeFiles(t, dir, tt.files)
			for i, name := range tt.links {
				if err := os.Link(filepath.Join(dir, name), filepath.Join(t.TempDir(), string(rune('a'+i)))); err != nil {
					t.Fatal(err)
				}
			}
			for _, name := range tt.syms {
				if err := os.Symlink(first, filepath.Join(dir, name)); err != nil {
					t.Fatal(err)
				}
			}
			if got := ownBytes(dir); got != tt.want {
				t.Errorf("ownBytes() = %d, want %d", got, tt.want)
			}
		})
	}
	if got := ownBytes(filepath.Join(t.TempDir(), "gone")); got != 0 {
		t.Errorf("ownBytes() of nothing = %d, want 0", got)
	}
}

// writeFiles writes files of the sizes files names under dir, and returns one of their paths.
func writeFiles(t *testing.T, dir string, files map[string]int) string {
	t.Helper()
	var last string
	for name, size := range files {
		last = filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(last), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(last, make([]byte, size), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return last
}

func TestNoMetrics(t *testing.T) {
	var m *metrics // a cache made outside a Server counts nothing
	ctx := context.Background()
	m.built(ctx, opMerge, time.Now())
	m.removed(ctx, opMerge, t.TempDir())
	m.lookedUp(ctx, true)
}
