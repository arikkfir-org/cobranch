package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"testing"
)

func TestSetup(t *testing.T) {
	tests := []struct {
		name     string
		level    string
		wantLogs int
		wantErr  bool
	}{
		{name: "info by default", level: "", wantLogs: 1},
		{name: "debug", level: "debug", wantLogs: 2},
		{name: "error", level: "error", wantLogs: 0},
		{name: "unknown level", level: "loud", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer slog.SetDefault(slog.Default())
			setLevel(t, tt.level)
			var out bytes.Buffer
			err := Setup(&out, "rest-server", "v1.2.3")
			if (err != nil) != tt.wantErr {
				t.Fatalf("Setup() error = %v, want error %t", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			ctx := context.Background()
			slog.DebugContext(ctx, "Debugging")
			slog.InfoContext(ctx, "Started")
			checkRecords(t, out.Bytes(), tt.wantLogs)
		})
	}
}

// setLevel sets DEVHUB_LOG_LEVEL to level for the test, or unsets it when level is empty.
func setLevel(t *testing.T, level string) {
	t.Helper()
	t.Setenv("DEVHUB_LOG_LEVEL", level) // restored when the test ends
	if level != "" {
		return
	}
	if err := os.Unsetenv("DEVHUB_LOG_LEVEL"); err != nil {
		t.Fatal(err)
	}
}

// checkRecords checks that out holds want JSON records, each naming the workload and the version.
func checkRecords(t *testing.T, out []byte, want int) {
	t.Helper()
	var lines [][]byte
	if len(out) > 0 {
		lines = bytes.Split(bytes.TrimSpace(out), []byte("\n"))
	}
	if len(lines) != want {
		t.Fatalf("logged %d records, want %d:\n%s", len(lines), want, out)
	}
	for _, line := range lines {
		var record map[string]any
		if err := json.Unmarshal(line, &record); err != nil {
			t.Fatalf("record %q is not JSON: %v", line, err)
		}
		if record["workload"] != "rest-server" || record["version"] != "v1.2.3" {
			t.Errorf("record %q lacks the workload and version", line)
		}
	}
}
