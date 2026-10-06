-- +goose Up
-- Generic key/value settings store. Used for tunnel provider configuration
-- (frps address, token — token value is sealed by the panel, not here).
CREATE TABLE settings (
    key text PRIMARY KEY,
    value jsonb NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE settings;
