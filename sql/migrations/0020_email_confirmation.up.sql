-- Two statements, deliberately. The first backfills every existing user as
-- confirmed; the second makes false the default for rows created from here on.
-- Collapsing them into a single ADD COLUMN ... DEFAULT FALSE would lock the
-- entire existing user base out of the product on deploy.
ALTER TABLE users ADD COLUMN confirmed BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE users ALTER COLUMN confirmed SET DEFAULT FALSE;

-- One live confirmation token per user: the PK on user_id means a resend
-- overwrites the previous link, the same way ical_tokens rotates.
CREATE TABLE email_confirmations (
    user_id     UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    token_hash  BYTEA NOT NULL UNIQUE,
    expires_at  TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
