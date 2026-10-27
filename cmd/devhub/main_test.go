package main

import (
	"bytes"
	"context"
	"os"
	"testing"

	"github.com/arikkfir-org/devhub/internal/system/buildinfo"
)

func TestRun(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{name: "version", args: []string{"version"}, wantCode: 0, wantStdout: buildinfo.Version() + "\n"},
		{name: "no subcommand", args: nil, wantCode: 2, wantStderr: usage + "\n"},
		{name: "unknown subcommand", args: []string{"serve"}, wantCode: 2, wantStderr: usage + "\n"},
		{name: "API server without a bucket", args: []string{"api-server"}, wantCode: 1, wantStderr: "devhub api-server: configuring: reading the API server's configuration from the DEVHUB_ variables: required key BLOB_URL missing value\n"},
		{name: "extra arguments", args: []string{"version", "now"}, wantCode: 2, wantStderr: usage + "\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("DEVHUB_BLOB_URL", "") // restored when the test ends
			if err := os.Unsetenv("DEVHUB_BLOB_URL"); err != nil {
				t.Fatal(err)
			}
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
