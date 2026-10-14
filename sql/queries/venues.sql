-- name: UpsertVenue :one
INSERT INTO venues (city_id, name, normalized_name, address, lat, lng, website_url)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (city_id, normalized_name)
DO UPDATE SET
    name        = EXCLUDED.name,
    address     = EXCLUDED.address,
    lat         = EXCLUDED.lat,
    lng         = EXCLUDED.lng,
    website_url = EXCLUDED.website_url,
    updated_at  = NOW()
RETURNING id;

-- name: ListCityVenues :many
-- Every venue in the city with at least one upcoming live (non-archived)
-- event, busiest first (ties alphabetical). The optional segment narrows both the list and the
-- counts with the calendar's rule: NULL-segment events count under every
-- value, so the counts agree with what the calendar shows.
SELECT
    v.id,
    v.name,
    v.address,
    v.lat,
    v.lng,
    v.website_url,
    COUNT(e.id)::int AS upcoming_event_count,
    MIN(e.starts_at)::timestamptz AS next_event_at
FROM venues v
JOIN events e ON e.venue_id = v.id
WHERE v.city_id = sqlc.arg(city_id)
  AND e.archived_at IS NULL
  AND event_over_at(e.starts_at, e.ends_at, e.time_tbd) > NOW()
  AND (
      sqlc.narg(segment)::text IS NULL
      OR e.segment IS NULL
      OR e.segment = sqlc.narg(segment)::text
  )
GROUP BY v.id
ORDER BY upcoming_event_count DESC, lower(v.name), v.id;
