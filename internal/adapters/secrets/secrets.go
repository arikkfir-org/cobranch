// Package secrets seals the secrets Cobranch keeps in its database with the installation's secrets key: auth.Sealer
// on AES-256-GCM, each secret bound to what it belongs to, so a sealed value copied to another row doesn't open
// (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/back-office.md, "Identity providers").
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"

	"cobranch.dev/internal/services/auth"
)

// MinKeyLength is the fewest characters a secrets key has.
const MinKeyLength = 32

// format is the first byte of every sealed value: the key's derivation and the cipher, so either can change later.
const format = 1

// ErrUnsealable is a sealed value this Box can't open: another key's, another row's, or changed.
var ErrUnsealable = errors.New("the sealed value doesn't open with this key")

// Box seals and opens secrets with one secrets key.
type Box struct {
	aead cipher.AEAD
}

var _ auth.Sealer = (*Box)(nil)

// NewBox returns the Box of key, the installation's secrets key: any text of MinKeyLength characters or more, from
// which it derives the cipher's key (HKDF-SHA256).
func NewBox(key string) (*Box, error) {
	if len(key) < MinKeyLength {
		return nil, fmt.Errorf("the secrets key has %d characters; it needs %d at least", len(key), MinKeyLength)
	}
	derived, err := hkdf.Key(sha256.New, []byte(key), nil, "cobranch secrets", 32)
	if err != nil {
		return nil, fmt.Errorf("deriving the cipher's key: %w", err)
	}
	block, err := aes.NewCipher(derived)
	if err != nil {
		return nil, fmt.Errorf("making the cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("making the cipher: %w", err)
	}
	return &Box{aead: aead}, nil
}

// Seal encrypts plain for about: the format, a random nonce, then the ciphertext and its tag.
func (b *Box) Seal(plain []byte, about string) ([]byte, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("making a nonce: %w", err)
	}
	return b.aead.Seal(append([]byte{format}, nonce...), nonce, plain, []byte(about)), nil
}

// Open decrypts what Seal sealed for about, or answers ErrUnsealable.
func (b *Box) Open(sealed []byte, about string) ([]byte, error) {
	n := b.aead.NonceSize()
	if len(sealed) < 1+n+b.aead.Overhead() || sealed[0] != format {
		return nil, ErrUnsealable
	}
	plain, err := b.aead.Open(nil, sealed[1:1+n], sealed[1+n:], []byte(about))
	if err != nil {
		return nil, ErrUnsealable
	}
	return plain, nil
}
