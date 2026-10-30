package main

import (
	"context"
	"io"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"

	"cobranch.dev/db/seeds"
	"cobranch.dev/internal/adapters/postgres"
	"cobranch.dev/internal/adapters/postgres/postgrestest"
)

func TestRunMigrateSeeds(t *testing.T) {
	tests := []struct {
		name     string
		seed     string
		wantSeed bool
	}{
		{name: "an installation, unset", seed: "", wantSeed: false},
		{name: "seeds off", seed: "false", wantSeed: false},
		{name: "a preview", seed: "true", wantSeed: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url := postgrestest.Database(t)
			t.Setenv("COBRANCH_DATABASE_URL", url)
			t.Setenv("COBRANCH_SEED", tt.seed) // restored when the test ends
			if tt.seed == "" {
				if err := os.Unsetenv("COBRANCH_SEED"); err != nil {
					t.Fatal(err)
				}
			}
			if err := runMigrate(context.Background(), io.Discard); err != nil {
				t.Fatalf("runMigrate() = %v", err)
			}
			conn, err := pgx.Connect(context.Background(), url)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = conn.Close(context.Background()) }()
			var version *int64
			err = conn.QueryRow(context.Background(), "SELECT max(version_id) FROM "+postgres.SeedTable).Scan(&version)
			if gotSeed := err == nil && version != nil && *version == int64(len(seeds.Go)); gotSeed != tt.wantSeed {
				t.Errorf("seeded = %t (version %v, error %v), want %t", gotSeed, version, err, tt.wantSeed)
			}
		})
	}
}
