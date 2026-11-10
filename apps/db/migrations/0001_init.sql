-- The platform's own records. Each app's data lives in its own database, not here.

CREATE TABLE caddysnake_apps.apps (
	slug         text PRIMARY KEY,           -- serving slug: "inventory" or "inventory--new-form"
	parent       text,                       -- app slug for a draft, NULL for a live app
	name         text NOT NULL,
	description  text NOT NULL DEFAULT '',
	database_url text NOT NULL,
	live_version text NOT NULL DEFAULT '',   -- '' until first published
	created_by   text NOT NULL DEFAULT '',
	created_at   timestamptz NOT NULL DEFAULT now(),
	published_at timestamptz,
	last_used_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE caddysnake_apps.settings (
	slug  text NOT NULL,
	key   text NOT NULL,
	value text NOT NULL,
	PRIMARY KEY (slug, key)
);

CREATE TABLE caddysnake_apps.publishes (
	id           bigserial PRIMARY KEY,
	slug         text NOT NULL,
	version      text NOT NULL,
	published_by text NOT NULL DEFAULT '',
	published_at timestamptz NOT NULL DEFAULT now(),
	ok           boolean NOT NULL,
	detail       text NOT NULL DEFAULT ''
);

CREATE TABLE caddysnake_apps.users (
	username      text PRIMARY KEY,
	password_hash text NOT NULL,
	is_admin      boolean NOT NULL DEFAULT false,
	created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE caddysnake_apps.sessions (
	token_hash text PRIMARY KEY,
	username   text NOT NULL REFERENCES caddysnake_apps.users (username) ON DELETE CASCADE,
	expires_at timestamptz NOT NULL
);

CREATE TABLE caddysnake_apps.access_keys (
	token_hash   text PRIMARY KEY,
	username     text NOT NULL REFERENCES caddysnake_apps.users (username) ON DELETE CASCADE,
	name         text NOT NULL DEFAULT '',
	created_at   timestamptz NOT NULL DEFAULT now(),
	last_used_at timestamptz
);
