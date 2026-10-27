// Package migrations holds DevHub's PostgreSQL schema migrations, numbered SQL files that goose applies in order. The
// API server applies them on start (internal/adapters/postgres).
//
// Migrations expand before they contract: pods of the old and the new version run side by side through a rolling
// upgrade, so a column is dropped only a release after nothing reads it.
package migrations

import "embed"

// FS holds the migrations, at its root.
//
//go:embed *.sql
var FS embed.FS
