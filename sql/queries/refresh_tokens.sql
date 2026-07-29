-- name: CreateRefreshToken :one
INSERT INTO refresh_tokens (user_id, token_hash, expires_at)
VALUES ($1, $2, $3)
RETURNING id, user_id, expires_at, created_at;

-- name: GetActiveRefreshTokenByHash :one
-- The JOIN carries `confirmed` so Refresh can mint a token with the claim
-- without a second query. It deliberately does NOT filter on u.deleted_at:
-- that preserves today's behavior exactly. Adding the filter would be a
-- separate (defensible) fix and does not belong bundled here.
SELECT rt.id, rt.user_id, rt.expires_at, rt.revoked_at, rt.created_at, u.confirmed
FROM refresh_tokens rt
JOIN users u ON u.id = rt.user_id
WHERE rt.token_hash = $1
  AND rt.revoked_at IS NULL
  AND rt.expires_at > NOW();

-- name: RevokeRefreshTokenByHash :exec
UPDATE refresh_tokens
SET revoked_at = NOW()
WHERE token_hash = $1 AND revoked_at IS NULL;
