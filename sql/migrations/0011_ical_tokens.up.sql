CREATE TABLE ical_tokens (
    user_id           UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    token_hash        BYTEA NOT NULL UNIQUE,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_accessed_at  TIMESTAMPTZ
);
