-- Created inside each app's own data file: which of the app's
-- migrations/*.sql files have been applied.
CREATE TABLE IF NOT EXISTS caddysnake_migrations (
	name       varchar(200) PRIMARY KEY CHECK (length(name) <= 200),
	applied_at integer NOT NULL
);
