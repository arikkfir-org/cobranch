package main

import (
	"context"
	"io"
	"strings"
	"testing"

	"cobranch.dev/internal/adapters/postgres/postgrestest"
)

// TestRunGitWorkloads runs a push worker and a Git server on one database, and checks that both turn ready once it
// is migrated and that the Git server asks a client without a credential for one.
func TestRunGitWorkloads(t *testing.T) {
	worker, gitServer := freeAddr(t), freeAddr(t)
	t.Setenv("COBRANCH_DATABASE_URL", postgrestest.Database(t))
	t.Setenv("COBRANCH_BLOB_URL", "file://"+t.TempDir())
	t.Setenv("COBRANCH_INTERNAL_TOKEN", "internal")
	t.Setenv("COBRANCH_PUSH_WORKER_URL", "http://"+worker)
	t.Setenv("COBRANCH_GIT_DATA_DIR", t.TempDir())
	t.Setenv("COBRANCH_HTTP_ADDR", worker)
	start(t, "push-worker", runPushWorker)
	if status, _ := waitReady(t, worker, func(status int) bool { return status != 0 }); status != 503 {
		t.Fatalf("the push worker's /readyz before cobranch migrate = %d, want 503", status)
	}
	t.Setenv("COBRANCH_HTTP_ADDR", gitServer)
	start(t, "git-server", runGitServer)
	if err := runMigrate(context.Background(), io.Discard); err != nil {
		t.Fatalf("runMigrate() = %v", err)
	}
	for name, addr := range map[string]string{"push worker": worker, "Git server": gitServer} {
		if status, body := waitReady(t, addr, func(status int) bool { return status == 200 }); status != 200 {
			t.Errorf("the %s's /readyz after cobranch migrate = %d %q, want 200", name, status, body)
		}
	}
	if status, body := get(gitServer, "GET", "/acme/website.git/info/refs?service=git-upload-pack", ""); status != 401 || !strings.Contains(body, "personal token") {
		t.Errorf("a fetch without a credential = %d %q, want 401", status, body)
	}
	if status, _ := get(worker, "POST", "/v1/pushes", "{}"); status != 401 {
		t.Errorf("a push without the internal token = %d, want 401", status)
	}
}

// start runs workload's run until the test ends, as its subcommand would.
func start(t *testing.T, workload string, run func(ctx context.Context, logs io.Writer) error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- run(ctx, io.Discard) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("%s = %v after it was asked to stop", workload, err)
		}
	})
}
