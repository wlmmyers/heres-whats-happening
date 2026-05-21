-- name: GetUserCalendarInRange :many
SELECT
    e.id              AS event_id,
    e.title,
    e.description,
    e.starts_at,
    e.ends_at,
    e.image_url,
    e.url,
    v.name            AS venue_name,
    v.address         AS venue_address,
    m.score,
    m.score_breakdown
FROM user_event_match m
JOIN events e ON e.id = m.event_id
JOIN venues v ON v.id = e.venue_id
WHERE m.user_id = $1
  AND e.archived_at IS NULL
  AND e.starts_at >= $2
  AND e.starts_at <  $3
ORDER BY e.starts_at ASC;

-- name: GetMatchedEventForUser :one
SELECT
    e.id              AS event_id,
    e.title,
    e.description,
    e.starts_at,
    e.ends_at,
    e.image_url,
    e.url,
    v.name            AS venue_name,
    v.address         AS venue_address,
    m.score,
    m.score_breakdown
FROM events e
JOIN venues v ON v.id = e.venue_id
LEFT JOIN user_event_match m ON m.event_id = e.id AND m.user_id = $2
WHERE e.id = $1
  AND e.archived_at IS NULL;
