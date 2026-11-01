package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"cobranch.dev/internal/adapters/postgres/postgrestest"
)

// TestRunGitWorkloads runs a push worker and a Git server on one database, which keeps the packs too, and checks that
// both turn ready once it is migrated, that the Git server asks a client without a credential for one, over HTTPS and
// over SSH with its host key, and that the push worker takes no calls.
func TestRunGitWorkloads(t *testing.T) {
	worker, gitServer, sshAddr := freeAddr(t), freeAddr(t), freeAddr(t)
	hostKey := hostKeyPEM(t)
	t.Setenv("COBRANCH_DATABASE_URL", postgrestest.Database(t))
	t.Setenv("COBRANCH_GIT_DATA_DIR", t.TempDir())
	t.Setenv("COBRANCH_GIT_SSH_ADDR", sshAddr)
	t.Setenv("COBRANCH_GIT_SSH_HOST_KEY", string(pem.EncodeToMemory(hostKey.block)))
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
	if status, _ := get(worker, "POST", "/v1/pushes", "{}"); status != 404 {
		t.Errorf("a push sent to the push worker = %d, want 404: it serves only its health", status)
	}
	checkSSHRefuses(t, sshAddr, hostKey.public)
}

// hostKey is a new SSH host key, as a PEM block and its public half.
type hostKey struct {
	block  *pem.Block
	public ssh.PublicKey
}

// hostKeyPEM makes a new Ed25519 host key.
func hostKeyPEM(t *testing.T) hostKey {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(private, "")
	if err != nil {
		t.Fatal(err)
	}
	key, err := ssh.NewPublicKey(public)
	if err != nil {
		t.Fatal(err)
	}
	return hostKey{block: block, public: key}
}

// checkSSHRefuses checks that the SSH server at addr shows host key want, and refuses a key nobody added.
func checkSSHRefuses(t *testing.T, addr string, want ssh.PublicKey) {
	t.Helper()
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(private)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := ssh.Dial("tcp", addr, &ssh.ClientConfig{User: "git", Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)}, HostKeyCallback: ssh.FixedHostKey(want),
		Timeout: 10 * time.Second})
	if err == nil {
		_ = conn.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "unable to authenticate") {
		t.Errorf("an SSH key nobody added = %v, want the host key accepted and the key refused", err)
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

// TestWorkloadsStopWhenTheyCantServe runs workloads that can't serve all they should: each returns the error instead
// of serving the rest, the push worker without its health endpoints, the Git server without SSH.
func TestWorkloadsStopWhenTheyCantServe(t *testing.T) {
	held, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = held.Close() }()
	key := string(pem.EncodeToMemory(hostKeyPEM(t).block))
	database := postgrestest.Database(t)
	tests := []struct {
		name    string
		run     func(ctx context.Context, logs io.Writer) error
		env     map[string]string
		wantErr string
	}{
		{name: "a push worker on a held address", run: runPushWorker, env: map[string]string{"COBRANCH_HTTP_ADDR": held.Addr().String()},
			wantErr: "address already in use"},
		{name: "a Git server on a held SSH address", run: runGitServer,
			env:     map[string]string{"COBRANCH_HTTP_ADDR": freeAddr(t), "COBRANCH_GIT_SSH_ADDR": held.Addr().String(), "COBRANCH_GIT_SSH_HOST_KEY": key},
			wantErr: "listening for SSH"},
		{name: "a Git server with a host key that isn't one", run: runGitServer,
			env:     map[string]string{"COBRANCH_HTTP_ADDR": freeAddr(t), "COBRANCH_GIT_SSH_ADDR": freeAddr(t), "COBRANCH_GIT_SSH_HOST_KEY": "nope"},
			wantErr: "reading COBRANCH_GIT_SSH_HOST_KEY"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("COBRANCH_DATABASE_URL", database)
			t.Setenv("COBRANCH_BLOB_URL", "file://"+t.TempDir())
			t.Setenv("COBRANCH_GIT_DATA_DIR", t.TempDir())
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			checkStops(t, tt.run, tt.wantErr)
		})
	}
}

// checkStops checks that run returns an error that says want by itself, within 10 seconds.
func checkStops(t *testing.T, run func(ctx context.Context, logs io.Writer) error, want string) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- run(ctx, io.Discard) }()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("the workload = %v, want an error that says %q", err, want)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the workload didn't return")
	}
}
