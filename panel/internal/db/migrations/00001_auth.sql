-- +goose Up
CREATE TABLE roles (
    id uuid PRIMARY KEY,
    name text NOT NULL UNIQUE,
    description text NOT NULL DEFAULT '',
    is_system boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE permissions (
    name text PRIMARY KEY
);

CREATE TABLE role_permissions (
    role_id uuid NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    permission text NOT NULL REFERENCES permissions(name) ON DELETE CASCADE,
    PRIMARY KEY (role_id, permission)
);

INSERT INTO roles (id, name, description, is_system) VALUES
    (gen_random_uuid(), 'admin', 'Full access to the panel and all servers.', true),
    (gen_random_uuid(), 'user', 'Regular user; manages their own servers.', true);

-- '*' is the wildcard permission held by the admin role; the rest are the
-- known permission catalog (RBAC checks are exact-match or wildcard).
INSERT INTO permissions (name) VALUES
    ('*'),
    ('user.read'),
    ('user.create'),
    ('user.disable'),
    ('node.read'),
    ('node.create'),
    ('node.delete'),
    ('audit.read'),
    ('role.read'),
    ('server.create'),
    ('server.delete'),
    ('server.update'),
    ('server.power'),
    ('console.read'),
    ('console.write'),
    ('file.read'),
    ('file.write'),
    ('plugin.install'),
    ('backup.create'),
    ('backup.restore'),
    ('schedule.manage'),
    ('subuser.manage');

INSERT INTO role_permissions (role_id, permission)
SELECT r.id, p.name FROM roles r, permissions p WHERE r.name = 'admin';

CREATE TABLE users (
    id uuid PRIMARY KEY,
    username text NOT NULL,
    email text UNIQUE,
    password_hash text NOT NULL,
    role_id uuid NOT NULL REFERENCES roles(id),
    disabled boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX users_username_lower_idx ON users (lower(username));

CREATE TABLE sessions (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash text NOT NULL UNIQUE,
    ip text NOT NULL DEFAULT '',
    user_agent text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL
);

CREATE INDEX sessions_user_id_idx ON sessions (user_id);
CREATE INDEX sessions_expires_at_idx ON sessions (expires_at);

CREATE TABLE api_keys (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name text NOT NULL,
    prefix text NOT NULL UNIQUE,
    secret_hash text NOT NULL,
    scopes text[] NOT NULL DEFAULT '{}',
    created_at timestamptz NOT NULL DEFAULT now(),
    last_used_at timestamptz,
    expires_at timestamptz
);

CREATE INDEX api_keys_user_id_idx ON api_keys (user_id);

-- +goose Down
DROP TABLE api_keys;
DROP TABLE sessions;
DROP TABLE users;
DROP TABLE role_permissions;
DROP TABLE permissions;
DROP TABLE roles;
