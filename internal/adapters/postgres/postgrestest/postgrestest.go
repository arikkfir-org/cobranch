// Package postgrestest gives tests a database of their own on a real PostgreSQL server.
package postgrestest

import (
	"context"
	"crypto/rand"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

// ServerEnv names the variable that holds the test server's URL. CI sets it; without it, tests that need a database
// are skipped, except in CI, where they fail.
const ServerEnv = "DEVHUB_TEST_DATABASE_URL"

// Database creates an empty database on the server ServerEnv names, drops it when the test ends, and returns its URL.
func Database(t testing.TB) string {
	t.Helper()
	serverURL := os.Getenv(ServerEnv)
	if serverURL == "" {
		if os.Getenv("CI") != "" {
			t.Fatal(ServerEnv + " is not set, and CI must run the tests that need a database")
		}
		t.Skip(ServerEnv + " is not set")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, serverURL)
	if err != nil {
		t.Fatalf("connecting to the test server: %v", err)
	}
	name := "test_" + strings.ToLower(rand.Text())
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatalf("creating database %s: %v", name, err)
	}
	t.Cleanup(func() {
		defer func() { _ = admin.Close(ctx) }()
		if _, err := admin.Exec(ctx, "DROP DATABASE "+name+" WITH (FORCE)"); err != nil {
			t.Errorf("dropping database %s: %v", name, err)
		}
	})
	u, err := url.Parse(serverURL)
	if err != nil {
		t.Fatalf("parsing %s: %v", ServerEnv, err)
	}
	u.Path = "/" + name
	return u.String()
}
