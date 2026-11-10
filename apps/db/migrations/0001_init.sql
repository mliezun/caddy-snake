-- The platform's own records. Each app's data lives in its own database, not here.
--
-- Text columns are as narrow as what they hold (see AGENTS.md); the Go code
-- enforces the same limits first, so people get a plain message, not a
-- database error. `text` is kept for the few values that can be large.

CREATE TABLE caddysnake_apps.apps (
	slug         varchar(52) PRIMARY KEY,           -- serving slug: "inventory" (30 at most) or "inventory--new-form" (plus "--" and 20)
	parent       varchar(30),                       -- app slug for a draft, NULL for a live app
	name         varchar(100) NOT NULL,
	description  varchar(500) NOT NULL DEFAULT '',
	database_url varchar(500) NOT NULL,
	live_version varchar(40) NOT NULL DEFAULT '',   -- '' until first published
	created_by   varchar(40) NOT NULL DEFAULT '',
	created_at   timestamptz NOT NULL DEFAULT now(),
	published_at timestamptz,
	last_used_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE caddysnake_apps.settings (
	slug  varchar(52) NOT NULL,
	key   varchar(100) NOT NULL,
	value text NOT NULL,                            -- may be a certificate or a JSON key file
	PRIMARY KEY (slug, key)
);

CREATE TABLE caddysnake_apps.publishes (
	id           bigserial PRIMARY KEY,
	slug         varchar(52) NOT NULL,
	version      varchar(40) NOT NULL,
	published_by varchar(40) NOT NULL DEFAULT '',
	published_at timestamptz NOT NULL DEFAULT now(),
	ok           boolean NOT NULL,
	detail       text NOT NULL DEFAULT ''           -- the steps, or a traceback
);

CREATE TABLE caddysnake_apps.users (
	username      varchar(40) PRIMARY KEY,
	password_hash varchar(200) NOT NULL,            -- Argon2id, encoded
	is_admin      boolean NOT NULL DEFAULT false,
	created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE caddysnake_apps.sessions (
	token_hash char(64) PRIMARY KEY,                -- SHA-256, hex
	username   varchar(40) NOT NULL REFERENCES caddysnake_apps.users (username) ON DELETE CASCADE,
	expires_at timestamptz NOT NULL
);

CREATE TABLE caddysnake_apps.access_keys (
	token_hash   char(64) PRIMARY KEY,              -- SHA-256, hex
	username     varchar(40) NOT NULL REFERENCES caddysnake_apps.users (username) ON DELETE CASCADE,
	name         varchar(60) NOT NULL DEFAULT '',
	created_at   timestamptz NOT NULL DEFAULT now(),
	last_used_at timestamptz
);
