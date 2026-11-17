-- Times are whole seconds since 1970 (UTC), passed in by the Go code.

-- name: CreateUser :exec
INSERT INTO users (username, password_hash, is_admin, created_at)
VALUES (sqlc.arg(username), sqlc.arg(password_hash), sqlc.arg(is_admin), sqlc.arg(now));

-- name: GetUser :one
SELECT * FROM users WHERE username = sqlc.arg(username);

-- name: ListUsers :many
SELECT username, is_admin, created_at FROM users ORDER BY username;

-- name: CountUsers :one
SELECT count(*) FROM users;

-- name: SetUserPassword :execrows
UPDATE users SET password_hash = sqlc.arg(password_hash) WHERE username = sqlc.arg(username);

-- name: DeleteUser :execrows
DELETE FROM users WHERE username = sqlc.arg(username);

-- name: CreateSession :exec
INSERT INTO sessions (token_hash, username, expires_at)
VALUES (sqlc.arg(token_hash), sqlc.arg(username), sqlc.arg(expires_at));

-- name: GetSessionUser :one
SELECT u.username, u.is_admin
FROM sessions AS s
JOIN users AS u ON u.username = s.username
WHERE s.token_hash = sqlc.arg(token_hash) AND s.expires_at > sqlc.arg(now);

-- name: DeleteSession :exec
DELETE FROM sessions WHERE token_hash = sqlc.arg(token_hash);

-- name: DeleteUserSessions :exec
DELETE FROM sessions WHERE username = sqlc.arg(username);

-- name: CountAdmins :one
SELECT count(*) FROM users WHERE is_admin;

-- name: DeleteExpiredSessions :exec
DELETE FROM sessions WHERE expires_at <= sqlc.arg(now);

-- name: CreateAccessKey :exec
INSERT INTO access_keys (token_hash, username, name, created_at)
VALUES (sqlc.arg(token_hash), sqlc.arg(username), sqlc.arg(name), sqlc.arg(now));

-- name: GetAccessKeyUser :one
SELECT u.username, u.is_admin
FROM access_keys AS k
JOIN users AS u ON u.username = k.username
WHERE k.token_hash = sqlc.arg(token_hash);

-- name: TouchAccessKey :exec
UPDATE access_keys SET last_used_at = sqlc.arg(now) WHERE token_hash = sqlc.arg(token_hash);

-- name: ListAccessKeys :many
SELECT name, created_at, last_used_at FROM access_keys WHERE username = sqlc.arg(username) ORDER BY created_at;

-- name: DeleteAccessKeysByName :execrows
DELETE FROM access_keys WHERE username = sqlc.arg(username) AND name = sqlc.arg(name);

-- name: DeleteUserAccessKeys :exec
DELETE FROM access_keys WHERE username = sqlc.arg(username);
