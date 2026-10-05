-- name: CreateAPIKey :one
INSERT INTO api_keys (id, user_id, name, prefix, secret_hash, scopes, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: GetAPIKeyByPrefix :one
SELECT * FROM api_keys WHERE prefix = $1;

-- name: ListAPIKeysByUser :many
SELECT id, user_id, name, prefix, scopes, created_at, last_used_at, expires_at
FROM api_keys WHERE user_id = $1 ORDER BY created_at;

-- name: DeleteAPIKey :exec
DELETE FROM api_keys WHERE id = $1 AND user_id = $2;

-- name: TouchAPIKey :exec
UPDATE api_keys SET last_used_at = now() WHERE id = $1;
