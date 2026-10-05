-- +goose Up
-- Phase 1: regular users manage their own servers (create, power, console,
-- files). Subuser-scoped per-server permissions arrive in Phase 2.
INSERT INTO role_permissions (role_id, permission)
SELECT r.id, p.name FROM roles r, permissions p
WHERE r.name = 'user'
  AND p.name IN (
    'server.create', 'server.power',
    'console.read', 'console.write',
    'file.read', 'file.write'
  );

-- +goose Down
DELETE FROM role_permissions
WHERE permission IN (
    'server.create', 'server.power',
    'console.read', 'console.write',
    'file.read', 'file.write'
  )
  AND role_id = (SELECT id FROM roles WHERE name = 'user');
