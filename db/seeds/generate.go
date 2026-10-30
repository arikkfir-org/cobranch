package seeds

// sqlc writes package queries from db/migrations and queries.sql (sqlc.yaml); the seeds call it, and write no SQL.
//go:generate go tool sqlc -f ../../sqlc.yaml generate
