-- +goose Up
-- Cached upstream version catalogs per runner. Artifact URLs are resolved
-- live at server creation; this table is the fast, filterable version list.
CREATE TABLE runner_versions (
    runner text NOT NULL,
    mc_version text NOT NULL,
    stable boolean NOT NULL DEFAULT true,
    java_version integer NOT NULL DEFAULT 0,
    published_at timestamptz,
    refreshed_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (runner, mc_version)
);

-- +goose Down
DROP TABLE runner_versions;
