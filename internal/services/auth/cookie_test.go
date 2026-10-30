package auth

import (
	"net/url"
	"testing"
)

func TestCookieScope(t *testing.T) {
	tests := []struct {
		name    string
		urls    []string
		want    Cookie
		wantErr bool
	}{
		{name: "one host", urls: []string{"https://cobranch.example.com", "https://cobranch.example.com/api", "https://cobranch.example.com/auth"}, want: Cookie{Name: HostCookie}},
		{name: "one host, cased differently", urls: []string{"https://Cobranch.example.com", "https://cobranch.Example.com/api"}, want: Cookie{Name: HostCookie}},
		{name: "a host each", urls: []string{"https://cobranch.example.com", "https://api.cobranch.example.com", "https://auth.cobranch.example.com"}, want: Cookie{Name: SecureCookie, Domain: "cobranch.example.com"}},
		{name: "siblings", urls: []string{"https://console.cobranch.example.com", "https://api.cobranch.example.com"}, want: Cookie{Name: SecureCookie, Domain: "cobranch.example.com"}},
		{name: "a parent that is a registrable domain", urls: []string{"https://console.example.com", "https://api.example.com"}, want: Cookie{Name: SecureCookie, Domain: "example.com"}},
		{name: "a parent that is a public suffix", urls: []string{"https://a.github.io", "https://b.github.io"}, wantErr: true},
		{name: "no common parent", urls: []string{"https://cobranch.example.com", "https://cobranch.example.org"}, wantErr: true},
		{name: "a URL without a host", urls: []string{"https://cobranch.example.com", "/api"}, wantErr: true},
		{name: "no URLs", wantErr: true},
		{name: "nil URLs skipped", urls: []string{"https://cobranch.example.com", ""}, want: Cookie{Name: HostCookie}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var urls []*url.URL
			for _, s := range tt.urls {
				if s == "" {
					urls = append(urls, nil)
					continue
				}
				u, err := url.Parse(s)
				if err != nil {
					t.Fatal(err)
				}
				urls = append(urls, u)
			}
			got, err := CookieScope(urls...)
			if (err != nil) != tt.wantErr {
				t.Fatalf("CookieScope() error = %v, want error %t", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("CookieScope() = %+v, want %+v", got, tt.want)
			}
		})
	}
}
