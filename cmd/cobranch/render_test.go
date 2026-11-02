package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// checkoutOf is a checkout of a temporary directory, holding files, by path.
func checkoutOf(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestRunRender(t *testing.T) {
	checkout := checkoutOf(t, map[string]string{
		"cobranch.yaml": "delivery:\n  targets:\n    previews:\n      branches: [\"feature/**\"]\n      namespace: shop-${branch.slug}\n    broken:\n      branches: [main]\n      namespace: ${branch}\n" +
			"  roots:\n    - path: deploy\n      substitute: true\n",
		"deploy/cm.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: settings\n  namespace: ${namespace}\ndata:\n  commit: ${commit.short}\n",
	})
	missing, bad := t.TempDir(), checkoutOf(t, map[string]string{"cobranch.yaml": "delivery: [x]\n"})
	flags := []string{"render", "--dir", checkout, "--branch", "feature/Login", "--commit", "3b7d0c4e9f0a"}
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{name: "a render", args: slices.Concat(flags, []string{"--target", "previews"}), wantCode: 0,
			wantStdout: "---\napiVersion: v1\ndata:\n  commit: 3b7d0c4\nkind: ConfigMap\nmetadata:\n  name: settings\n  namespace: shop-feature-login\n"},
		{name: "main's target", args: slices.Concat(flags, []string{"--target", "staging", "--namespace", "shop-staging"}), wantCode: 0,
			wantStdout: "---\napiVersion: v1\ndata:\n  commit: 3b7d0c4\nkind: ConfigMap\nmetadata:\n  name: settings\n  namespace: shop-staging\n"},
		{name: "a namespace that isn't one", args: slices.Concat(flags, []string{"--target", "staging", "--namespace", "Shop"}), wantCode: 1,
			wantStderr: "cobranch render: planning: invalid delivery: target staging's namespace \"Shop\" is not a DNS label: 1 to 63 lowercase letters, digits and '-'\n"},
		{name: "no target", args: flags, wantCode: 2, wantStderr: renderUsage + "\n"},
		{name: "no flags", args: []string{"render"}, wantCode: 2, wantStderr: renderUsage + "\n"},
		{name: "an unknown flag", args: slices.Concat(flags, []string{"--target", "previews", "--context", "prod"}), wantCode: 2, wantStderr: renderUsage + "\n"},
		{name: "an argument", args: slices.Concat(flags, []string{"--target", "previews", "deploy"}), wantCode: 2, wantStderr: renderUsage + "\n"},
		{name: "a target the file lacks", args: slices.Concat(flags, []string{"--target", "staging"}), wantCode: 1,
			wantStderr: "cobranch render: planning: invalid delivery: no target staging: give the environment's namespace, or name a target the checkout declares\n"},
		{name: "a target's namespace that isn't one", args: slices.Concat(flags, []string{"--target", "broken"}), wantCode: 1,
			wantStderr: "cobranch render: planning: invalid delivery: target broken's namespace \"feature/Login\" is not a DNS label: 1 to 63 lowercase letters, digits and '-'\n"},
		{name: "no settings, for main's target", args: []string{"render", "--dir", missing, "--target", "t", "--namespace", "shop", "--branch", "main",
			"--commit", "abc"}, wantCode: 0},
		{name: "no settings", args: []string{"render", "--dir", missing, "--target", "t", "--branch", "main", "--commit", "abc"}, wantCode: 1,
			wantStderr: "cobranch render: planning: invalid delivery: no target t: give the environment's namespace, or name a target the checkout declares\n"},
		{name: "settings that don't parse", args: []string{"render", "--dir", bad, "--target", "t", "--branch", "main", "--commit", "abc"}, wantCode: 1,
			wantStderr: "cobranch render: parsing: /cobranch.yaml: line 1: delivery must be a mapping\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("COBRANCH_UPLOAD_URL", "")
			var stdout, stderr bytes.Buffer
			if got := run(context.Background(), tt.args, &stdout, &stderr); got != tt.wantCode {
				t.Errorf("run(%q) = %d, want %d", tt.args, got, tt.wantCode)
			}
			if stdout.String() != tt.wantStdout {
				t.Errorf("run(%q) stdout = %q, want %q", tt.args, stdout.String(), tt.wantStdout)
			}
			if stderr.String() != tt.wantStderr {
				t.Errorf("run(%q) stderr = %q, want %q", tt.args, stderr.String(), tt.wantStderr)
			}
		})
	}
}

// TestRunRenderUploads posts the render to its delivery with its token, as a render pod does, and prints none of it.
func TestRunRenderUploads(t *testing.T) {
	checkout := checkoutOf(t, map[string]string{"cobranch.yaml": "delivery:\n  roots:\n    - path: deploy\n",
		"deploy/cm.yaml": "apiVersion: v1\nkind: Secret\nmetadata:\n  name: keys\n"})
	tests := []struct {
		name                   string
		status                 int
		body                   string
		wantCode               int
		wantStdout, wantStderr string
	}{
		{name: "kept", status: http.StatusCreated, wantStdout: "Uploaded the render, 55 bytes.\n"},
		{name: "a delivery that no longer renders", status: http.StatusConflict, wantCode: 1,
			body:       `{"title":"Conflict","detail":"delivery #7 doesn't render"}`,
			wantStderr: "cobranch render: uploading the render: 409 Conflict: Conflict: delivery #7 doesn't render\n"},
		{name: "a server that says nothing more", status: http.StatusBadGateway, wantCode: 1,
			wantStderr: "cobranch render: uploading the render: 502 Bad Gateway\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body) // the test reads what it can
				got = append(got, r.Method+" "+r.URL.Path+" "+r.Header.Get("Authorization")+" "+r.Header.Get("Content-Type")+" "+string(body))
				w.WriteHeader(tt.status)
				_, _ = io.WriteString(w, tt.body) // the client reads what it can
			}))
			defer srv.Close()
			t.Setenv("COBRANCH_UPLOAD_URL", srv.URL+"/v1/orgs/acme/repos/site/deliveries/7/manifests")
			t.Setenv("COBRANCH_TOKEN", "cbd_token")
			var stdout, stderr bytes.Buffer
			args := []string{"render", "--dir", checkout, "--target", "preview", "--namespace", "shop", "--branch", "main", "--commit", "abc"}
			if code := run(context.Background(), args, &stdout, &stderr); code != tt.wantCode {
				t.Errorf("run() = %d, want %d", code, tt.wantCode)
			}
			want := []string{"POST /v1/orgs/acme/repos/site/deliveries/7/manifests Bearer cbd_token application/yaml " +
				"---\napiVersion: v1\nkind: Secret\nmetadata:\n  name: keys\n"}
			if !slices.Equal(got, want) {
				t.Errorf("requests = %q, want %q", got, want)
			}
			if stdout.String() != tt.wantStdout || stderr.String() != tt.wantStderr {
				t.Errorf("stdout = %q, stderr = %q, want %q and %q", stdout.String(), stderr.String(), tt.wantStdout, tt.wantStderr)
			}
		})
	}
}
