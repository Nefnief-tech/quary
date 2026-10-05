-- name: InsertAuditEntry :exec
INSERT INTO audit_log (user_id, actor, action, resource_type, resource_id, ip, metadata)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: ListAuditEntries :many
SELECT * FROM audit_log ORDER BY id DESC LIMIT $1 OFFSET $2;
