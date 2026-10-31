package secrets

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

const key = "a secrets key of at least 32 characters"

func TestNewBox(t *testing.T) {
	tests := []struct {
		name    string
		key     string
		wantErr bool
	}{
		{name: "32 characters", key: strings.Repeat("k", 32)},
		{name: "longer", key: key},
		{name: "too short", key: strings.Repeat("k", 31), wantErr: true},
		{name: "none", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := NewBox(tt.key); (err != nil) != tt.wantErr {
				t.Errorf("NewBox() error = %v, want an error: %t", err, tt.wantErr)
			}
		})
	}
}

func TestOpen(t *testing.T) {
	box, err := NewBox(key)
	if err != nil {
		t.Fatal(err)
	}
	other, err := NewBox(key + "!")
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := box.Seal([]byte("the client secret"), "identity_providers/google/client_secret")
	if err != nil {
		t.Fatal(err)
	}
	flipped := bytes.Clone(sealed)
	flipped[len(flipped)-1] ^= 1
	reformatted := bytes.Clone(sealed)
	reformatted[0] = 2
	tests := []struct {
		name   string
		box    *Box
		sealed []byte
		about  string
		want   string
	}{
		{name: "what was sealed", box: box, sealed: sealed, about: "identity_providers/google/client_secret", want: "the client secret"},
		{name: "another row's", box: box, sealed: sealed, about: "identity_providers/okta/client_secret"},
		{name: "another key", box: other, sealed: sealed, about: "identity_providers/google/client_secret"},
		{name: "changed", box: box, sealed: flipped, about: "identity_providers/google/client_secret"},
		{name: "another format", box: box, sealed: reformatted, about: "identity_providers/google/client_secret"},
		{name: "too short", box: box, sealed: sealed[:20], about: "identity_providers/google/client_secret"},
		{name: "nothing", box: box, about: "identity_providers/google/client_secret"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.box.Open(tt.sealed, tt.about)
			if tt.want == "" {
				if !errors.Is(err, ErrUnsealable) {
					t.Errorf("Open() = %q, %v; want ErrUnsealable", got, err)
				}
				return
			}
			if err != nil || string(got) != tt.want {
				t.Errorf("Open() = %q, %v; want %q", got, err, tt.want)
			}
		})
	}
}

func TestSealTwice(t *testing.T) {
	box, err := NewBox(key)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := box.Seal([]byte("secret"), "about")
	b, _ := box.Seal([]byte("secret"), "about")
	if bytes.Equal(a, b) || bytes.Contains(a, []byte("secret")) {
		t.Errorf("Seal() twice = %x and %x: want two random nonces, never the plain text", a, b)
	}
}
