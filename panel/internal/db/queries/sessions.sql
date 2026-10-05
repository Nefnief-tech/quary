-- name: CreateSession :one
INSERT INTO sessions (id, user_id, token_hash, ip, user_agent, expires_at)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: SessionWithUserByTokenHash :one
SELECT s.id AS session_id, s.expires_at, u.id AS user_id, u.username, u.email,
       u.role_id, u.disabled AS user_disabled, r.name AS role_name
FROM sessions s
JOIN users u ON u.id = s.user_id
JOIN roles r ON r.id = u.role_id
WHERE s.token_hash = $1 AND s.expires_at > now();

-- name: DeleteSession :exec
DELETE FROM sessions WHERE id = $1;

-- name: DeleteExpiredSessions :exec
DELETE FROM sessions WHERE expires_at < now();
