-- name: CreateServerBan :one
INSERT INTO server_bans (id, server_id, name, ip, reason, banned_by)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: ListServerBans :many
SELECT * FROM server_bans WHERE server_id = $1 ORDER BY created_at DESC;

-- name: GetServerBan :one
SELECT * FROM server_bans WHERE id = $1 AND server_id = $2;

-- name: DeleteServerBan :exec
DELETE FROM server_bans WHERE id = $1 AND server_id = $2;
