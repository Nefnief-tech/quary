-- name: CreateNode :one
INSERT INTO nodes (id, name, description)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetNode :one
SELECT * FROM nodes WHERE id = $1;

-- name: ListNodes :many
SELECT * FROM nodes ORDER BY created_at;

-- name: DeleteNode :exec
DELETE FROM nodes WHERE id = $1;

-- name: TouchNodeHeartbeat :exec
UPDATE nodes SET
    last_seen_at = now(),
    daemon_version = $2,
    hostname = $3,
    docker_version = $4,
    os = $5,
    arch = $6,
    updated_at = now()
WHERE id = $1;
