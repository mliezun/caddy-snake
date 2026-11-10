-- Run against an app's own database (not the platform's).

-- name: ListAppliedAppMigrations :many
SELECT name FROM caddysnake_migrations ORDER BY name;

-- name: RecordAppMigration :exec
INSERT INTO caddysnake_migrations (name) VALUES ($1);
