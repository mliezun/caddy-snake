-- Lookups against the server's catalogs, used when creating a database and a
-- login for each app. The CREATE/ALTER/DROP statements themselves take
-- identifiers, which cannot be query parameters, so they are built in
-- postgres.go from validated names.

-- name: RoleExists :one
SELECT EXISTS (SELECT 1 FROM pg_catalog.pg_roles WHERE rolname = sqlc.arg(name)::text);

-- name: DatabaseExists :one
SELECT EXISTS (SELECT 1 FROM pg_catalog.pg_database WHERE datname = sqlc.arg(name)::text);
