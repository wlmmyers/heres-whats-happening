CREATE TABLE user_event_not_interested (
    user_id    UUID NOT NULL REFERENCES users(id)  ON DELETE CASCADE,
    event_id   UUID NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, event_id)
);
