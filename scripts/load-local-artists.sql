-- Stage 2: load prod's artist enrichment into the LOCAL dev DB.
--
-- One transaction, one psql session (the temp tables die with it). Re-runnable:
-- every insert upserts, so a second run is a no-op refresh rather than a
-- duplicate-key failure.
--
-- Unlike the app's UpsertArtistImage/Bio/TourSnapshot, the child upserts here
-- assign EXCLUDED unconditionally. The app guards those columns because a
-- transient provider error must not blank good data; this script is mirroring
-- prod verbatim, so prod's value always wins.

BEGIN;

-- ---------------------------------------------------------------- artists ---
CREATE TEMP TABLE t_artists (
    id UUID, name_key TEXT, display_name TEXT, mbid TEXT, disambiguation TEXT,
    artist_type TEXT, country TEXT, begin_year TEXT, status TEXT,
    resolved_at TIMESTAMPTZ, created_at TIMESTAMPTZ, updated_at TIMESTAMPTZ
) ON COMMIT DROP;
\copy t_artists FROM 'artists.tsv'

-- Prod's id is carried so a fresh local DB ends up with identical UUIDs, which
-- makes the child joins below trivially correct. On a re-run the name_key
-- conflict fires instead and the existing local id is kept.
INSERT INTO artists (id, name_key, display_name, mbid, disambiguation,
                     artist_type, country, begin_year, status,
                     resolved_at, created_at, updated_at)
SELECT id, name_key, display_name, mbid, disambiguation,
       artist_type, country, begin_year, status,
       resolved_at, created_at, updated_at
FROM t_artists
ON CONFLICT (name_key) DO UPDATE SET
    display_name   = EXCLUDED.display_name,
    mbid           = EXCLUDED.mbid,
    disambiguation = EXCLUDED.disambiguation,
    artist_type    = EXCLUDED.artist_type,
    country        = EXCLUDED.country,
    begin_year     = EXCLUDED.begin_year,
    status         = EXCLUDED.status,
    resolved_at    = EXCLUDED.resolved_at,
    updated_at     = EXCLUDED.updated_at;

-- ---------------------------------------------------------- artist_images ---
CREATE TEMP TABLE t_images (
    name_key TEXT, status TEXT, url TEXT, width INT, height INT, file TEXT,
    source TEXT, credit JSONB, reason TEXT, checked_at TIMESTAMPTZ
) ON COMMIT DROP;
\copy t_images FROM 'artist_images.tsv'

INSERT INTO artist_images (artist_id, status, url, width, height, file,
                           source, credit, reason, checked_at)
SELECT a.id, t.status, t.url, t.width, t.height, t.file,
       t.source, t.credit, t.reason, t.checked_at
FROM t_images t JOIN artists a ON a.name_key = t.name_key
ON CONFLICT (artist_id) DO UPDATE SET
    status     = EXCLUDED.status,
    url        = EXCLUDED.url,
    width      = EXCLUDED.width,
    height     = EXCLUDED.height,
    file       = EXCLUDED.file,
    source     = EXCLUDED.source,
    credit     = EXCLUDED.credit,
    reason     = EXCLUDED.reason,
    checked_at = EXCLUDED.checked_at;

-- ------------------------------------------------------------ artist_bios ---
CREATE TEMP TABLE t_bios (
    name_key TEXT, status TEXT, bio_md TEXT, sources JSONB, model TEXT,
    reason TEXT, generated_at TIMESTAMPTZ
) ON COMMIT DROP;
\copy t_bios FROM 'artist_bios.tsv'

INSERT INTO artist_bios (artist_id, status, bio_md, sources, model,
                         reason, generated_at)
SELECT a.id, t.status, t.bio_md, t.sources, t.model, t.reason, t.generated_at
FROM t_bios t JOIN artists a ON a.name_key = t.name_key
ON CONFLICT (artist_id) DO UPDATE SET
    status       = EXCLUDED.status,
    bio_md       = EXCLUDED.bio_md,
    sources      = EXCLUDED.sources,
    model        = EXCLUDED.model,
    reason       = EXCLUDED.reason,
    generated_at = EXCLUDED.generated_at;

-- --------------------------------------------------- artist_tour_snapshots ---
CREATE TEMP TABLE t_tours (
    name_key TEXT, status TEXT, tour_name TEXT, songs JSONB, observed_date DATE,
    observed_venue TEXT, observed_city TEXT, setlist_url TEXT, blurb TEXT,
    blurb_model TEXT, reason TEXT, fetched_at TIMESTAMPTZ
) ON COMMIT DROP;
\copy t_tours FROM 'artist_tour_snapshots.tsv'

INSERT INTO artist_tour_snapshots (artist_id, status, tour_name, songs,
                                   observed_date, observed_venue, observed_city,
                                   setlist_url, blurb, blurb_model, reason, fetched_at)
SELECT a.id, t.status, t.tour_name, t.songs, t.observed_date, t.observed_venue,
       t.observed_city, t.setlist_url, t.blurb, t.blurb_model, t.reason, t.fetched_at
FROM t_tours t JOIN artists a ON a.name_key = t.name_key
ON CONFLICT (artist_id) DO UPDATE SET
    status         = EXCLUDED.status,
    tour_name      = EXCLUDED.tour_name,
    songs          = EXCLUDED.songs,
    observed_date  = EXCLUDED.observed_date,
    observed_venue = EXCLUDED.observed_venue,
    observed_city  = EXCLUDED.observed_city,
    setlist_url    = EXCLUDED.setlist_url,
    blurb          = EXCLUDED.blurb,
    blurb_model    = EXCLUDED.blurb_model,
    reason         = EXCLUDED.reason,
    fetched_at     = EXCLUDED.fetched_at;

-- ------------------------------------------- events.headline_artist_id link ---
CREATE TEMP TABLE t_links (
    source_name TEXT, source_event_id TEXT, name_key TEXT
) ON COMMIT DROP;
\copy t_links FROM 'headline_links.tsv'

-- Only local events that prod also has get linked; the rest keep NULL, exactly
-- as prod has them.
UPDATE events e
SET headline_artist_id = a.id
FROM t_links t
JOIN event_sources s ON s.name = t.source_name
JOIN artists a       ON a.name_key = t.name_key
WHERE e.source_id = s.id
  AND e.source_event_id = t.source_event_id
  AND e.headline_artist_id IS DISTINCT FROM a.id;

COMMIT;
