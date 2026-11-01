package gitserver

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewEmptiesTheDataDirectory(t *testing.T) {
	dir := t.TempDir()
	left := filepath.Join(dir, workDir, "repo-1", "HEAD")
	if err := os.MkdirAll(filepath.Dir(left), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(left, []byte("ref: refs/heads/main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := New(t.Context(), Config{DataDir: dir, Hook: []string{"git-hook"}}, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if _, err := os.Stat(left); !os.IsNotExist(err) {
		t.Errorf("a file a previous container left is still there: %v", err)
	}
	hook, err := os.ReadFile(filepath.Join(dir, hooksDir, "pre-receive"))
	if err != nil || string(hook) != "#!/bin/sh\nexec 'git-hook'\n" {
		t.Errorf("the hook is %q, %v", hook, err)
	}
}
