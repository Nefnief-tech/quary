-- name: CreateAllocation :one
INSERT INTO allocations (id, node_id, ip, port)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: ListAllocationsByNode :many
SELECT a.id, a.node_id, a.ip, a.port, a.created_at, s.id AS server_id
FROM allocations a
LEFT JOIN servers s ON s.allocation_id = a.id
WHERE a.node_id = $1
ORDER BY a.ip, a.port;

-- name: PickFreeAllocation :one
SELECT a.* FROM allocations a
LEFT JOIN servers s ON s.allocation_id = a.id OR s.map_allocation_id = a.id
WHERE a.node_id = $1 AND s.id IS NULL
ORDER BY a.created_at
LIMIT 1
FOR UPDATE OF a SKIP LOCKED;

-- Same, but skips an allocation this transaction already row-locked
-- (SKIP LOCKED does not exclude own-transaction locks). Used to claim a
-- second, distinct port for the map webserver during create.
-- name: PickFreeAllocationExcluding :one
SELECT a.* FROM allocations a
LEFT JOIN servers s ON s.allocation_id = a.id OR s.map_allocation_id = a.id
WHERE a.node_id = $1 AND a.id != $2 AND s.id IS NULL
ORDER BY a.created_at
LIMIT 1
FOR UPDATE OF a SKIP LOCKED;

-- name: DeleteAllocation :exec
DELETE FROM allocations WHERE id = $1;

-- name: GetAllocation :one
SELECT * FROM allocations WHERE id = $1;

-- name: GetAllocationForServer :one
SELECT a.* FROM allocations a
JOIN servers s ON s.allocation_id = a.id
WHERE s.id = $1;

-- Row-lock an allocation for a settings-tab port move.
-- name: GetAllocationForUpdate :one
SELECT * FROM allocations WHERE id = $1 FOR UPDATE;

-- Free unless claimed by a server other than the caller's own.
-- name: AllocationServerCount :one
SELECT count(*) FROM servers
WHERE (allocation_id = $1 OR map_allocation_id = $1) AND id != $2;
