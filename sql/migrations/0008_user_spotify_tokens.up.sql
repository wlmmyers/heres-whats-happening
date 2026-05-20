CREATE TABLE user_spotify_tokens (
    user_id            UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    access_token_enc   BYTEA NOT NULL,
    refresh_token_enc  BYTEA NOT NULL,
    expires_at         TIMESTAMPTZ NOT NULL,
    scope              TEXT NOT NULL,
    last_synced_at     TIMESTAMPTZ,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
