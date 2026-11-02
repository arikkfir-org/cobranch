package gitserver_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/ssh"

	"cobranch.dev/internal/adapters/postgres"
	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/auth/authtest"
)

// sshClientEnv names the directory of the SSH client the test binary runs as: its key, id, and the server's host
// key, host.pub.
const sshClientEnv = "COBRANCH_TEST_SSH_CLIENT"

// runSSHClient is Git's SSH client, run as OpenSSH's ssh would be: [-o SendEnv=GIT_PROTOCOL] [-p port] user@host
// command. It signs in with dir's key, checks the server's host key, and returns the command's exit status.
func runSSHClient(dir string, args []string) int {
	addr, user, command, sendEnv, err := sshArgs(args)
	if err == nil {
		err = runSSHCommand(dir, addr, user, command, sendEnv)
	}
	var exit *ssh.ExitError
	if errors.As(err, &exit) {
		return exit.ExitStatus()
	}
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "ssh:", err) // Git shows it, or nobody does
		return 255
	}
	return 0
}

// sshArgs reads the arguments Git gives its SSH client.
func sshArgs(args []string) (addr, user, command string, sendEnv bool, err error) {
	port := "22"
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-o":
			i++
			sendEnv = sendEnv || (i < len(args) && args[i] == "SendEnv=GIT_PROTOCOL")
		case "-p":
			i++
			if i < len(args) {
				port = args[i]
			}
		case "-4", "-6":
		default:
			user, host, _ := strings.Cut(args[i], "@")
			return net.JoinHostPort(host, port), user, strings.Join(args[i+1:], " "), sendEnv, nil
		}
	}
	return "", "", "", false, fmt.Errorf("no host in %q", args)
}

// runSSHCommand runs command at addr as user, signed in with dir's key, with its standard streams.
func runSSHCommand(dir, addr, user, command string, sendEnv bool) error {
	signer, hostKey, err := readSSHClient(dir)
	if err != nil {
		return err
	}
	client, err := ssh.Dial("tcp", addr, &ssh.ClientConfig{User: user, Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.FixedHostKey(hostKey), Timeout: 10 * time.Second})
	if err != nil {
		return fmt.Errorf("connecting: %w", err)
	}
	defer func() { _ = client.Close() }() // the command ended
	session, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("opening a session: %w", err)
	}
	if protocol := os.Getenv("GIT_PROTOCOL"); sendEnv && protocol != "" {
		if err := session.Setenv("GIT_PROTOCOL", protocol); err != nil {
			return fmt.Errorf("sending GIT_PROTOCOL: %w", err)
		}
	}
	session.Stdin, session.Stdout, session.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := session.Run(command); err != nil {
		return fmt.Errorf("running %s: %w", command, err)
	}
	return nil
}

// readSSHClient reads dir's key and the server's host key.
func readSSHClient(dir string) (ssh.Signer, ssh.PublicKey, error) {
	private, err := os.ReadFile(filepath.Join(dir, "id"))
	if err != nil {
		return nil, nil, fmt.Errorf("reading the key: %w", err)
	}
	signer, err := ssh.ParsePrivateKey(private)
	if err != nil {
		return nil, nil, fmt.Errorf("parsing the key: %w", err)
	}
	host, err := os.ReadFile(filepath.Join(dir, "host.pub"))
	if err != nil {
		return nil, nil, fmt.Errorf("reading the host key: %w", err)
	}
	hostKey, _, _, _, err := ssh.ParseAuthorizedKey(host)
	if err != nil {
		return nil, nil, fmt.Errorf("parsing the host key: %w", err)
	}
	return signer, hostKey, nil
}

// serveSSH serves Git over SSH from a Git server with its own cache in dataDir, until the test ends, and returns its
// URL and host key.
func (s *stack) serveSSH(dataDir string) (string, ssh.PublicKey) {
	s.t.Helper()
	hostKey := authtest.NewSSHKey(s.t)
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		s.t.Fatal(err)
	}
	srv := s.server(dataDir)
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- srv.ServeSSH(ctx, ln, hostKey, time.Second) }()
	s.t.Cleanup(func() {
		cancel()
		if err := <-served; err != nil {
			s.t.Errorf("ServeSSH() = %v, want nil once its context ended", err)
		}
	})
	return "ssh://git@" + ln.Addr().String(), hostKey.PublicKey()
}

// sshClient is a directory runSSHClient signs in from: a new key, which user adds unless user is uuid.Nil, and the
// server's host key.
func (s *stack) sshClient(user uuid.UUID, hostKey ssh.PublicKey) string {
	s.t.Helper()
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		s.t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(private, "")
	if err != nil {
		s.t.Fatal(err)
	}
	dir := s.t.TempDir()
	for name, content := range map[string][]byte{"id": pem.EncodeToMemory(block), "host.pub": ssh.MarshalAuthorizedKey(hostKey)} {
		if err := os.WriteFile(filepath.Join(dir, name), content, 0o600); err != nil {
			s.t.Fatal(err)
		}
	}
	if user == uuid.Nil {
		return dir
	}
	signer, err := ssh.NewSignerFromKey(private)
	if err != nil {
		s.t.Fatal(err)
	}
	key, hash, now := signer.PublicKey(), sha256.Sum256(signer.PublicKey().Marshal()), time.Now()
	if err := postgres.NewAuthStore(s.pool).AddSSHKey(context.Background(), auth.Credential{ID: uuid.Must(uuid.NewV7()), Kind: auth.CredentialSSHKey,
		PrincipalID: user, Hint: ssh.FingerprintSHA256(key), CreatedAt: now, CreatedBy: user, Name: "test", PublicKey: authtest.AuthorizedKey(key, "")}, hash[:]); err != nil {
		s.t.Fatal(err)
	}
	return dir
}

// sshGit runs gitCmd with client's key as Git's SSH client.
func sshGit(t *testing.T, client, dir string, args ...string) (string, error) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return gitCmd(t, dir, append([]string{"-c", "core.sshCommand=env " + sshClientEnv + "=" + client + " " + self, "-c", "ssh.variant=ssh"}, args...)...)
}

// sshClone clones url with client's key into a new directory, and returns it with git's output and error.
func sshClone(t *testing.T, client, url string) (string, string, error) {
	t.Helper()
	parent := t.TempDir()
	out, err := sshGit(t, client, parent, "clone", "-q", url, "repo")
	return filepath.Join(parent, "repo"), out, err
}

// TestSSH pushes main over SSH as Acme website's maintainer, then has keys clone it and push to it: its reader's, for
// it and a repository she can't read, and a key nobody added.
func TestSSH(t *testing.T) {
	t.Parallel()
	s := newStack(t)
	base, hostKey := s.serveSSH(t.TempDir())
	head := s.pushOverSSH(base, s.sshClient(olivia, hostKey))
	reader := s.sshClient(gwen, hostKey)
	tests := []struct {
		name    string
		client  string
		path    string
		push    bool
		wantErr string
	}{
		{name: "a reader clones", client: reader, path: "/acme/website.git"},
		{name: "a reader clones without .git", client: reader, path: "/acme/website"},
		{name: "a reader pushes", client: reader, path: "/acme/website.git", push: true, wantErr: "You may read this repository, but not push to it"},
		{name: "a repository the key's user can't read", client: reader, path: "/globex/ledger.git", wantErr: "Repository not found"},
		{name: "a key nobody added", client: s.sshClient(uuid.Nil, hostKey), path: "/acme/website.git", wantErr: "unable to authenticate"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir, out, err := sshClone(t, tt.client, base+tt.path)
			if err == nil && tt.push {
				commit(t, dir, "draft.md", "# Draft\n")
				out, err = sshGit(t, tt.client, dir, "push", "-q", "origin", "HEAD:refs/heads/main")
			}
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(out, tt.wantErr) {
					t.Errorf("git = %v\n%s\nwant it to fail with %q", err, out, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("git clone: %v\n%s", err, out)
			}
			if got := strings.TrimSpace(mustGit(t, dir, "rev-parse", "HEAD")); got != head {
				t.Errorf("the clone is at %s, want %s", got, head)
			}
		})
	}
	t.Run("a shell", func(t *testing.T) { checkShell(t, base, reader) })
}

// pushOverSSH clones Acme's website from base with client's key, pushes a commit to main, checks the push was
// recorded as over SSH, and returns main's head.
func (s *stack) pushOverSSH(base, client string) string {
	s.t.Helper()
	dir, out, err := sshClone(s.t, client, base+"/acme/website.git")
	if err != nil {
		s.t.Fatalf("git clone: %v\n%s", err, out)
	}
	mustGit(s.t, dir, "config", "user.useConfigOnly", "false")
	head := commit(s.t, dir, "README.md", "# Website\n")
	if out, err := sshGit(s.t, client, dir, "push", "-q", "origin", "HEAD:refs/heads/main"); err != nil {
		s.t.Fatalf("git push: %v\n%s", err, out)
	}
	if got := query[string](s.t, s.pool, "SELECT sha FROM refs WHERE repo_id = $1 AND name = 'refs/heads/main'", website); got != head {
		s.t.Errorf("main = %s, want %s", got, head)
	}
	if got := query[string](s.t, s.pool, "SELECT transport FROM pushes WHERE repo_id = $1", website); got != "ssh" {
		s.t.Errorf("the push came over %s, want ssh", got)
	}
	return head
}

// checkShell checks that a shell over SSH, as any user name, greets the key's user and ends.
func checkShell(t *testing.T, base, client string) {
	t.Helper()
	signer, hostKey, err := readSSHClient(client)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := ssh.Dial("tcp", strings.TrimPrefix(base, "ssh://git@"), &ssh.ClientConfig{User: "anyone", Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.FixedHostKey(hostKey)})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	session, err := conn.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	session.Stderr = &stderr
	if err := session.Shell(); err != nil {
		t.Fatal(err)
	}
	var exit *ssh.ExitError
	if err := session.Wait(); !errors.As(err, &exit) || exit.ExitStatus() != 1 || !strings.Contains(stderr.String(), "Cobranch serves Git over SSH without a shell") {
		t.Errorf("the shell = %v, %q, want exit status 1 and a greeting", err, stderr.String())
	}
}
