-- name: UpsertIcalToken :exec
INSERT INTO ical_tokens (user_id, token_hash)
VALUES ($1, $2)
ON CONFLICT (user_id) DO UPDATE SET
    token_hash       = EXCLUDED.token_hash,
    created_at       = NOW(),
    last_accessed_at = NULL;

-- name: GetIcalTokenByHash :one
SELECT user_id, token_hash, created_at, last_accessed_at
FROM ical_tokens
WHERE token_hash = $1;

-- name: DeleteIcalTokenByUser :exec
DELETE FROM ical_tokens WHERE user_id = $1;

-- name: UpdateIcalTokenLastAccessed :exec
UPDATE ical_tokens SET last_accessed_at = NOW() WHERE user_id = $1;
