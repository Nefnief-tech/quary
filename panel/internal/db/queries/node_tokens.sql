-- name: CreateNodeToken :one
INSERT INTO node_tokens (id, node_id, token_hash, expires_at)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- Row-lock a valid token so enrollment is atomic even if two daemons race.
-- name: GetNodeTokenForUpdate :one
SELECT * FROM node_tokens
WHERE token_hash = $1
  AND used_at IS NULL AND revoked_at IS NULL AND expires_at > now()
FOR UPDATE;

-- name: MarkNodeTokenUsed :exec
UPDATE node_tokens SET used_at = now() WHERE id = $1;

-- name: RevokeNodeTokens :exec
UPDATE node_tokens SET revoked_at = now()
WHERE node_id = $1 AND used_at IS NULL AND revoked_at IS NULL;

-- name: NodeTokenExists :one
SELECT count(*) FROM node_tokens
WHERE node_id = $1 AND used_at IS NULL AND revoked_at IS NULL AND expires_at > now();
