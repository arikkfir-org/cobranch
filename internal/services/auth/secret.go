package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"hash/crc32"
	"math/big"
	"strings"
)

// Prefixes of Cobranch's secrets, so a scanner knows one on sight.
const (
	// PrefixSession starts a session's token, which the cookie carries.
	PrefixSession = "cbs_"
	// PrefixAPIKey starts an API key's secret.
	PrefixAPIKey = "cbk_"
	// PrefixPersonalToken starts a personal token.
	PrefixPersonalToken = "cbp_"
)

const (
	// base62 is big.Int's alphabet for base 62.
	base62   = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	bodyLen  = 43 // 32 random bytes in base62
	checkLen = 6  // the CRC32 of the prefix and body, in base62
)

// NewSecret makes a secret of prefix: the prefix, 32 random bytes in base62 and a CRC32 checksum of both, as GitHub's
// tokens, so a scanner spots a leaked one and checks it offline.
func NewSecret(prefix string) string {
	var b [32]byte
	_, _ = rand.Read(b[:]) // never fails, since Go 1.24
	var n big.Int
	n.SetBytes(b[:])
	body := leftPad(n.Text(62), bodyLen)
	return prefix + body + checksum(prefix+body)
}

// ValidSecret reports whether s has prefix's shape and its checksum holds: a cheap check before the database is asked.
func ValidSecret(prefix, s string) bool {
	if !strings.HasPrefix(s, prefix) || len(s) != len(prefix)+bodyLen+checkLen {
		return false
	}
	for _, c := range s[len(prefix):] {
		if !strings.ContainsRune(base62, c) {
			return false
		}
	}
	return checksum(s[:len(s)-checkLen]) == s[len(s)-checkLen:]
}

// Hash is what the database keeps of a secret: its SHA-256, so a copy of the database signs nobody in. A plain,
// fast hash is the right one here, not a password hash: a secret is 32 random bytes from NewSecret, which no
// dictionary or brute force reaches, and the database finds a credential by its hash on every request, which a
// salted or slow hash would make impossible. Code scanners that take any hashed secret for a password flag this
// line; that finding is a false positive, dismissed as such.
func Hash(secret string) []byte {
	sum := sha256.Sum256([]byte(secret))
	return sum[:]
}

// Hint is the last four characters of a secret, which the Console shows.
func Hint(secret string) string {
	return secret[len(secret)-4:]
}

func checksum(s string) string {
	var n big.Int
	n.SetUint64(uint64(crc32.ChecksumIEEE([]byte(s))))
	return leftPad(n.Text(62), checkLen)
}

// leftPad pads s with zeros to n characters; big.Int's text drops leading zeros.
func leftPad(s string, n int) string {
	return strings.Repeat("0", n-len(s)) + s
}
