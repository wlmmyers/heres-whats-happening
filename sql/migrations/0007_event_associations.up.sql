CREATE TABLE event_performers (
    event_id         UUID NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    performer_name   TEXT NOT NULL,
    normalized_name  TEXT NOT NULL,
    PRIMARY KEY (event_id, normalized_name)
);

CREATE INDEX event_performers_normalized_name ON event_performers (normalized_name);

CREATE TABLE event_genres (
    event_id   UUID NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    genre_slug TEXT NOT NULL REFERENCES genres(slug),
    PRIMARY KEY (event_id, genre_slug)
);
