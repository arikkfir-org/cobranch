package main

import (
	"bytes"
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
		{name: "extra arguments", args: []string{"version", "now"}, wantCode: 2, wantStderr: usage + "\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if got := run(tt.args, &stdout, &stderr); got != tt.wantCode {
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
