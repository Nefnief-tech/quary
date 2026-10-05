-- +goose Up
CREATE TABLE nodes (
    id uuid PRIMARY KEY,
    name text NOT NULL UNIQUE,
    description text NOT NULL DEFAULT '',
    daemon_version text NOT NULL DEFAULT '',
    hostname text NOT NULL DEFAULT '',
    docker_version text NOT NULL DEFAULT '',
    os text NOT NULL DEFAULT '',
    arch text NOT NULL DEFAULT '',
    last_seen_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- Single-use enrollment tokens; stored as SHA-256 hashes.
CREATE TABLE node_tokens (
    id uuid PRIMARY KEY,
    node_id uuid NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    token_hash text NOT NULL UNIQUE,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    used_at timestamptz,
    revoked_at timestamptz
);

CREATE INDEX node_tokens_node_id_idx ON node_tokens (node_id);

-- +goose Down
DROP TABLE node_tokens;
DROP TABLE nodes;
