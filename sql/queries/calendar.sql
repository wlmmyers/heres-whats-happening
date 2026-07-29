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
  -- Still showable: a date-only event runs until its local day is out, a timed
  -- one until it ends. Filtering on starts_at alone would drop an event that
  -- has begun but is not over — and drop date-only events from 00:00 onward.
  AND event_over_at(e.starts_at, e.ends_at, e.time_tbd) > NOW()
  AND NOT EXISTS (
      SELECT 1 FROM user_event_not_interested ni
      WHERE ni.user_id = m.user_id AND ni.event_id = e.id
  )
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

-- name: GetCityCalendarInRange :many
-- Every showable event in the city, with no match filtering — this is what the
-- calendar falls back to for a user who has no interests to match against.
SELECT
    e.id              AS event_id,
    e.title,
    e.description,
    e.starts_at,
    e.ends_at,
    e.image_url,
    e.url,
    v.name            AS venue_name,
    v.address         AS venue_address
FROM events e
JOIN venues v ON v.id = e.venue_id
WHERE v.city_id = $1
  AND e.archived_at IS NULL
  AND e.starts_at >= $2
  AND e.starts_at <  $3
  -- Same showable rule as GetUserCalendarInRange: a date-only event runs until
  -- its local day is out, a timed one until it ends.
  AND event_over_at(e.starts_at, e.ends_at, e.time_tbd) > NOW()
ORDER BY e.starts_at ASC;
