-- name: ListRoles :many
SELECT * FROM roles ORDER BY name;

-- name: GetRoleByName :one
SELECT * FROM roles WHERE name = $1;

-- name: PermissionsForRole :many
SELECT p.name FROM permissions p
JOIN role_permissions rp ON rp.permission = p.name
WHERE rp.role_id = $1;
