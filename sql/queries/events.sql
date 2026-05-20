-- name: UpsertEvent :one
INSERT INTO events (
    source_id, source_event_id, title, description, starts_at, ends_at,
    venue_id, image_url, url, last_seen_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, NOW())
ON CONFLICT (source_id, source_event_id)
DO UPDATE SET
    title         = EXCLUDED.title,
    description   = EXCLUDED.description,
    starts_at     = EXCLUDED.starts_at,
    ends_at       = EXCLUDED.ends_at,
    venue_id      = EXCLUDED.venue_id,
    image_url     = EXCLUDED.image_url,
    url           = EXCLUDED.url,
    last_seen_at  = NOW(),
    archived_at   = NULL,
    updated_at    = NOW()
RETURNING id;

-- name: GetEventByID :one
SELECT id, source_id, source_event_id, title, description, starts_at, ends_at,
       venue_id, image_url, url, last_seen_at, archived_at, created_at, updated_at
FROM events
WHERE id = $1;

-- name: GetEventBySourceKey :one
SELECT id, source_id, source_event_id, title, description, starts_at, ends_at,
       venue_id, image_url, url, last_seen_at, archived_at, created_at, updated_at
FROM events
WHERE source_id = $1 AND source_event_id = $2;
