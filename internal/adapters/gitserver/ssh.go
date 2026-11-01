package gitserver

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/crypto/ssh"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/git"
	"cobranch.dev/internal/services/repos"
)

var tracer = otel.Tracer("cobranch.dev/internal/adapters/gitserver")

// sshHandshakeTimeout is how long a connection may take to sign in.
const sshHandshakeTimeout = 30 * time.Second

// sshKeyExtension names, in a connection's permissions, the fingerprint of the key that signed it in.
const sshKeyExtension = "cobranch-key"

// ServeSSH serves Git over SSH on ln until ctx ends: clone, fetch and push at <org>/<repo>.git, signed in by an SSH key
// its user added, with any user name. It then lets the connections in flight finish for up to grace, and closes the
// rest.
func (s *Server) ServeSSH(ctx context.Context, ln net.Listener, hostKey ssh.Signer, grace time.Duration) error {
	defer func() { _ = ln.Close() }() // closed already once ctx ended
	conns, closeAll := context.WithCancel(context.WithoutCancel(ctx))
	defer closeAll()
	stop := context.AfterFunc(ctx, func() { _ = ln.Close() }) // Accept then fails, which ends the loop
	defer stop()
	var wg sync.WaitGroup
	err := s.acceptSSH(ctx, conns, ln, hostKey, &wg)
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(grace):
		closeAll()
		<-done
	}
	return err
}

// acceptSSH serves each connection ln accepts on a goroutine of wg's, with conns, until ctx ends or ln fails.
func (s *Server) acceptSSH(ctx, conns context.Context, ln net.Listener, hostKey ssh.Signer, wg *sync.WaitGroup) error {
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("accepting SSH connections: %w", err)
		}
		wg.Go(func() { s.serveSSHConn(conns, conn, hostKey) })
	}
}

// serveSSHConn signs a connection in by its SSH key and serves its sessions, until its client or ctx closes it.
func (s *Server) serveSSHConn(ctx context.Context, conn net.Conn, hostKey ssh.Signer) {
	defer func() { _ = conn.Close() }() // its client went away, or ctx closed it
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	cfg, signedIn := s.sshConfig(ctx, hostKey)
	_ = conn.SetDeadline(time.Now().Add(sshHandshakeTimeout)) // a connection that fails here fails its handshake
	server, channels, requests, err := ssh.NewServerConn(conn, cfg)
	if err != nil {
		return // a client that couldn't sign in, or went away: its own to tell
	}
	_ = conn.SetDeadline(time.Time{}) // the connection is the client's from here, idle or not
	// The key that signed the connection in is the one its permissions name: a client may offer others before it.
	viewer, ok := signedIn[server.Permissions.Extensions[sshKeyExtension]]
	if !ok {
		return
	}
	go ssh.DiscardRequests(requests)
	var sessions sync.WaitGroup
	defer sessions.Wait()
	for ch := range channels {
		if ch.ChannelType() != "session" {
			_ = ch.Reject(ssh.UnknownChannelType, "Cobranch serves Git's sessions alone") // the client is gone
			continue
		}
		channel, chRequests, err := ch.Accept()
		if err != nil {
			continue
		}
		sessions.Go(func() { s.serveSSHSession(ctx, viewer, channel, chRequests) })
	}
}

// sshConfig is a connection's SSH configuration: its host key, and the SSH keys its users added, each of which
// signedIn then maps, by its fingerprint, to who it acts as.
func (s *Server) sshConfig(ctx context.Context, hostKey ssh.Signer) (*ssh.ServerConfig, map[string]auth.Authenticated) {
	signedIn := map[string]auth.Authenticated{}
	cfg := &ssh.ServerConfig{
		ServerVersion: "SSH-2.0-Cobranch",
		PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			a, err := s.auth.AuthenticateSSHKey(ctx, key.Marshal())
			if err != nil {
				if !errors.Is(err, auth.ErrUnauthenticated) {
					slog.ErrorContext(ctx, "Checking a Git client's SSH key failed", "error", err)
				}
				return nil, fmt.Errorf("authenticating the SSH key: %w", err)
			}
			fingerprint := ssh.FingerprintSHA256(key)
			signedIn[fingerprint] = a
			return &ssh.Permissions{Extensions: map[string]string{sshKeyExtension: fingerprint}}, nil
		},
	}
	cfg.AddHostKey(hostKey)
	return cfg, signedIn
}

// serveSSHSession serves one session: its variable GIT_PROTOCOL, then one Git command, or a greeting for a shell. It
// refuses whatever else its client asks.
func (s *Server) serveSSHSession(ctx context.Context, viewer auth.Authenticated, ch ssh.Channel, requests <-chan *ssh.Request) {
	defer func() { _ = ch.Close() }() // the client is gone, or has its exit status
	protocol := ""
	for req := range requests {
		var status uint32
		switch req.Type {
		case "env":
			var env struct{ Name, Value string }
			ok := ssh.Unmarshal(req.Payload, &env) == nil && env.Name == "GIT_PROTOCOL" && gitProtocol.MatchString(env.Value)
			if ok {
				protocol = env.Value
			}
			_ = req.Reply(ok, nil) // a client gone away needs no answer
			continue
		case "exec":
			_ = req.Reply(true, nil) // as above
			var command struct{ Command string }
			if err := ssh.Unmarshal(req.Payload, &command); err != nil {
				status = tell(ch, "Cobranch runs a Git command, such as git-upload-pack 'acme/website.git'")
			} else {
				status = s.runSSHCommand(ctx, viewer, ch, protocol, command.Command)
			}
		case "shell":
			_ = req.Reply(true, nil) // as above
			status = tell(ch, "Hi "+viewer.User.DisplayName+"! You've signed in, but Cobranch serves Git over SSH without a shell.")
		default:
			_ = req.Reply(false, nil) // as above
			continue
		}
		go ssh.DiscardRequests(requests)
		_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{status})) // as above
		return
	}
}

// tell writes message to a session's standard error, as Git shows a remote's, and returns the exit status 1.
func tell(ch ssh.Channel, message string) uint32 {
	_, _ = io.WriteString(ch.Stderr(), message+"\n") // a client gone away reads nothing
	return 1
}

// runSSHCommand runs a Git client's command, git-upload-pack or git-receive-pack '<org>/<repo>.git', for viewer, and
// returns its exit status.
func (s *Server) runSSHCommand(ctx context.Context, viewer auth.Authenticated, ch ssh.Channel, protocol, command string) uint32 {
	service, orgSlug, slug, ok := sshCommand(command)
	if !ok {
		return tell(ch, "Cobranch serves git-upload-pack and git-receive-pack for '<organization>/<repository>.git' over SSH")
	}
	ctx, span := tracer.Start(auth.WithViewer(ctx, viewer), "gitserver.ssh "+service, trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(attribute.String("enduser.id", viewer.User.ID.String())))
	defer span.End()
	repo, held, err := s.repos.Locate(ctx, viewer.User, orgSlug, slug)
	if errors.Is(err, repos.ErrNotFound) {
		return tell(ch, "Repository not found")
	}
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		slog.ErrorContext(ctx, "Finding a repository for Git failed", "error", err)
		return tell(ch, "The repository could not be found")
	}
	span.SetAttributes(attribute.String("cobranch.repository.id", repo.ID.String()))
	if service == receivePack && !slices.Contains(held, repos.PermPush) {
		return tell(ch, "You may read this repository, but not push to it")
	}
	dir, remove, err := s.snapshotWorkspace(ctx, repo.ID)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		slog.ErrorContext(ctx, "Building a repository failed", "repository", repo.ID, "error", err)
		return tell(ch, "The repository could not be read")
	}
	defer remove()
	if service == uploadPack {
		var env []string
		if protocol != "" {
			env = []string{"GIT_PROTOCOL=" + protocol}
		}
		return s.runSSH(ctx, ch, ch, env, "upload-pack", dir)
	}
	return s.receiveSSH(ctx, ch, viewer, repo, slices.Contains(held, repos.PermBypass), dir)
}

// receiveSSH serves a push over SSH as receivePack does over HTTPS: the hook records it before Git answers.
func (s *Server) receiveSSH(ctx context.Context, ch ssh.Channel, viewer auth.Authenticated, repo repos.Repository, bypass bool, dir string) uint32 {
	id, err := uuid.NewV7()
	if err != nil {
		slog.ErrorContext(ctx, "Making a push's ID failed", "error", err)
		return tell(ch, "The push could not start")
	}
	token := s.expect(&pending{ctx: ctx, id: id, repo: repo, pusher: viewer.User.ID, bypass: bypass, dir: dir, received: time.Now(),
		transport: git.TransportSSH})
	defer s.forget(token)
	return s.runSSH(ctx, io.LimitReader(ch, s.cfg.MaxPushSize+pushOverhead), ch, []string{HookURLEnv + "=" + s.hookURL, HookTokenEnv + "=" + token},
		"receive-pack", dir)
}

// runSSH runs the git CLI's args with stdin, streaming its output to ch, and returns 0, or 1 when it fails. A failure
// is logged: the client sees only Git's protocol.
func (s *Server) runSSH(ctx context.Context, stdin io.Reader, ch ssh.Channel, env []string, args ...string) uint32 {
	var stderr bytes.Buffer
	cmd := s.git(ctx, env, args...)
	cmd.Stdout, cmd.Stderr = ch, &limited{buf: &stderr, n: 4096}
	in, err := cmd.StdinPipe()
	if err == nil {
		err = cmd.Start()
	}
	if err == nil {
		// A client holds its side open until the exit status comes, so the copy ends with the session, never before Wait.
		go func() { _, _ = io.Copy(in, stdin); _ = in.Close() }() // git exited, or the client went away
		err = cmd.Wait()
	}
	if err == nil {
		return 0
	}
	if ctx.Err() == nil {
		slog.ErrorContext(ctx, "Running git failed", "command", args[0], "error", err, "stderr", strings.TrimSpace(stderr.String()))
	}
	return 1
}

// sshCommand reads the command a Git client runs over SSH, such as git-upload-pack 'acme/website.git': its service,
// and the repository's organization and slug. Git puts the path in single quotes, and an ssh:// URL's starts with a
// slash; a slug has no quote.
func sshCommand(command string) (service, orgSlug, slug string, ok bool) {
	service, arg, _ := strings.Cut(command, " ")
	if service != uploadPack && service != receivePack {
		return "", "", "", false
	}
	path, quoted := strings.CutPrefix(arg, "'")
	path, closed := strings.CutSuffix(path, "'")
	if !quoted || !closed || strings.ContainsAny(path, `'\`) {
		return "", "", "", false
	}
	orgSlug, slug, ok = strings.Cut(strings.TrimPrefix(path, "/"), "/")
	slug = strings.TrimSuffix(slug, ".git")
	if !ok || orgSlug == "" || slug == "" || strings.Contains(slug, "/") {
		return "", "", "", false
	}
	return service, orgSlug, slug, true
}
