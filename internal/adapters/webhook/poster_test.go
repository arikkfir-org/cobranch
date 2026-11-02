package webhook

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"cobranch.dev/internal/services/runner"
)

// receiver is an https server on loopback that answers status, or redirects to /elsewhere, recording what it got.
func receiver(t *testing.T, status int) (*httptest.Server, *[]string) {
	t.Helper()
	var got []string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body) // the test reads what arrived, if anything
		got = append(got, r.Method+" "+r.URL.Path+" "+r.Header.Get("X-Test")+" "+string(body))
		if status == http.StatusFound {
			http.Redirect(w, r, "/elsewhere", status)
			return
		}
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return srv, &got
}

// trusting is a Poster whose ranges are except and allowed, trusting srv's certificate.
func trusting(t *testing.T, srv *httptest.Server, except, allowed []string) *Poster {
	t.Helper()
	p, err := New(except, allowed)
	if err != nil {
		t.Fatal(err)
	}
	p.client.Transport.(*http.Transport).TLSClientConfig = srv.Client().Transport.(*http.Transport).TLSClientConfig
	return p
}

func TestPost(t *testing.T) {
	loopback := []string{"127.0.0.0/8", "::1/128"}
	tests := []struct {
		name            string
		status          int
		except, allowed []string
		url             func(base string) string
		wantErr         string
		unreachable     bool
		wantGot         int
	}{
		{name: "a receiver that answers 2xx", status: http.StatusAccepted, url: func(b string) string { return b + "/hook" }, wantGot: 1},
		{name: "a receiver that fails", status: http.StatusInternalServerError, url: func(b string) string { return b + "/hook" },
			wantErr: "answered 500 Internal Server Error", wantGot: 1},
		{name: "a redirect, never followed", status: http.StatusFound, url: func(b string) string { return b + "/hook" },
			wantErr: "answered 302 Found", wantGot: 1},
		{name: "an address in a range runs may not reach", status: http.StatusOK, except: loopback,
			url: func(b string) string { return b + "/hook" }, unreachable: true, wantErr: "127.0.0.1, in 127.0.0.0/8"},
		{name: "a range the operator allowed", status: http.StatusOK, except: loopback, allowed: []string{"127.0.0.1/32"},
			url: func(b string) string { return b + "/hook" }, wantGot: 1},
		{name: "plain HTTP", status: http.StatusOK, url: func(b string) string { return strings.Replace(b, "https:", "http:", 1) },
			unreachable: true, wantErr: "not an https URL"},
		{name: "the unspecified address", status: http.StatusOK,
			url: func(b string) string { return "https://0.0.0.0:" + b[strings.LastIndex(b, ":")+1:] + "/hook" }, unreachable: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, got := receiver(t, tt.status)
			p := trusting(t, srv, tt.except, tt.allowed)
			err := p.Post(context.Background(), tt.url(srv.URL), []byte(`{"run":1}`), map[string]string{"X-Test": "signed"})
			switch {
			case tt.wantErr == "" && !tt.unreachable && err != nil:
				t.Fatalf("Post() = %v", err)
			case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
				t.Fatalf("Post() = %v, want %q", err, tt.wantErr)
			case errors.Is(err, runner.ErrUnreachable) != tt.unreachable:
				t.Fatalf("Post() = %v, unreachable %v", err, tt.unreachable)
			}
			if len(*got) != tt.wantGot {
				t.Fatalf("the receiver got %q, want %d posts", *got, tt.wantGot)
			}
			if tt.wantGot > 0 && (*got)[0] != `POST /hook signed {"run":1}` {
				t.Errorf("the receiver got %q", (*got)[0])
			}
		})
	}
}

func TestNewRefusesBadRanges(t *testing.T) {
	if _, err := New([]string{"10.0.0.0/33"}, nil); err == nil {
		t.Error("New() with a bad range = nil")
	}
	if _, err := New(nil, []string{"nowhere"}); err == nil {
		t.Error("New() with a bad allowed range = nil")
	}
}
