-- name: CreateUser :exec
INSERT INTO caddysnake_apps.users (username, password_hash, is_admin) VALUES ($1, $2, $3);

-- name: GetUser :one
SELECT * FROM caddysnake_apps.users WHERE username = $1;

-- name: ListUsers :many
SELECT username, is_admin, created_at FROM caddysnake_apps.users ORDER BY username;

-- name: CountUsers :one
SELECT count(*) FROM caddysnake_apps.users;

-- name: SetUserPassword :execrows
UPDATE caddysnake_apps.users SET password_hash = $2 WHERE username = $1;

-- name: DeleteUser :execrows
DELETE FROM caddysnake_apps.users WHERE username = $1;

-- name: CreateSession :exec
INSERT INTO caddysnake_apps.sessions (token_hash, username, expires_at) VALUES ($1, $2, $3);

-- name: GetSessionUser :one
SELECT u.username, u.is_admin
FROM caddysnake_apps.sessions AS s
JOIN caddysnake_apps.users AS u ON u.username = s.username
WHERE s.token_hash = $1 AND s.expires_at > now();

-- name: DeleteSession :exec
DELETE FROM caddysnake_apps.sessions WHERE token_hash = $1;

-- name: DeleteUserSessions :exec
DELETE FROM caddysnake_apps.sessions WHERE username = $1;

-- name: CountAdmins :one
SELECT count(*) FROM caddysnake_apps.users WHERE is_admin;

-- name: DeleteExpiredSessions :exec
DELETE FROM caddysnake_apps.sessions WHERE expires_at <= now();

-- name: CreateAccessKey :exec
INSERT INTO caddysnake_apps.access_keys (token_hash, username, name) VALUES ($1, $2, $3);

-- name: GetAccessKeyUser :one
SELECT u.username, u.is_admin
FROM caddysnake_apps.access_keys AS k
JOIN caddysnake_apps.users AS u ON u.username = k.username
WHERE k.token_hash = $1;

-- name: TouchAccessKey :exec
UPDATE caddysnake_apps.access_keys SET last_used_at = now() WHERE token_hash = $1;

-- name: ListAccessKeys :many
SELECT name, created_at, last_used_at FROM caddysnake_apps.access_keys WHERE username = $1 ORDER BY created_at;

-- name: DeleteAccessKeysByName :execrows
DELETE FROM caddysnake_apps.access_keys WHERE username = $1 AND name = $2;
