package auth_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/ssh"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/auth/authtest"
)

// newSSHKey is a new Ed25519 key's public half.
func newSSHKey(t *testing.T) ssh.PublicKey {
	t.Helper()
	return authtest.NewSSHKey(t).PublicKey()
}

// rsaSSHKey is a new RSA key of bits' public half.
func rsaSSHKey(t *testing.T, bits int) ssh.PublicKey {
	t.Helper()
	private, err := rsa.GenerateKey(rand.Reader, bits)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ssh.NewPublicKey(&private.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

// certificateOf is a user certificate of key, signed by a new key.
func certificateOf(t *testing.T, key ssh.PublicKey) ssh.PublicKey {
	t.Helper()
	_, ca, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(ca)
	if err != nil {
		t.Fatal(err)
	}
	cert := &ssh.Certificate{Key: key, CertType: ssh.UserCert, ValidBefore: ssh.CertTimeInfinity}
	if err := cert.SignCert(rand.Reader, signer); err != nil {
		t.Fatal(err)
	}
	return cert
}

func TestValidateSSHKey(t *testing.T) {
	key, rsaKey := newSSHKey(t), rsaSSHKey(t, 2048)
	bare := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key)))
	tests := []struct {
		name     string
		in       auth.SSHKeyInput
		want     ssh.PublicKey
		wantName string
		wantCode string
	}{
		{name: "named by its comment", in: auth.SSHKeyInput{PublicKey: " " + authtest.AuthorizedKey(key, "alice@laptop") + "\n"}, want: key, wantName: "alice@laptop"},
		{name: "named by its user", in: auth.SSHKeyInput{Name: " Laptop ", PublicKey: authtest.AuthorizedKey(key, "alice@laptop")}, want: key, wantName: "Laptop"},
		{name: "an RSA key of 2048 bits", in: auth.SSHKeyInput{Name: "Old laptop", PublicKey: authtest.AuthorizedKey(rsaKey, "")}, want: rsaKey, wantName: "Old laptop"},
		{name: "an RSA key of 1024 bits", in: auth.SSHKeyInput{Name: "Old", PublicKey: authtest.AuthorizedKey(rsaSSHKey(t, 1024), "")}, wantCode: "ssh_key.type_refused"},
		{name: "a certificate", in: auth.SSHKeyInput{Name: "Cert", PublicKey: authtest.AuthorizedKey(certificateOf(t, key), "")}, wantCode: "ssh_key.type_refused"},
		{name: "a key with options", in: auth.SSHKeyInput{Name: "Laptop", PublicKey: `command="true" ` + bare}, wantCode: "ssh_key.invalid"},
		{name: "two keys", in: auth.SSHKeyInput{Name: "Laptop", PublicKey: bare + "\n" + bare}, wantCode: "ssh_key.invalid"},
		{name: "a private key", in: auth.SSHKeyInput{Name: "Laptop", PublicKey: "-----BEGIN OPENSSH PRIVATE KEY-----"}, wantCode: "ssh_key.invalid"},
		{name: "nothing", in: auth.SSHKeyInput{Name: "Laptop", PublicKey: "  "}, wantCode: "ssh_key.invalid"},
		{name: "no name and no comment", in: auth.SSHKeyInput{PublicKey: bare}, wantCode: "ssh_key.name_invalid"},
		{name: "a long name", in: auth.SSHKeyInput{Name: strings.Repeat("n", 101), PublicKey: bare}, wantCode: "ssh_key.name_invalid"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := auth.ValidateSSHKey(tt.in)
			var verr *auth.ValidationError
			if tt.wantCode != "" {
				if !errors.As(err, &verr) || verr.Code != tt.wantCode {
					t.Fatalf("ValidateSSHKey() error = %v, want code %s", err, tt.wantCode)
				}
				return
			}
			wantKey := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(tt.want)))
			if err != nil || got.Name != tt.wantName || got.PublicKey != wantKey || got.Fingerprint != ssh.FingerprintSHA256(tt.want) || len(got.Hash) != 32 {
				t.Errorf("ValidateSSHKey() = %+v, %v, want %s named %q", got, err, wantKey, tt.wantName)
			}
		})
	}
}

// addSSHKeyCase adds a key as Alice, as the case has her.
type addSSHKeyCase struct {
	name     string
	as       func(a auth.Authenticated) auth.Authenticated
	in       func(t *testing.T) auth.SSHKeyInput
	before   bool
	fail     bool
	wantErr  error
	wantCode string
}

func TestAddSSHKey(t *testing.T) {
	apiKey := auth.User{Principal: auth.Principal{ID: uuid.New(), Kind: auth.KindAPIKey, DisplayName: "Deploy"}}
	same := newSSHKey(t)
	tests := []addSSHKeyCase{
		{name: "from a session", as: func(a auth.Authenticated) auth.Authenticated { return a }},
		{name: "from a personal token", as: func(a auth.Authenticated) auth.Authenticated {
			return auth.Authenticated{User: a.User, Credential: auth.Credential{ID: uuid.New(), Kind: auth.CredentialPersonalToken}}
		}, wantErr: auth.ErrForbidden},
		{name: "from an API key", as: func(auth.Authenticated) auth.Authenticated {
			return auth.Authenticated{User: apiKey, Credential: auth.Credential{ID: uuid.New(), Kind: auth.CredentialAPIKey}}
		}, wantErr: auth.ErrForbidden},
		{name: "a key that isn't one", as: func(a auth.Authenticated) auth.Authenticated { return a },
			in: func(*testing.T) auth.SSHKeyInput {
				return auth.SSHKeyInput{Name: "Laptop", PublicKey: "ssh-ed25519 nope"}
			}, wantCode: "ssh_key.invalid"},
		{name: "a key added already", as: func(a auth.Authenticated) auth.Authenticated { return a },
			in: func(*testing.T) auth.SSHKeyInput {
				return auth.SSHKeyInput{Name: "Laptop", PublicKey: authtest.AuthorizedKey(same, "")}
			}, before: true, wantCode: "ssh_key.taken"},
		{name: "a store that fails", as: func(a auth.Authenticated) auth.Authenticated { return a }, fail: true, wantErr: errDown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { runAddSSHKey(t, tt) })
	}
}

// runAddSSHKey runs one case of TestAddSSHKey: by default, a new key named by its comment.
func runAddSSHKey(t *testing.T, tt addSSHKeyCase) {
	t.Helper()
	s, store, a := signedInAs(t)
	in := auth.SSHKeyInput{PublicKey: authtest.AuthorizedKey(newSSHKey(t), "alice@laptop")}
	if tt.in != nil {
		in = tt.in(t)
	}
	if tt.before {
		if _, err := s.AddSSHKey(context.Background(), a, in); err != nil {
			t.Fatal(err)
		}
	}
	if tt.fail {
		store.Fail = errDown
	}
	key, err := s.AddSSHKey(context.Background(), tt.as(a), in)
	var verr *auth.ValidationError
	if tt.wantCode != "" {
		if !errors.As(err, &verr) || verr.Code != tt.wantCode {
			t.Fatalf("AddSSHKey() error = %v, want code %s", err, tt.wantCode)
		}
		return
	}
	if !errors.Is(err, tt.wantErr) {
		t.Fatalf("AddSSHKey() error = %v, want %v", err, tt.wantErr)
	}
	if err != nil {
		return
	}
	if key.Kind != auth.CredentialSSHKey || key.PrincipalID != a.User.ID || key.CreatedBy != a.User.ID || key.Name != "alice@laptop" ||
		!strings.HasPrefix(key.Hint, "SHA256:") || !key.ExpiresAt.IsZero() || key.Permissions != nil {
		t.Errorf("AddSSHKey() = %+v, want Alice's key, named by its comment, that never ends", key)
	}
}

// authenticateSSHKeyCase signs in with Alice's key, which change may change first, or with a key nobody added.
type authenticateSSHKeyCase struct {
	name      string
	change    func(c *auth.Credential, store *authtest.Store, a auth.Authenticated)
	other     bool
	wantErr   error
	wantTouch bool
}

func TestAuthenticateSSHKey(t *testing.T) {
	tests := []authenticateSSHKeyCase{
		{name: "a key its user added", wantTouch: true},
		{name: "a key used lately", change: func(c *auth.Credential, _ *authtest.Store, _ auth.Authenticated) {
			c.LastUsedAt = now.Add(time.Hour - time.Second)
		}},
		{name: "a key nobody added", other: true, wantErr: auth.ErrUnauthenticated},
		{name: "a suspended user", change: func(_ *auth.Credential, store *authtest.Store, a auth.Authenticated) {
			u := store.Users[a.User.ID]
			u.Suspended = true
			store.Users[a.User.ID] = u
		}, wantErr: auth.ErrUnauthenticated},
		{name: "a personal token's hash", change: func(c *auth.Credential, _ *authtest.Store, _ auth.Authenticated) {
			c.Kind = auth.CredentialPersonalToken
		},
			wantErr: auth.ErrUnauthenticated},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { runAuthenticateSSHKey(t, tt) })
	}
}

// runAuthenticateSSHKey runs one case of TestAuthenticateSSHKey, an hour after the key was added.
func runAuthenticateSSHKey(t *testing.T, tt authenticateSSHKeyCase) {
	t.Helper()
	s, store, a := signedInAs(t)
	ctx := context.Background()
	key := newSSHKey(t)
	added, err := s.AddSSHKey(ctx, a, auth.SSHKeyInput{Name: "Laptop", PublicKey: authtest.AuthorizedKey(key, "")})
	if err != nil {
		t.Fatal(err)
	}
	if tt.change != nil {
		for hash, c := range store.Credentials {
			if c.ID == added.ID {
				tt.change(&c, store, a)
				store.Credentials[hash] = c
			}
		}
	}
	if tt.other {
		key = newSSHKey(t)
	}
	at := now.Add(time.Hour)
	s.SetClock(func() time.Time { return at })
	got, err := s.AuthenticateSSHKey(ctx, key.Marshal())
	if !errors.Is(err, tt.wantErr) {
		t.Fatalf("AuthenticateSSHKey() error = %v, want %v", err, tt.wantErr)
	}
	if err != nil {
		return
	}
	if got.User.ID != a.User.ID || got.Credential.ID != added.ID || got.Session.ID != uuid.Nil {
		t.Errorf("AuthenticateSSHKey() = %+v, want Alice under her key", got)
	}
	if touched := got.Credential.LastUsedAt.Equal(at); touched != tt.wantTouch {
		t.Errorf("last used %v at %v, want touched %t", got.Credential.LastUsedAt, at, tt.wantTouch)
	}
}

func TestSSHKeysAndDeleteSSHKey(t *testing.T) {
	s, store, a := signedInAs(t)
	ctx := context.Background()
	older, err := s.AddSSHKey(ctx, a, auth.SSHKeyInput{Name: "Laptop", PublicKey: authtest.AuthorizedKey(newSSHKey(t), "")})
	if err != nil {
		t.Fatal(err)
	}
	s.SetClock(func() time.Time { return now.Add(time.Minute) })
	again := newSSHKey(t)
	newer, err := s.AddSSHKey(ctx, a, auth.SSHKeyInput{Name: "Desktop", PublicKey: authtest.AuthorizedKey(again, "")})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.SSHKeys(ctx, a.User.ID); err != nil || len(got) != 2 || got[0].ID != newer.ID || got[1].ID != older.ID {
		t.Fatalf("SSHKeys() = %+v, %v, want the newer first", got, err)
	}
	if err := s.DeleteSSHKey(ctx, a, newer.ID); err != nil {
		t.Fatalf("DeleteSSHKey() = %v", err)
	}
	if _, err := s.AuthenticateSSHKey(ctx, again.Marshal()); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Errorf("AuthenticateSSHKey() of a deleted key = %v, want auth.ErrUnauthenticated", err)
	}
	// A deleted key may be added again.
	if _, err := s.AddSSHKey(ctx, a, auth.SSHKeyInput{Name: "Desktop", PublicKey: authtest.AuthorizedKey(again, "")}); err != nil {
		t.Errorf("AddSSHKey() of a deleted key = %v", err)
	}
	store.Fail = errDown
	if _, err := s.SSHKeys(ctx, a.User.ID); err == nil {
		t.Error("SSHKeys() on a store that fails = nil, want its error")
	}
}

func TestDeleteSSHKey(t *testing.T) {
	apiKey := auth.User{Principal: auth.Principal{ID: uuid.New(), Kind: auth.KindAPIKey, DisplayName: "Deploy"}}
	tests := []struct {
		name    string
		as      func(a auth.Authenticated) auth.Authenticated
		fail    bool
		wantErr error
	}{
		{name: "from a session", as: func(a auth.Authenticated) auth.Authenticated { return a }},
		{name: "from a personal token", as: func(a auth.Authenticated) auth.Authenticated {
			return auth.Authenticated{User: a.User, Credential: auth.Credential{ID: uuid.New(), Kind: auth.CredentialPersonalToken, Permissions: []string{"repo.read"}}}
		}, wantErr: auth.ErrForbidden},
		{name: "from an API key", as: func(auth.Authenticated) auth.Authenticated {
			return auth.Authenticated{User: apiKey, Credential: auth.Credential{ID: uuid.New(), Kind: auth.CredentialAPIKey}}
		}, wantErr: auth.ErrForbidden},
		{name: "another user's key", as: func(a auth.Authenticated) auth.Authenticated {
			a.User.ID = uuid.New()
			return a
		}, wantErr: auth.ErrNotFound},
		{name: "a store that fails", as: func(a auth.Authenticated) auth.Authenticated { return a }, fail: true, wantErr: errDown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, store, a := signedInAs(t)
			ctx := context.Background()
			key := newSSHKey(t)
			added, err := s.AddSSHKey(ctx, a, auth.SSHKeyInput{Name: "Laptop", PublicKey: authtest.AuthorizedKey(key, "")})
			if err != nil {
				t.Fatal(err)
			}
			if tt.fail {
				store.Fail = errDown
			}
			if err := s.DeleteSSHKey(ctx, tt.as(a), added.ID); !errors.Is(err, tt.wantErr) {
				t.Fatalf("DeleteSSHKey() = %v, want %v", err, tt.wantErr)
			}
			store.Fail = nil
			_, err = s.AuthenticateSSHKey(ctx, key.Marshal())
			if deleted := errors.Is(err, auth.ErrUnauthenticated); deleted != (tt.wantErr == nil) {
				t.Errorf("AuthenticateSSHKey() after DeleteSSHKey() = %v, want the key deleted: %t", err, tt.wantErr == nil)
			}
		})
	}
}
