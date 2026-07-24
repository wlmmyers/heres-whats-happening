CREATE TABLE rate_limit_events (
    id         BIGSERIAL PRIMARY KEY,
    bucket     TEXT        NOT NULL,  -- namespaces the limit, e.g. 'signup'
    key        TEXT        NOT NULL,  -- client IP
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Serves both the windowed count and the oldest-in-window lookup.
CREATE INDEX rate_limit_events_lookup
    ON rate_limit_events (bucket, key, created_at DESC);
