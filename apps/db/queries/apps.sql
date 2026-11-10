-- name: GetApp :one
SELECT * FROM caddysnake_apps.apps WHERE slug = $1;

-- name: ListAllApps :many
SELECT * FROM caddysnake_apps.apps ORDER BY slug;

-- name: ListLiveApps :many
SELECT * FROM caddysnake_apps.apps WHERE parent IS NULL ORDER BY slug;

-- name: ListDrafts :many
SELECT * FROM caddysnake_apps.apps WHERE parent = sqlc.arg(parent)::text ORDER BY slug;

-- name: InsertApp :exec
INSERT INTO caddysnake_apps.apps (slug, parent, name, description, database_url, created_by)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: DeleteApp :exec
DELETE FROM caddysnake_apps.apps WHERE slug = $1;

-- name: SetLiveVersion :exec
UPDATE caddysnake_apps.apps SET live_version = $2, published_at = now() WHERE slug = $1;

-- name: TouchApp :exec
UPDATE caddysnake_apps.apps SET last_used_at = now() WHERE slug = $1;

-- name: RecordPublish :exec
INSERT INTO caddysnake_apps.publishes (slug, version, published_by, ok, detail)
VALUES ($1, $2, $3, $4, $5);

-- name: DeletePublishes :exec
DELETE FROM caddysnake_apps.publishes WHERE slug = $1;

-- name: ListSettings :many
SELECT key, value FROM caddysnake_apps.settings WHERE slug = $1 ORDER BY key;

-- name: UpsertSetting :exec
INSERT INTO caddysnake_apps.settings (slug, key, value) VALUES ($1, $2, $3)
ON CONFLICT (slug, key) DO UPDATE SET value = EXCLUDED.value;

-- name: DeleteSetting :exec
DELETE FROM caddysnake_apps.settings WHERE slug = $1 AND key = $2;

-- name: DeleteSettings :exec
DELETE FROM caddysnake_apps.settings WHERE slug = $1;

-- name: CopySettings :exec
INSERT INTO caddysnake_apps.settings (slug, key, value)
SELECT sqlc.arg(to_slug)::text, s.key, s.value
FROM caddysnake_apps.settings AS s
WHERE s.slug = sqlc.arg(from_slug)::text
ON CONFLICT (slug, key) DO UPDATE SET value = EXCLUDED.value;
