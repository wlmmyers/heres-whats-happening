-- name: UpsertArtist :one
INSERT INTO artists (
    name_key, display_name, mbid, disambiguation, artist_type,
    country, begin_year, status, resolved_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NOW())
ON CONFLICT (name_key) DO UPDATE SET
    display_name   = EXCLUDED.display_name,
    mbid           = EXCLUDED.mbid,
    disambiguation = EXCLUDED.disambiguation,
    artist_type    = EXCLUDED.artist_type,
    country        = EXCLUDED.country,
    begin_year     = EXCLUDED.begin_year,
    status         = EXCLUDED.status,
    resolved_at    = NOW(),
    updated_at     = NOW()
RETURNING id;

-- name: UpsertArtistImage :exec
INSERT INTO artist_images (
    artist_id, status, url, width, height, file, source, credit, reason, checked_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, NOW())
ON CONFLICT (artist_id) DO UPDATE SET
    status     = EXCLUDED.status,
    url        = EXCLUDED.url,
    width      = EXCLUDED.width,
    height     = EXCLUDED.height,
    file       = EXCLUDED.file,
    source     = EXCLUDED.source,
    credit     = EXCLUDED.credit,
    reason     = EXCLUDED.reason,
    checked_at = NOW();

-- name: UpsertArtistBio :exec
INSERT INTO artist_bios (
    artist_id, status, bio_md, sources, model, reason, generated_at
)
VALUES ($1, $2, $3, $4, $5, $6, NOW())
ON CONFLICT (artist_id) DO UPDATE SET
    status       = EXCLUDED.status,
    bio_md       = EXCLUDED.bio_md,
    sources      = EXCLUDED.sources,
    model        = EXCLUDED.model,
    reason       = EXCLUDED.reason,
    generated_at = NOW();

-- name: UpsertArtistTourSnapshot :exec
INSERT INTO artist_tour_snapshots (
    artist_id, status, tour_name, songs, observed_date, observed_venue,
    observed_city, setlist_url, blurb, blurb_model, reason, fetched_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, NOW())
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
    fetched_at     = NOW();

-- name: GetArtistEnrichmentBatch :many
-- One row per artist id, with whatever enrichment exists. Left joins throughout
-- so a resolved artist with no successful enrichment still comes back. Mirrors
-- ListEventPerformersBatch's shape: the calendar handlers fetch a page, then
-- batch-load the page's artists in one round trip.
SELECT
    a.id           AS artist_id,
    a.display_name,
    a.mbid,
    a.disambiguation,
    a.status       AS artist_status,
    i.status       AS image_status,
    i.url          AS image_url,
    i.width        AS image_width,
    i.height       AS image_height,
    i.credit       AS image_credit,
    b.status       AS bio_status,
    b.bio_md,
    b.sources      AS bio_sources,
    t.status       AS tour_status,
    t.tour_name,
    t.songs,
    t.observed_date,
    t.observed_venue,
    t.observed_city,
    t.setlist_url,
    t.blurb
FROM artists a
LEFT JOIN artist_images         i ON i.artist_id = a.id
LEFT JOIN artist_bios           b ON b.artist_id = a.id
LEFT JOIN artist_tour_snapshots t ON t.artist_id = a.id
WHERE a.id = ANY($1::uuid[]);
