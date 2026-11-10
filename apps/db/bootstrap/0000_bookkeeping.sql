-- Applied on every start, before any migration: the schema that holds every
-- platform table, and the table recording which migration files have run.
-- Keep it idempotent.
CREATE SCHEMA IF NOT EXISTS caddysnake_apps;

CREATE TABLE IF NOT EXISTS caddysnake_apps.schema_migrations (
	name       text PRIMARY KEY,
	applied_at timestamptz NOT NULL DEFAULT now()
);
