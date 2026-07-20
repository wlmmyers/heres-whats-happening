CREATE TABLE artist_genre_cache (
    name_key    TEXT PRIMARY KEY,           -- events.NormalizeString(artist name)
    mbid        TEXT,                        -- NULL when status = 'not_found'
    genres      JSONB NOT NULL DEFAULT '[]'::jsonb, -- [{"name":..,"count":..}]
    status      TEXT NOT NULL CHECK (status IN ('ok', 'not_found')),
    resolved_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
