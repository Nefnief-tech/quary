-- name: UpsertRunnerVersion :exec
INSERT INTO runner_versions (runner, mc_version, stable, java_version, published_at, refreshed_at)
VALUES ($1, $2, $3, $4, $5, now())
ON CONFLICT (runner, mc_version) DO UPDATE SET
    stable = EXCLUDED.stable,
    java_version = EXCLUDED.java_version,
    published_at = EXCLUDED.published_at,
    refreshed_at = now();

-- name: ListRunnerVersions :many
SELECT * FROM runner_versions WHERE runner = $1;

-- name: GetRunnerVersion :one
SELECT * FROM runner_versions WHERE runner = $1 AND mc_version = $2;

-- name: FreshRunnerVersionCount :one
SELECT count(*) FROM runner_versions
WHERE runner = $1 AND refreshed_at > now() - interval '24 hours';
