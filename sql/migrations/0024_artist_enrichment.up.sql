-- Artist-scoped enrichment. One band playing five dates is one bio, one photo,
-- one setlist — so all of this hangs off the artist, not the event.
--
-- name_key joins the key space event_performers.normalized_name and
-- artist_genre_cache.name_key already use: events.NormalizeString(performer).
-- No link table is needed.
CREATE TABLE artists (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name_key       TEXT NOT NULL UNIQUE,
    display_name   TEXT NOT NULL,
    mbid           TEXT,
    disambiguation TEXT,
    artist_type    TEXT,
    country        TEXT,
    begin_year     TEXT,
    -- 'ok'/'not_found' rather than the enrichment tables' three-state status:
    -- this column describes MusicBrainz resolution and deliberately mirrors
    -- artist_genre_cache.status, which answers the same question.
    status         TEXT NOT NULL CHECK (status IN ('ok','not_found')),
    resolved_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- The three-state status is what makes "succeed, fail, or exhaust max attempts"
-- persistable. 'none' means we looked and there is genuinely nothing (retry
-- rarely); 'error' means the attempt broke (retry soon). TTLs live in the
-- Lambda and mirror internal/scraper/spotify/genres.go:17-19.
CREATE TABLE artist_images (
    artist_id   UUID PRIMARY KEY REFERENCES artists(id) ON DELETE CASCADE,
    status      TEXT NOT NULL CHECK (status IN ('ok','none','error')),
    url         TEXT,
    width       INT,
    height      INT,
    file        TEXT,
    source      TEXT,
    credit      JSONB,
    reason      TEXT,
    checked_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE artist_bios (
    artist_id     UUID PRIMARY KEY REFERENCES artists(id) ON DELETE CASCADE,
    status        TEXT NOT NULL CHECK (status IN ('ok','none','error')),
    bio_md        TEXT,
    sources       JSONB NOT NULL DEFAULT '[]'::jsonb,
    model         TEXT,
    reason        TEXT,
    generated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Setlist and blurb share a table: one workflow produces both and they have
-- identical lifetimes. A NULL blurb with status='ok' means the setlist landed
-- but the blurb call did not.
CREATE TABLE artist_tour_snapshots (
    artist_id      UUID PRIMARY KEY REFERENCES artists(id) ON DELETE CASCADE,
    status         TEXT NOT NULL CHECK (status IN ('ok','none','error')),
    tour_name      TEXT,
    songs          JSONB NOT NULL DEFAULT '[]'::jsonb,
    observed_date  DATE,
    observed_venue TEXT,
    observed_city  TEXT,
    setlist_url    TEXT,
    blurb          TEXT,
    blurb_model    TEXT,
    reason         TEXT,
    fetched_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Which performer the Lambda actually enriched. event_performers has no
-- ordering column and cannot answer this itself.
ALTER TABLE events ADD COLUMN headline_artist_id UUID REFERENCES artists(id) ON DELETE SET NULL;

-- Postgres does not index a foreign key automatically, and the ON DELETE
-- SET NULL would otherwise seq-scan events.
CREATE INDEX events_headline_artist_id_idx ON events (headline_artist_id);
