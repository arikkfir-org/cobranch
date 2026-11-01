package main

import (
	"bytes"
	"context"
	"testing"

	"cobranch.dev/internal/services/git"
	"cobranch.dev/internal/system/buildinfo"
)

func TestRun(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string
		env        map[string]string
		wantStderr string
	}{
		{name: "version", args: []string{"version"}, wantCode: 0, wantStdout: buildinfo.Version() + "\n"},
		{name: "no subcommand", args: nil, wantCode: 2, wantStderr: usage + "\n"},
		{name: "unknown subcommand", args: []string{"serve"}, wantCode: 2, wantStderr: usage + "\n"},
		{name: "REST server stopping in no time", args: []string{"rest-server"}, env: map[string]string{"COBRANCH_SHUTDOWN_TIMEOUT": "soon"}, wantCode: 1,
			wantStderr: "cobranch rest-server: configuring: reading the server's configuration from the COBRANCH_ variables: envconfig.Process: assigning COBRANCH_SHUTDOWN_TIMEOUT to ShutdownTimeout: converting 'soon' to type time.Duration. details: time: invalid duration \"soon\"\n"},
		{name: "GraphQL server stopping in no time", args: []string{"graphql-server"}, env: map[string]string{"COBRANCH_SHUTDOWN_TIMEOUT": "soon"}, wantCode: 1,
			wantStderr: "cobranch graphql-server: configuring: reading the server's configuration from the COBRANCH_ variables: envconfig.Process: assigning COBRANCH_SHUTDOWN_TIMEOUT to ShutdownTimeout: converting 'soon' to type time.Duration. details: time: invalid duration \"soon\"\n"},
		{name: "Admin server stopping in no time", args: []string{"admin-server"}, env: map[string]string{"COBRANCH_SHUTDOWN_TIMEOUT": "soon"}, wantCode: 1,
			wantStderr: "cobranch admin-server: configuring: reading the server's configuration from the COBRANCH_ variables: envconfig.Process: assigning COBRANCH_SHUTDOWN_TIMEOUT to ShutdownTimeout: converting 'soon' to type time.Duration. details: time: invalid duration \"soon\"\n"},
		{
			name:       "migrate with a database URL that isn't one",
			args:       []string{"migrate"},
			env:        map[string]string{"COBRANCH_DATABASE_URL": "postgres://cobranch:secret@db:port/cobranch"},
			wantCode:   1,
			wantStderr: "cobranch migrate: connecting to the database: parsing the database's connection settings: they are not a valid URL or keyword list\n",
		},
		{name: "Git server stopping in no time", args: []string{"git-server"}, env: map[string]string{"COBRANCH_SHUTDOWN_TIMEOUT": "soon"}, wantCode: 1,
			wantStderr: "cobranch git-server: configuring: reading git-server's configuration from the COBRANCH_ variables: envconfig.Process: assigning COBRANCH_SHUTDOWN_TIMEOUT to ShutdownTimeout: converting 'soon' to type time.Duration. details: time: invalid duration \"soon\"\n"},
		{name: "push worker polling at no interval", args: []string{"push-worker"}, env: map[string]string{"COBRANCH_PUSH_POLL_INTERVAL": "often"}, wantCode: 1,
			wantStderr: "cobranch push-worker: configuring: reading push-worker's configuration from the COBRANCH_ variables: envconfig.Process: assigning COBRANCH_PUSH_POLL_INTERVAL to PollInterval: converting 'often' to type time.Duration. details: time: invalid duration \"often\"\n"},
		{name: "a hook outside a push", args: []string{"git-hook"}, wantCode: 1, wantStderr: git.MessageNotRecorded + "\n"},
		{name: "extra arguments", args: []string{"version", "now"}, wantCode: 2, wantStderr: usage + "\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for k, v := range tt.env {
				t.Setenv(k, v)
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
