-- +goose Up
-- Browser-reachable host for ports a node publishes (map webserver); the
-- daemon reports it from config or falls back to its hostname.
ALTER TABLE nodes ADD COLUMN public_host text NOT NULL DEFAULT '';

-- Second allocation for servers that run an embedded map webserver
-- (BlueMap); nullable because vanilla runners have no map.
ALTER TABLE servers
    ADD COLUMN map_allocation_id uuid REFERENCES allocations(id) ON DELETE SET NULL;

-- Bans issued through the panel. The authoritative list stays in the
-- server itself; this table mirrors panel-issued bans so the UI can list
-- and lift them without parsing console output.
CREATE TABLE server_bans (
    id uuid PRIMARY KEY,
    server_id uuid NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    name text NOT NULL DEFAULT '',
    ip text NOT NULL DEFAULT '',
    reason text NOT NULL DEFAULT '',
    banned_by uuid REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK (name <> '' OR ip <> '')
);

CREATE INDEX server_bans_server_id_idx ON server_bans (server_id);

-- +goose Down
DROP TABLE server_bans;
ALTER TABLE servers DROP COLUMN map_allocation_id;
ALTER TABLE nodes DROP COLUMN public_host;
