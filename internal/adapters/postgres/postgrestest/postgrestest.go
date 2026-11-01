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
const ServerEnv = "COBRANCH_TEST_DATABASE_URL"

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
	return databaseURL(t, serverURL, name)
}

// databaseURL is the URL of database name on the server at serverURL.
func databaseURL(t testing.TB, serverURL, name string) string {
	t.Helper()
	u, err := url.Parse(serverURL)
	if err != nil {
		t.Fatalf("parsing %s: %v", ServerEnv, err)
	}
	u.Path = "/" + name
	return u.String()
}

// Copy copies the database at sourceURL, which Database created, into a new one, as restoring a backup of it would,
// drops the copy when the test ends, and returns its URL. It ends every other session on the source first, since
// PostgreSQL copies only a database nobody is connected to; their pools connect again as they need.
func Copy(t testing.TB, sourceURL string) string {
	t.Helper()
	ctx := context.Background()
	serverURL := os.Getenv(ServerEnv)
	admin, err := pgx.Connect(ctx, serverURL)
	if err != nil {
		t.Fatalf("connecting to the test server: %v", err)
	}
	defer func() { _ = admin.Close(ctx) }()
	source, err := url.Parse(sourceURL)
	if err != nil {
		t.Fatalf("parsing the source's URL: %v", err)
	}
	from := strings.TrimPrefix(source.Path, "/")
	name := "test_" + strings.ToLower(rand.Text())
	if _, err := admin.Exec(ctx, "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1", from); err != nil {
		t.Fatalf("ending the sessions on %s: %v", from, err)
	}
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name+" TEMPLATE "+pgx.Identifier{from}.Sanitize()); err != nil {
		t.Fatalf("copying %s into %s: %v", from, name, err)
	}
	t.Cleanup(func() {
		cleanup, err := pgx.Connect(ctx, serverURL)
		if err != nil {
			t.Errorf("connecting to the test server: %v", err)
			return
		}
		defer func() { _ = cleanup.Close(ctx) }()
		if _, err := cleanup.Exec(ctx, "DROP DATABASE "+name+" WITH (FORCE)"); err != nil {
			t.Errorf("dropping database %s: %v", name, err)
		}
	})
	return databaseURL(t, serverURL, name)
}
