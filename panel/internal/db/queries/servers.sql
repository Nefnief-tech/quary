-- name: CreateServer :one
INSERT INTO servers (id, name, owner_id, node_id, allocation_id, runner, mc_version,
                     status, memory_mb, disk_mb, cpu_percent, config)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
RETURNING *;

-- name: GetServer :one
SELECT * FROM servers WHERE id = $1;

-- name: ListServers :many
SELECT * FROM servers ORDER BY created_at;

-- name: ListServersByOwner :many
SELECT * FROM servers WHERE owner_id = $1 ORDER BY created_at;

-- name: SetServerStatus :exec
UPDATE servers SET status = $2, updated_at = now() WHERE id = $1;

-- name: RenameServer :exec
UPDATE servers SET name = $2, updated_at = now() WHERE id = $1;

-- name: SetServerResources :exec
UPDATE servers SET memory_mb = $2, cpu_percent = $3, updated_at = now() WHERE id = $1;

-- name: SetServerAllocation :exec
UPDATE servers SET allocation_id = $2, updated_at = now() WHERE id = $1;

-- name: UpdateServerConfig :exec
UPDATE servers SET config = $2, updated_at = now() WHERE id = $1;

-- name: DeleteServer :exec
DELETE FROM servers WHERE id = $1;
