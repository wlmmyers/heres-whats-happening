CREATE TABLE user_event_match (
    user_id          UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    event_id         UUID NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    score            DOUBLE PRECISION NOT NULL,
    score_breakdown  JSONB NOT NULL DEFAULT '{}'::jsonb,
    computed_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, event_id)
);

CREATE INDEX user_event_match_user_score ON user_event_match (user_id, score DESC);
