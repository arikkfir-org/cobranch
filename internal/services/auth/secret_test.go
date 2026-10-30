package auth

import (
	"strings"
	"testing"
)

func TestNewSecret(t *testing.T) {
	tests := []struct {
		name   string
		prefix string
	}{
		{name: "a session token", prefix: PrefixSession},
		{name: "an API key", prefix: PrefixAPIKey},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			seen := map[string]bool{}
			for range 100 {
				s := NewSecret(tt.prefix)
				checkSecret(t, tt.prefix, s)
				if seen[s] {
					t.Fatalf("NewSecret(%q) = %q twice", tt.prefix, s)
				}
				seen[s] = true
			}
		})
	}
}

// checkSecret checks that s has prefix's shape, validates, hashes and hints.
func checkSecret(t *testing.T, prefix, s string) {
	t.Helper()
	if !strings.HasPrefix(s, prefix) || len(s) != len(prefix)+bodyLen+checkLen {
		t.Fatalf("NewSecret(%q) = %q: wrong shape", prefix, s)
	}
	if !ValidSecret(prefix, s) {
		t.Fatalf("ValidSecret(%q) = false for a new secret", s)
	}
	if len(Hash(s)) != 32 || Hint(s) != s[len(s)-4:] {
		t.Fatalf("Hash or Hint of %q is off", s)
	}
}

func TestValidSecret(t *testing.T) {
	valid := NewSecret(PrefixSession)
	tests := []struct {
		name   string
		prefix string
		secret string
		want   bool
	}{
		{name: "a new secret", prefix: PrefixSession, secret: valid, want: true},
		{name: "another prefix", prefix: PrefixAPIKey, secret: valid},
		{name: "a changed character", prefix: PrefixSession, secret: valid[:10] + flip(valid[10]) + valid[11:]},
		{name: "a changed checksum", prefix: PrefixSession, secret: valid[:len(valid)-1] + flip(valid[len(valid)-1])},
		{name: "too short", prefix: PrefixSession, secret: valid[:len(valid)-1]},
		{name: "too long", prefix: PrefixSession, secret: valid + "0"},
		{name: "a character outside base62", prefix: PrefixSession, secret: valid[:10] + "-" + valid[11:]},
		{name: "empty", prefix: PrefixSession, secret: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ValidSecret(tt.prefix, tt.secret); got != tt.want {
				t.Errorf("ValidSecret(%q, %q) = %t, want %t", tt.prefix, tt.secret, got, tt.want)
			}
		})
	}
}

// flip returns a base62 character other than c.
func flip(c byte) string {
	if c == '0' {
		return "1"
	}
	return "0"
}
