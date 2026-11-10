-- Used by the migration runner in ../migrate.go.

-- name: LockMigrations :exec
SELECT pg_advisory_lock(sqlc.arg(key)::bigint);

-- name: UnlockMigrations :exec
SELECT pg_advisory_unlock(sqlc.arg(key)::bigint);

-- name: ListAppliedMigrations :many
SELECT name FROM caddysnake_apps.schema_migrations ORDER BY name;

-- name: RecordMigration :exec
INSERT INTO caddysnake_apps.schema_migrations (name) VALUES ($1);
