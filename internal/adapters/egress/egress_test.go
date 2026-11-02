package egress

import (
	"errors"
	"strings"
	"testing"
)

var errRefused = errors.New("refused")

func TestPrefixes(t *testing.T) {
	got, err := Prefixes([]string{"10.1.2.3/8", "::1/128"})
	if err != nil || len(got) != 2 || got[0].String() != "10.0.0.0/8" || got[1].String() != "::1/128" {
		t.Errorf("Prefixes() = %v, %v, want them masked", got, err)
	}
	if _, err := Prefixes([]string{"10.0.0.0"}); err == nil || !strings.Contains(err.Error(), `range "10.0.0.0"`) {
		t.Errorf("Prefixes(an address) = %v, want the range named", err)
	}
}

func TestCheck(t *testing.T) {
	deny, err := Prefixes([]string{"10.0.0.0/8", "127.0.0.0/8", "fd00::/8"})
	if err != nil {
		t.Fatal(err)
	}
	allow, err := Prefixes([]string{"10.20.0.0/16"})
	if err != nil {
		t.Fatal(err)
	}
	g := Guard{Deny: deny, Allow: allow, Refused: errRefused}
	tests := []struct {
		name, address string
		refused       bool
		wantErr       string
	}{
		{name: "outside every range", address: "192.0.2.1:443"},
		{name: "in a denied range", address: "10.1.1.1:443", refused: true, wantErr: "10.1.1.1, in 10.0.0.0/8"},
		{name: "in an allowed range inside a denied one", address: "10.20.3.4:6443"},
		{name: "an IPv4-mapped IPv6 address in a denied range", address: "[::ffff:127.0.0.1]:443", refused: true},
		{name: "an IPv6 address in a denied range", address: "[fd12::1]:443", refused: true},
		{name: "the unspecified address", address: "0.0.0.0:443", refused: true},
		{name: "the unspecified IPv6 address", address: "[::]:443", refused: true},
		{name: "a name, never dialled", address: "example.com:443", wantErr: "reading address"},
		{name: "no port", address: "192.0.2.1", wantErr: "reading address"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := g.Check(tt.address)
			switch {
			case errors.Is(err, errRefused) != tt.refused:
				t.Errorf("Check() = %v, refused %t", err, tt.refused)
			case tt.wantErr == "" && !tt.refused && err != nil:
				t.Errorf("Check() = %v", err)
			case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
				t.Errorf("Check() = %v, want %q", err, tt.wantErr)
			}
		})
	}
}
