package config

import (
	"os"
	"testing"
	"time"
)

func TestLoadAPIServer(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		want    APIServer
		wantErr bool
	}{
		{
			name: "defaults",
			env:  map[string]string{"DEVHUB_BLOB_URL": "file:///blob"},
			want: APIServer{HTTPAddr: ":8080", BlobURL: "file:///blob", ShutdownTimeout: 20 * time.Second},
		},
		{
			name: "every variable",
			env: map[string]string{
				"DEVHUB_HTTP_ADDR":        ":9090",
				"DEVHUB_DATABASE_URL":     "postgres://db/devhub",
				"DEVHUB_BLOB_URL":         "gs://bucket",
				"DEVHUB_SHUTDOWN_TIMEOUT": "5s",
			},
			want: APIServer{
				HTTPAddr:        ":9090",
				DatabaseURL:     "postgres://db/devhub",
				BlobURL:         "gs://bucket",
				ShutdownTimeout: 5 * time.Second,
			},
		},
		{name: "no bucket", env: map[string]string{}, wantErr: true},
		{
			name:    "bad duration",
			env:     map[string]string{"DEVHUB_BLOB_URL": "file:///blob", "DEVHUB_SHUTDOWN_TIMEOUT": "soon"},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, name := range []string{"HTTP_ADDR", "DATABASE_URL", "BLOB_URL", "SHUTDOWN_TIMEOUT"} {
				unsetenv(t, "DEVHUB_"+name)
			}
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			got, err := LoadAPIServer()
			if (err != nil) != tt.wantErr {
				t.Fatalf("LoadAPIServer() error = %v, want error %t", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("LoadAPIServer() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// unsetenv unsets name for the test, and sets it back when the test ends.
func unsetenv(t *testing.T, name string) {
	t.Helper()
	t.Setenv(name, "") // records the value to restore
	if err := os.Unsetenv(name); err != nil {
		t.Fatal(err)
	}
}
