-- name: UpsertPasswordReset :exec
INSERT INTO password_resets (user_id, token_hash, expires_at)
VALUES ($1, $2, $3)
ON CONFLICT (user_id) DO UPDATE SET
    token_hash  = EXCLUDED.token_hash,
    expires_at  = EXCLUDED.expires_at,
    consumed_at = NULL,
    created_at  = NOW();

-- name: ResetPasswordWithToken :one
-- Claims the token and applies the reset in ONE statement, so it is atomic
-- without a transaction: two concurrent submits of the same link cannot both
-- win, and a failure part-way cannot burn the token without changing the
-- password. Data-modifying CTEs always run to completion, so `revoked` signs
-- the user out everywhere even though nothing selects from it.
--
-- Setting confirmed is deliberate: opening the emailed link proves ownership
-- of the address just as the confirmation link does.
--
-- No row means the token is unknown, expired, already used, or belongs to a
-- deleted account; callers cannot tell which, and do not need to.
WITH claimed AS (
    UPDATE password_resets pr
    SET consumed_at = NOW()
    WHERE pr.token_hash = $1
      AND pr.consumed_at IS NULL
      AND pr.expires_at > NOW()
    RETURNING pr.user_id
), revoked AS (
    UPDATE refresh_tokens rt
    SET revoked_at = NOW()
    FROM claimed
    WHERE rt.user_id = claimed.user_id
      AND rt.revoked_at IS NULL
)
UPDATE users u
SET password_hash = $2, confirmed = TRUE
FROM claimed
WHERE u.id = claimed.user_id
  AND u.deleted_at IS NULL
RETURNING u.id, u.email;
