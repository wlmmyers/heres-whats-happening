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
    -- Guarded, not EXCLUDED: a 90-day-stale cache entry that happens to
    -- re-run into a transient provider error (e.g. an LLM 429/529) sends
    -- status='error' with every payload column NULL. Assigning those
    -- unconditionally would blank out a good photo. status/reason/checked_at
    -- stay unconditional so the error is observable and the TTL clock moves.
    url        = CASE WHEN EXCLUDED.status = 'ok' THEN EXCLUDED.url ELSE artist_images.url END,
    width      = CASE WHEN EXCLUDED.status = 'ok' THEN EXCLUDED.width ELSE artist_images.width END,
    height     = CASE WHEN EXCLUDED.status = 'ok' THEN EXCLUDED.height ELSE artist_images.height END,
    file       = CASE WHEN EXCLUDED.status = 'ok' THEN EXCLUDED.file ELSE artist_images.file END,
    source     = CASE WHEN EXCLUDED.status = 'ok' THEN EXCLUDED.source ELSE artist_images.source END,
    credit     = CASE WHEN EXCLUDED.status = 'ok' THEN EXCLUDED.credit ELSE artist_images.credit END,
    reason     = EXCLUDED.reason,
    checked_at = NOW();

-- name: UpsertArtistBio :exec
INSERT INTO artist_bios (
    artist_id, status, bio_md, sources, model, reason, generated_at
)
VALUES ($1, $2, $3, $4, $5, $6, NOW())
ON CONFLICT (artist_id) DO UPDATE SET
    status       = EXCLUDED.status,
    -- Guarded, not EXCLUDED: see the comment on UpsertArtistImage. A transient
    -- error re-run must not blank a bio that a previous run already wrote.
    bio_md       = CASE WHEN EXCLUDED.status = 'ok' THEN EXCLUDED.bio_md ELSE artist_bios.bio_md END,
    sources      = CASE WHEN EXCLUDED.status = 'ok' THEN EXCLUDED.sources ELSE artist_bios.sources END,
    model        = CASE WHEN EXCLUDED.status = 'ok' THEN EXCLUDED.model ELSE artist_bios.model END,
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
    -- Guarded, not EXCLUDED: see the comment on UpsertArtistImage. A transient
    -- error re-run must not blank a setlist/blurb that a previous run already
    -- wrote.
    tour_name      = CASE WHEN EXCLUDED.status = 'ok' THEN EXCLUDED.tour_name ELSE artist_tour_snapshots.tour_name END,
    songs          = CASE WHEN EXCLUDED.status = 'ok' THEN EXCLUDED.songs ELSE artist_tour_snapshots.songs END,
    observed_date  = CASE WHEN EXCLUDED.status = 'ok' THEN EXCLUDED.observed_date ELSE artist_tour_snapshots.observed_date END,
    observed_venue = CASE WHEN EXCLUDED.status = 'ok' THEN EXCLUDED.observed_venue ELSE artist_tour_snapshots.observed_venue END,
    observed_city  = CASE WHEN EXCLUDED.status = 'ok' THEN EXCLUDED.observed_city ELSE artist_tour_snapshots.observed_city END,
    setlist_url    = CASE WHEN EXCLUDED.status = 'ok' THEN EXCLUDED.setlist_url ELSE artist_tour_snapshots.setlist_url END,
    blurb          = CASE WHEN EXCLUDED.status = 'ok' THEN EXCLUDED.blurb ELSE artist_tour_snapshots.blurb END,
    blurb_model    = CASE WHEN EXCLUDED.status = 'ok' THEN EXCLUDED.blurb_model ELSE artist_tour_snapshots.blurb_model END,
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
