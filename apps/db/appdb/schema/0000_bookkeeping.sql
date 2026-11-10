-- Created inside every app's own database on publish: records which of the
-- app's migrations/*.sql files have been applied there. Keep it idempotent.
CREATE TABLE IF NOT EXISTS caddysnake_migrations (
	name       text PRIMARY KEY,
	applied_at timestamptz NOT NULL DEFAULT now()
);
