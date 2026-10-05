-- name: CountUsers :one
SELECT count(*) FROM users;

-- name: CreateUser :one
INSERT INTO users (id, username, email, password_hash, role_id)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetUser :one
SELECT * FROM users WHERE id = $1;

-- name: GetUserByUsername :one
SELECT * FROM users WHERE lower(username) = lower($1);

-- name: ListUsers :many
SELECT * FROM users ORDER BY created_at LIMIT $1 OFFSET $2;

-- name: SetUserDisabled :exec
UPDATE users SET disabled = $2, updated_at = now() WHERE id = $1;

-- name: RoleOfUser :one
SELECT r.* FROM roles r
JOIN users u ON u.role_id = r.id
WHERE u.id = $1;
