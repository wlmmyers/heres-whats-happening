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
       venue_id, image_url, url, embedding, last_seen_at, archived_at, created_at, updated_at
FROM events
WHERE id = $1;

-- name: GetEventBySourceKey :one
SELECT id, source_id, source_event_id, title, description, starts_at, ends_at,
       venue_id, image_url, url, last_seen_at, archived_at, created_at, updated_at
FROM events
WHERE source_id = $1 AND source_event_id = $2;

-- name: SelectEventsNeedingEmbedding :many
SELECT id, title, description
FROM events
WHERE embedding IS NULL
  AND archived_at IS NULL
  AND starts_at > NOW();

-- name: UpdateEventEmbedding :exec
UPDATE events
SET embedding = $2, embedding_updated_at = NOW(), updated_at = NOW()
WHERE id = $1;

-- name: ListUpcomingEventsForMatching :many
SELECT id, embedding
FROM events
WHERE archived_at IS NULL AND starts_at > NOW();

-- name: ListEventPerformersBatch :many
SELECT event_id, performer_name, normalized_name
FROM event_performers
WHERE event_id = ANY($1::uuid[]);

-- name: ListEventGenresBatch :many
SELECT event_id, genre_slug
FROM event_genres
WHERE event_id = ANY($1::uuid[]);

-- name: ArchiveStaleEvents :exec
UPDATE events
SET archived_at = NOW(), updated_at = NOW()
WHERE archived_at IS NULL
  AND last_seen_at < NOW() - INTERVAL '7 days';
