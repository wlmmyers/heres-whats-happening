CREATE TABLE event_sources (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name          TEXT NOT NULL UNIQUE,
    adapter_kind  TEXT NOT NULL,
    config        JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO event_sources (name, adapter_kind, config)
VALUES ('ticketmaster', 'ticketmaster_api', '{}'::jsonb);

CREATE TABLE venues (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    city_id         UUID NOT NULL REFERENCES cities(id),
    name            TEXT NOT NULL,
    normalized_name TEXT NOT NULL,
    address         TEXT,
    lat             DOUBLE PRECISION,
    lng             DOUBLE PRECISION,
    website_url     TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (city_id, normalized_name)
);

CREATE TABLE genres (
    slug   TEXT PRIMARY KEY,
    label  TEXT NOT NULL
);

INSERT INTO genres (slug, label) VALUES
    ('rock',         'Rock'),
    ('pop',          'Pop'),
    ('hip-hop',      'Hip-Hop'),
    ('electronic',   'Electronic'),
    ('jazz',         'Jazz'),
    ('classical',    'Classical'),
    ('folk',         'Folk'),
    ('country',      'Country'),
    ('metal',        'Metal'),
    ('indie',        'Indie'),
    ('rnb',          'R&B'),
    ('latin',        'Latin'),
    ('world',        'World'),
    ('blues',        'Blues'),
    ('reggae',       'Reggae'),
    ('theater',      'Theater'),
    ('musical',      'Musical'),
    ('comedy',       'Comedy'),
    ('dance',        'Dance'),
    ('opera',        'Opera'),
    ('film',         'Film'),
    ('sports',       'Sports'),
    ('food',         'Food'),
    ('art',          'Art'),
    ('family',       'Family'),
    ('other',        'Other');
