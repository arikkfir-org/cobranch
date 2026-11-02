package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestRunRender(t *testing.T) {
	checkout := t.TempDir()
	for name, content := range map[string]string{
		"cobranch.yaml": "delivery:\n  targets:\n    previews:\n      branches: [\"feature/**\"]\n      namespace: shop-${branch.slug}\n    broken:\n      branches: [main]\n      namespace: ${branch}\n" +
			"  roots:\n    - path: deploy\n      substitute: true\n",
		"deploy/cm.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: settings\n  namespace: ${namespace}\ndata:\n  commit: ${commit.short}\n",
	} {
		p := filepath.Join(checkout, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	missing := t.TempDir()
	bad := t.TempDir()
	if err := os.WriteFile(filepath.Join(bad, "cobranch.yaml"), []byte("delivery: [x]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
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
		{name: "no target", args: flags, wantCode: 2, wantStderr: renderUsage + "\n"},
		{name: "no flags", args: []string{"render"}, wantCode: 2, wantStderr: renderUsage + "\n"},
		{name: "an unknown flag", args: slices.Concat(flags, []string{"--target", "previews", "--context", "prod"}), wantCode: 2, wantStderr: renderUsage + "\n"},
		{name: "an argument", args: slices.Concat(flags, []string{"--target", "previews", "deploy"}), wantCode: 2, wantStderr: renderUsage + "\n"},
		{name: "a target the file lacks", args: slices.Concat(flags, []string{"--target", "staging"}), wantCode: 1,
			wantStderr: "cobranch render: planning: invalid delivery: no target staging\n"},
		{name: "a namespace that isn't one", args: slices.Concat(flags, []string{"--target", "broken"}), wantCode: 1,
			wantStderr: "cobranch render: planning: invalid delivery: target broken's namespace \"feature/Login\" is not a DNS label: 1 to 63 lowercase letters, digits and '-'\n"},
		{name: "no settings", args: []string{"render", "--dir", missing, "--target", "t", "--branch", "main", "--commit", "abc"}, wantCode: 1,
			wantStderr: "cobranch render: reading /cobranch.yaml: open " + filepath.Join(missing, "cobranch.yaml") + ": no such file or directory\n"},
		{name: "settings that don't parse", args: []string{"render", "--dir", bad, "--target", "t", "--branch", "main", "--commit", "abc"}, wantCode: 1,
			wantStderr: "cobranch render: parsing: /cobranch.yaml: line 1: delivery must be a mapping\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
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
