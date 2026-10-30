package postgres

// sqlc writes package queries from db/migrations and db/queries (sqlc.yaml); the stores here call it, and write no
// SQL but Schema's read of goose's version table, whose name sqlc doesn't know.
//go:generate go tool sqlc -f ../../../sqlc.yaml generate
