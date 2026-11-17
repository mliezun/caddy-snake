-- Run against an app's own data file (not the platform's).

-- name: ListAppliedAppMigrations :many
SELECT name FROM caddysnake_migrations ORDER BY name;

-- name: RecordAppMigration :exec
INSERT INTO caddysnake_migrations (name, applied_at) VALUES (sqlc.arg(name), sqlc.arg(applied_at));
