CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE cities (
    id        UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    slug      TEXT NOT NULL UNIQUE,
    name      TEXT NOT NULL,
    timezone  TEXT NOT NULL
);

INSERT INTO cities (slug, name, timezone)
VALUES ('v1-city', 'V1 City', 'America/New_York');
