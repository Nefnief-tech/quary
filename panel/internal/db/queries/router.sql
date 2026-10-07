-- name: CreateRouterDomain :one
INSERT INTO router_domains (id, server_id, domain)
VALUES ($1, $2, $3)
RETURNING *;

-- name: ListRouterDomains :many
SELECT d.*, s.name AS server_name, s.node_id, a.port AS local_port
FROM router_domains d
JOIN servers s ON s.id = d.server_id
JOIN allocations a ON a.id = s.allocation_id
ORDER BY d.created_at;

-- name: ListRouterDomainsByServer :many
SELECT d.*, s.name AS server_name, s.node_id, a.port AS local_port
FROM router_domains d
JOIN servers s ON s.id = d.server_id
JOIN allocations a ON a.id = s.allocation_id
WHERE d.server_id = $1
ORDER BY d.created_at;

-- name: DeleteRouterDomain :exec
DELETE FROM router_domains WHERE id = $1 AND server_id = $2;
