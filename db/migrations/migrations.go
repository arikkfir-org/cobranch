// Package migrations holds Cobranch's PostgreSQL schema migrations, which goose applies in order: numbered SQL files,
// and Go for what SQL can't say. The migrate Job applies them before the servers roll out (internal/adapters/postgres),
// and a server turns ready only once the schema is at the highest version they number.
//
// Each release's migrations keep the previous release's pods working, since both run side by side through a rolling
// upgrade: a column is dropped only once neither the new release nor the one before it reads it, so a rollback goes
// back one release at most. A long backfill runs later, as a task of the controller's.
package migrations

import (
	"embed"

	"github.com/pressly/goose/v3"
)

// FS holds the SQL migrations, at its root.
//
//go:embed *.sql
var FS embed.FS

// Go holds the migrations written in Go, each made with goose.NewGoMigration and numbered among the SQL files. None
// yet.
var Go []*goose.Migration
