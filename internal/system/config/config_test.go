package config

import (
	"os"
	"testing"
	"time"
)

func TestLoadServer(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		want    Server
		wantErr bool
	}{
		{
			name: "defaults",
			env:  map[string]string{"DEVHUB_BLOB_URL": "file:///blob"},
			want: Server{HTTPAddr: ":8080", BlobURL: "file:///blob", ShutdownTimeout: 20 * time.Second},
		},
		{
			name: "every variable",
			env: map[string]string{
				"DEVHUB_HTTP_ADDR":        ":9090",
				"DEVHUB_DATABASE_URL":     "postgres://db/devhub",
				"DEVHUB_BLOB_URL":         "gs://bucket",
				"DEVHUB_SHUTDOWN_TIMEOUT": "5s",
				"DEVHUB_CONSOLE_URL":      "https://Console.Example.com/home",
			},
			want: Server{
				HTTPAddr:        ":9090",
				DatabaseURL:     "postgres://db/devhub",
				BlobURL:         "gs://bucket",
				ShutdownTimeout: 5 * time.Second,
				ConsoleOrigin:   "https://console.example.com",
			},
		},
		{name: "the Console set empty", env: map[string]string{"DEVHUB_BLOB_URL": "file:///blob", "DEVHUB_CONSOLE_URL": ""}, want: Server{HTTPAddr: ":8080", BlobURL: "file:///blob", ShutdownTimeout: 20 * time.Second}},
		{name: "the Console without a scheme", env: map[string]string{"DEVHUB_BLOB_URL": "file:///blob", "DEVHUB_CONSOLE_URL": "console"}, wantErr: true},
		{name: "no bucket", env: map[string]string{}, wantErr: true},
		{
			name:    "bad duration",
			env:     map[string]string{"DEVHUB_BLOB_URL": "file:///blob", "DEVHUB_SHUTDOWN_TIMEOUT": "soon"},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, name := range []string{"HTTP_ADDR", "DATABASE_URL", "BLOB_URL", "SHUTDOWN_TIMEOUT", "CONSOLE_URL"} {
				unsetenv(t, "DEVHUB_"+name)
			}
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			got, err := LoadServer()
			if (err != nil) != tt.wantErr {
				t.Fatalf("LoadServer() error = %v, want error %t", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("LoadServer() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestOriginDecode(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		want    Origin
		wantErr bool
	}{
		{name: "a URL with a path", value: "https://console.example.com/home", want: "https://console.example.com"},
		{name: "a host in capitals", value: "https://Console.Example.com", want: "https://console.example.com"},
		{name: "a port", value: "http://localhost:8080/", want: "http://localhost:8080"},
		{name: "empty", value: "", want: ""},
		{name: "no scheme", value: "console.example.com", wantErr: true},
		{name: "another scheme", value: "ftp://console.example.com", wantErr: true},
		{name: "no host", value: "https:///home", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got Origin
			err := got.Decode(tt.value)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Decode(%q) error = %v, want error %t", tt.value, err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("Decode(%q) = %q, want %q", tt.value, got, tt.want)
			}
		})
	}
}

func TestLoadMigrate(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want Migrate
	}{
		{name: "libpq's variables", env: map[string]string{}, want: Migrate{}},
		{name: "a URL", env: map[string]string{"DEVHUB_DATABASE_URL": "postgres://db/devhub"}, want: Migrate{DatabaseURL: "postgres://db/devhub"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			unsetenv(t, "DEVHUB_DATABASE_URL")
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			got, err := LoadMigrate()
			if err != nil {
				t.Fatalf("LoadMigrate() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("LoadMigrate() = %+v, want %+v", got, tt.want)
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
