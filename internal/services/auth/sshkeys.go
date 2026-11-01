package auth

import (
	"context"
	"crypto/rsa"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"golang.org/x/crypto/ssh"
)

// minRSABits is the smallest RSA key an SSH key may be.
const minRSABits = 2048

// sshKeyTypes are the SSH keys a user may add: Ed25519, ECDSA and RSA, the last of minRSABits or more, and their
// security keys' kinds. DSA is gone from OpenSSH, and a certificate names no key of its own.
var sshKeyTypes = map[string]bool{
	ssh.KeyAlgoED25519: true, ssh.KeyAlgoECDSA256: true, ssh.KeyAlgoECDSA384: true, ssh.KeyAlgoECDSA521: true,
	ssh.KeyAlgoRSA: true, ssh.KeyAlgoSKED25519: true, ssh.KeyAlgoSKECDSA256: true,
}

// SSHKeyInput is what adds an SSH key: its name, and its public key as an authorized_keys line has it, such as
// ssh-ed25519 AAAA… laptop. An empty name takes the key's comment.
type SSHKeyInput struct {
	Name      string
	PublicKey string
}

// ParsedSSHKey is an SSH key that ValidateSSHKey accepted.
type ParsedSSHKey struct {
	Name string
	// PublicKey is the key's type and base64, without its comment.
	PublicKey string
	// Fingerprint is the key's SHA256 fingerprint, as ssh-keygen -l shows it.
	Fingerprint string
	// Hash is the SHA-256 of the key's wire form, by which a Git client's key finds it.
	Hash []byte
}

// ValidateSSHKey normalizes in, or refuses it: one public key of a type sshKeyTypes admits, with no options, and a name
// of 1 to 100 characters, its comment's when none is given.
func ValidateSSHKey(in SSHKeyInput) (ParsedSSHKey, error) {
	invalid := &ValidationError{Field: "publicKey", Code: "ssh_key.invalid", Message: "a public key is one line such as ssh-ed25519 AAAA… you@laptop"}
	line := strings.TrimSpace(in.PublicKey)
	if line == "" || strings.ContainsAny(line, "\r\n") {
		return ParsedSSHKey{}, invalid
	}
	key, comment, options, rest, err := ssh.ParseAuthorizedKey([]byte(line))
	if err != nil || len(options) > 0 || len(rest) > 0 {
		return ParsedSSHKey{}, invalid
	}
	if !sshKeyTypes[key.Type()] {
		return ParsedSSHKey{}, &ValidationError{Field: "publicKey", Code: "ssh_key.type_refused",
			Message: fmt.Sprintf("%s keys are refused: add an Ed25519, ECDSA or RSA key", key.Type())}
	}
	if key.Type() == ssh.KeyAlgoRSA {
		if bits, ok := rsaBits(key); !ok || bits < minRSABits {
			return ParsedSSHKey{}, &ValidationError{Field: "publicKey", Code: "ssh_key.type_refused", Message: fmt.Sprintf("an RSA key has %d bits or more", minRSABits)}
		}
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = strings.TrimSpace(comment)
	}
	if name == "" || len(name) > 100 {
		return ParsedSSHKey{}, &ValidationError{Field: "name", Code: "ssh_key.name_invalid", Message: "a name is 1 to 100 characters"}
	}
	hash := sha256.Sum256(key.Marshal())
	return ParsedSSHKey{Name: name, PublicKey: strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key))), Fingerprint: ssh.FingerprintSHA256(key),
		Hash: hash[:]}, nil
}

// rsaBits is an RSA key's size in bits, and whether key is one.
func rsaBits(key ssh.PublicKey) (int, bool) {
	crypto, ok := key.(ssh.CryptoPublicKey)
	if !ok {
		return 0, false
	}
	pub, ok := crypto.CryptoPublicKey().(*rsa.PublicKey)
	if !ok {
		return 0, false
	}
	return pub.N.BitLen(), true
}

// AddSSHKey adds an SSH key for the user a signs in, from a session only: never from a token or an API key's secret,
// so no narrowed credential makes an unnarrowed one. The key then acts as its user over SSH, as their session would,
// until its user deletes it.
func (s *Service) AddSSHKey(ctx context.Context, a Authenticated, in SSHKeyInput) (Credential, error) {
	if a.Session.ID == uuid.Nil || a.User.Kind != KindUser {
		return Credential{}, fmt.Errorf("%w: only a signed-in user adds an SSH key, never a token or a key", ErrForbidden)
	}
	parsed, err := ValidateSSHKey(in)
	if err != nil {
		return Credential{}, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return Credential{}, fmt.Errorf("making the key's ID: %w", err)
	}
	key := Credential{ID: id, Kind: CredentialSSHKey, PrincipalID: a.User.ID, Hint: parsed.Fingerprint, CreatedAt: s.now(), CreatedBy: a.User.ID,
		Name: parsed.Name, PublicKey: parsed.PublicKey}
	err = s.store.AddSSHKey(ctx, key, parsed.Hash)
	if errors.Is(err, ErrSSHKeyTaken) {
		return Credential{}, &ValidationError{Field: "publicKey", Code: "ssh_key.taken", Message: "this key is added already, to your account or another's"}
	}
	if err != nil {
		return Credential{}, fmt.Errorf("recording the key: %w", err)
	}
	return key, nil
}

// SSHKeys lists user userID's SSH keys, newest first.
func (s *Service) SSHKeys(ctx context.Context, userID uuid.UUID) ([]Credential, error) {
	keys, err := s.store.SSHKeys(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("listing the SSH keys: %w", err)
	}
	return keys, nil
}

// DeleteSSHKey deletes SSH key id of the user a signs in, or ErrNotFound; the next connection with it fails. Like
// adding one, only a session deletes one, never a token or an API key's secret, so a token narrowed to reading
// repositories can't take its user's SSH access away.
func (s *Service) DeleteSSHKey(ctx context.Context, a Authenticated, id uuid.UUID) error {
	if a.Session.ID == uuid.Nil || a.User.Kind != KindUser {
		return fmt.Errorf("%w: only a signed-in user deletes an SSH key, never a token or a key", ErrForbidden)
	}
	if err := s.store.DeleteSSHKey(ctx, a.User.ID, id); err != nil {
		return fmt.Errorf("deleting the SSH key: %w", err)
	}
	return nil
}

// AuthenticateSSHKey resolves an SSH public key, in its wire form, to the user who added it, unnarrowed:
// ErrUnauthenticated for a key nobody added, or whose user is suspended. A key is marked used, at most once a minute.
func (s *Service) AuthenticateSSHKey(ctx context.Context, wire []byte) (Authenticated, error) {
	hash := sha256.Sum256(wire)
	return s.authenticateCredential(ctx, hash[:], CredentialSSHKey)
}
