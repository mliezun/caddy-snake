-- Times are whole seconds since 1970 (UTC), passed in by the Go code.

-- name: GetApp :one
SELECT * FROM apps WHERE slug = sqlc.arg(slug);

-- name: ListAllApps :many
SELECT * FROM apps ORDER BY slug;

-- name: ListLiveApps :many
SELECT * FROM apps WHERE parent IS NULL ORDER BY slug;

-- name: ListDrafts :many
SELECT * FROM apps WHERE parent = sqlc.arg(parent) ORDER BY slug;

-- name: InsertApp :exec
INSERT INTO apps (slug, parent, name, description, created_by, created_at, last_used_at)
VALUES (sqlc.arg(slug), sqlc.arg(parent), sqlc.arg(name), sqlc.arg(description), sqlc.arg(created_by), sqlc.arg(now), sqlc.arg(now));

-- name: DeleteApp :exec
DELETE FROM apps WHERE slug = sqlc.arg(slug);

-- name: SetLiveVersion :exec
UPDATE apps SET live_version = sqlc.arg(live_version), published_at = sqlc.arg(now) WHERE slug = sqlc.arg(slug);

-- name: TouchApp :exec
UPDATE apps SET last_used_at = sqlc.arg(now) WHERE slug = sqlc.arg(slug);

-- name: RecordPublish :exec
INSERT INTO publishes (slug, version, published_by, published_at, ok, detail)
VALUES (sqlc.arg(slug), sqlc.arg(version), sqlc.arg(published_by), sqlc.arg(now), sqlc.arg(ok), sqlc.arg(detail));

-- name: DeletePublishes :exec
DELETE FROM publishes WHERE slug = sqlc.arg(slug);

-- name: ListSettings :many
SELECT key, value FROM settings WHERE slug = sqlc.arg(slug) ORDER BY key;

-- name: UpsertSetting :exec
INSERT INTO settings (slug, key, value) VALUES (sqlc.arg(slug), sqlc.arg(key), sqlc.arg(value))
ON CONFLICT (slug, key) DO UPDATE SET value = excluded.value;

-- name: DeleteSetting :exec
DELETE FROM settings WHERE slug = sqlc.arg(slug) AND key = sqlc.arg(key);

-- name: DeleteSettings :exec
DELETE FROM settings WHERE slug = sqlc.arg(slug);

-- name: CopySettings :exec
INSERT INTO settings (slug, key, value)
SELECT sqlc.arg(to_slug), s.key, s.value
FROM settings AS s
WHERE s.slug = sqlc.arg(from_slug)
ON CONFLICT (slug, key) DO UPDATE SET value = excluded.value;
