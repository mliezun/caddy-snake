-- Applied on every start, before the migrations: where they are tracked.
CREATE TABLE IF NOT EXISTS schema_migrations (
	name       varchar(200) PRIMARY KEY CHECK (length(name) <= 200),
	applied_at integer NOT NULL
);
