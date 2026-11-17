-- The platform's own records (SQLite, in <data folder>/platform.db). Each app's
-- data lives in its own file, not here.
--
-- Text columns are as narrow as what they hold (see AGENTS.md). SQLite does
-- not enforce the length in a type such as varchar(40), so every such column
-- also has a CHECK that does; the Go code enforces the same limits first, so
-- people get a plain message, not a database error. `text` is kept for the
-- few values that can be large.
--
-- Times are whole seconds since 1970 (UTC), written by the Go code.

CREATE TABLE apps (
	slug         varchar(52) PRIMARY KEY CHECK (length(slug) <= 52),              -- serving slug: "inventory" (30 at most) or "inventory--new-form" (plus "--" and 20)
	parent       varchar(30) CHECK (length(parent) <= 30),                        -- app slug for a draft, NULL for a live app
	name         varchar(100) NOT NULL CHECK (length(name) <= 100),
	description  varchar(500) NOT NULL DEFAULT '' CHECK (length(description) <= 500),
	live_version varchar(40) NOT NULL DEFAULT '' CHECK (length(live_version) <= 40),  -- '' until first published
	created_by   varchar(40) NOT NULL DEFAULT '' CHECK (length(created_by) <= 40),
	created_at   integer NOT NULL,
	published_at integer,
	last_used_at integer NOT NULL
);

CREATE TABLE settings (
	slug  varchar(52) NOT NULL CHECK (length(slug) <= 52),
	key   varchar(100) NOT NULL CHECK (length(key) <= 100),
	value text NOT NULL,                                                           -- may be a certificate or a JSON key file
	PRIMARY KEY (slug, key)
);

CREATE TABLE publishes (
	id           integer PRIMARY KEY AUTOINCREMENT,
	slug         varchar(52) NOT NULL CHECK (length(slug) <= 52),
	version      varchar(40) NOT NULL CHECK (length(version) <= 40),
	published_by varchar(40) NOT NULL DEFAULT '' CHECK (length(published_by) <= 40),
	published_at integer NOT NULL,
	ok           boolean NOT NULL,
	detail       text NOT NULL DEFAULT ''                                          -- the steps, or a traceback
);

CREATE TABLE users (
	username      varchar(40) PRIMARY KEY CHECK (length(username) <= 40),
	password_hash varchar(200) NOT NULL CHECK (length(password_hash) <= 200),      -- Argon2id, encoded
	is_admin      boolean NOT NULL DEFAULT false,
	created_at    integer NOT NULL
);

CREATE TABLE sessions (
	token_hash varchar(64) PRIMARY KEY CHECK (length(token_hash) = 64),            -- SHA-256, hex: always 64 characters
	username   varchar(40) NOT NULL REFERENCES users (username) ON DELETE CASCADE,
	expires_at integer NOT NULL
);

CREATE TABLE access_keys (
	token_hash   varchar(64) PRIMARY KEY CHECK (length(token_hash) = 64),          -- SHA-256, hex: always 64 characters
	username     varchar(40) NOT NULL REFERENCES users (username) ON DELETE CASCADE,
	name         varchar(60) NOT NULL DEFAULT '' CHECK (length(name) <= 60),
	created_at   integer NOT NULL,
	last_used_at integer
);
