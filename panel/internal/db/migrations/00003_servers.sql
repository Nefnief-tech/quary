-- +goose Up
-- Skeleton for Phase 0; lifecycle, runners and installs are wired in Phase 1.
CREATE TABLE allocations (
    id uuid PRIMARY KEY,
    node_id uuid NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    ip text NOT NULL,
    port integer NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (node_id, ip, port)
);

CREATE TABLE servers (
    id uuid PRIMARY KEY,
    name text NOT NULL,
    owner_id uuid NOT NULL REFERENCES users(id),
    node_id uuid NOT NULL REFERENCES nodes(id),
    allocation_id uuid NOT NULL UNIQUE REFERENCES allocations(id),
    runner text NOT NULL DEFAULT 'vanilla',
    mc_version text NOT NULL DEFAULT '',
    status text NOT NULL DEFAULT 'offline',
    memory_mb integer NOT NULL DEFAULT 2048,
    disk_mb integer NOT NULL DEFAULT 10240,
    cpu_percent integer NOT NULL DEFAULT 200,
    config jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX servers_owner_id_idx ON servers (owner_id);
CREATE INDEX servers_node_id_idx ON servers (node_id);

-- +goose Down
DROP TABLE servers;
DROP TABLE allocations;
