package main

import (
	"bytes"
	"context"
	"os"
	"testing"

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
		{name: "REST server without a bucket", args: []string{"rest-server"}, wantCode: 1, wantStderr: "cobranch rest-server: configuring: reading the server's configuration from the COBRANCH_ variables: required key BLOB_URL missing value\n"},
		{name: "GraphQL server without a bucket", args: []string{"graphql-server"}, wantCode: 1, wantStderr: "cobranch graphql-server: configuring: reading the server's configuration from the COBRANCH_ variables: required key BLOB_URL missing value\n"},
		{name: "Admin server without a bucket", args: []string{"admin-server"}, wantCode: 1, wantStderr: "cobranch admin-server: configuring: reading the server's configuration from the COBRANCH_ variables: required key BLOB_URL missing value\n"},
		{
			name:       "migrate with a database URL that isn't one",
			args:       []string{"migrate"},
			env:        map[string]string{"COBRANCH_DATABASE_URL": "postgres://cobranch:secret@db:port/cobranch"},
			wantCode:   1,
			wantStderr: "cobranch migrate: connecting to the database: parsing the database's connection settings: they are not a valid URL or keyword list\n",
		},
		{name: "extra arguments", args: []string{"version", "now"}, wantCode: 2, wantStderr: usage + "\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("COBRANCH_BLOB_URL", "") // restored when the test ends
			if err := os.Unsetenv("COBRANCH_BLOB_URL"); err != nil {
				t.Fatal(err)
			}
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
