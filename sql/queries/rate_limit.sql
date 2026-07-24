-- name: CountRateLimitEvents :one
SELECT COUNT(*)
FROM rate_limit_events
WHERE bucket = sqlc.arg(bucket)
  AND key = sqlc.arg(key)
  AND created_at > sqlc.arg(since);

-- name: OldestRateLimitEvent :one
SELECT created_at
FROM rate_limit_events
WHERE bucket = sqlc.arg(bucket)
  AND key = sqlc.arg(key)
  AND created_at > sqlc.arg(since)
ORDER BY created_at ASC
LIMIT 1;

-- name: InsertRateLimitEvent :exec
INSERT INTO rate_limit_events (bucket, key, created_at)
VALUES (sqlc.arg(bucket), sqlc.arg(key), sqlc.arg(created_at));

-- name: DeleteRateLimitEventsBefore :exec
DELETE FROM rate_limit_events
WHERE created_at < sqlc.arg(before);
