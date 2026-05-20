CREATE TABLE events (
    id                       UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    source_id                UUID NOT NULL REFERENCES event_sources(id),
    source_event_id          TEXT NOT NULL,
    title                    TEXT NOT NULL,
    description              TEXT NOT NULL DEFAULT '',
    starts_at                TIMESTAMPTZ NOT NULL,
    ends_at                  TIMESTAMPTZ,
    venue_id                 UUID NOT NULL REFERENCES venues(id),
    image_url                TEXT,
    url                      TEXT,
    embedding                vector(384),
    embedding_updated_at     TIMESTAMPTZ,
    last_seen_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    archived_at              TIMESTAMPTZ,
    created_at               TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at               TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (source_id, source_event_id)
);

CREATE INDEX events_starts_at        ON events (starts_at);
CREATE INDEX events_venue_id         ON events (venue_id);
CREATE INDEX events_archived_at      ON events (archived_at);
CREATE INDEX events_last_seen_at     ON events (last_seen_at);
