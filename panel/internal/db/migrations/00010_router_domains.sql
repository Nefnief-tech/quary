-- +goose Up
-- Handshake-router domains: which hostname (as typed by the player) routes
-- to which server on the node's standard port.
CREATE TABLE router_domains (
    id uuid PRIMARY KEY,
    server_id uuid NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    domain text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (domain)
);

CREATE INDEX router_domains_server_id_idx ON router_domains (server_id);

-- +goose Down
DROP TABLE router_domains;
