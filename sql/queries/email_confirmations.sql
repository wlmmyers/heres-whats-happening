-- name: UpsertEmailConfirmation :exec
INSERT INTO email_confirmations (user_id, token_hash, expires_at)
VALUES ($1, $2, $3)
ON CONFLICT (user_id) DO UPDATE SET
    token_hash  = EXCLUDED.token_hash,
    expires_at  = EXCLUDED.expires_at,
    consumed_at = NULL,
    created_at  = NOW();

-- name: GetEmailConfirmationByHash :one
SELECT user_id, token_hash, expires_at, consumed_at, created_at
FROM email_confirmations
WHERE token_hash = $1;

-- name: ConsumeEmailConfirmation :exec
UPDATE email_confirmations
SET consumed_at = NOW()
WHERE user_id = $1;
