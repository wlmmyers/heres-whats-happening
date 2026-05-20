CREATE TABLE user_interests (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id          UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind             TEXT NOT NULL CHECK (kind IN ('spotify_top_artist', 'spotify_top_genre', 'manual_tag')),
    value            TEXT NOT NULL,
    normalized_value TEXT NOT NULL,
    weight           DOUBLE PRECISION NOT NULL DEFAULT 1.0,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (user_id, kind, normalized_value)
);

CREATE INDEX user_interests_user_kind ON user_interests (user_id, kind);
