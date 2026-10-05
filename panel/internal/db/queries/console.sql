-- name: InsertConsoleLine :exec
INSERT INTO console_lines (server_id, at, line)
VALUES ($1, $2, $3);

-- name: RecentConsoleLines :many
SELECT at, line FROM console_lines
WHERE server_id = $1
ORDER BY at DESC
LIMIT $2;

-- Keep the newest 500 lines per server.
-- name: PruneConsoleLines :execrows
DELETE FROM console_lines c
USING (
    SELECT id, row_number() OVER (PARTITION BY server_id ORDER BY at DESC, id DESC) AS rn
    FROM console_lines
) ranked
WHERE c.id = ranked.id AND ranked.rn > 500;
