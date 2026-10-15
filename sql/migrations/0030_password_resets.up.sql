-- One live reset token per user: the PK on user_id means a fresh request
-- overwrites the previous link, the same way email_confirmations rotates.
CREATE TABLE password_resets (
    user_id     UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    token_hash  BYTEA NOT NULL UNIQUE,
    expires_at  TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
