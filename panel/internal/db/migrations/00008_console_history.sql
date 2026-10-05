-- +goose Up
-- Persistent console history: survives panel restarts and is replayed when
-- the in-memory scrollback is empty. Pruned to the newest lines per server.
CREATE TABLE console_lines (
    id bigserial PRIMARY KEY,
    server_id uuid NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    at timestamptz NOT NULL DEFAULT now(),
    line text NOT NULL
);

CREATE INDEX console_lines_server_at_idx ON console_lines (server_id, at DESC);

-- +goose Down
DROP TABLE console_lines;
