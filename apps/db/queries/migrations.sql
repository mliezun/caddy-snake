-- Used by the migration runner in ../migrate.go.

-- name: ListAppliedMigrations :many
SELECT name FROM schema_migrations ORDER BY name;

-- name: RecordMigration :exec
INSERT INTO schema_migrations (name, applied_at) VALUES (sqlc.arg(name), sqlc.arg(applied_at));
