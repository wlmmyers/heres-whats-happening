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
    e.headline_artist_id,
    v.name            AS venue_name,
    v.address         AS venue_address,
    m.score,
    m.score_breakdown
FROM events e
JOIN venues v ON v.id = e.venue_id
LEFT JOIN user_event_match m ON m.event_id = e.id AND m.user_id = $2
WHERE e.id = $1
  AND e.archived_at IS NULL;

-- name: GetUserCalendarPage :many
-- One page of the user's matched events. Ordered by (starts_at, id) so the
-- keyset cursor is stable when several events start at the same instant —
-- ordering on starts_at alone would drop or repeat rows at page boundaries.
SELECT
    e.id              AS event_id,
    e.title,
    e.description,
    e.starts_at,
    e.ends_at,
    e.image_url,
    e.url,
    e.headline_artist_id,
    v.name            AS venue_name,
    v.address         AS venue_address,
    m.score,
    m.score_breakdown
FROM user_event_match m
JOIN events e ON e.id = m.event_id
JOIN venues v ON v.id = e.venue_id
WHERE m.user_id = sqlc.arg(user_id)
  AND e.archived_at IS NULL
  -- Still showable: a date-only event runs until its local day is out, a timed
  -- one until it ends. With the from/to window gone this is also the feed's
  -- lower bound — there is no upper bound.
  AND event_over_at(e.starts_at, e.ends_at, e.time_tbd) > NOW()
  AND NOT EXISTS (
      SELECT 1 FROM user_event_not_interested ni
      WHERE ni.user_id = m.user_id AND ni.event_id = e.id
  )
  -- A NULL cursor means "first page": the guard short-circuits and every row
  -- qualifies. Otherwise this is a strict row-comparison keyset seek.
  -- cursor_starts_at and cursor_event_id are all-or-nothing: passing one
  -- non-NULL and the other NULL makes the row comparison evaluate to NULL for
  -- rows tied on starts_at, silently dropping them. parseCursor always sets
  -- both or neither.
  AND (
      sqlc.narg(cursor_starts_at)::timestamptz IS NULL
      OR (e.starts_at, e.id) > (sqlc.narg(cursor_starts_at)::timestamptz,
                                sqlc.narg(cursor_event_id)::uuid)
  )
  -- The caller-named lower bound, mutually exclusive with the cursor (the
  -- handler rejects both at once with a 422). Strict: an event starting exactly
  -- at the bound is excluded, so passing an event's own starts_at means
  -- "everything after that event". Unlike the cursor there is no id tiebreak,
  -- so events tied on the bound instant all drop out together — which is the
  -- point, since the caller named an instant, not a row.
  AND (
      sqlc.narg(starts_at_after)::timestamptz IS NULL
      OR e.starts_at > sqlc.narg(starts_at_after)::timestamptz
  )
ORDER BY e.starts_at ASC, e.id ASC
LIMIT sqlc.arg(page_limit);

-- name: GetCityCalendarPage :many
-- One page of every showable event in the city, with no match filtering and
-- (deliberately) no not-interested filtering: this endpoint returns an
-- identical response for every caller. Same ordering and cursor rules as
-- GetUserCalendarPage.
SELECT
    e.id              AS event_id,
    e.title,
    e.description,
    e.starts_at,
    e.ends_at,
    e.image_url,
    e.url,
    e.headline_artist_id,
    v.name            AS venue_name,
    v.address         AS venue_address
FROM events e
JOIN venues v ON v.id = e.venue_id
WHERE v.city_id = sqlc.arg(city_id)
  AND e.archived_at IS NULL
  AND event_over_at(e.starts_at, e.ends_at, e.time_tbd) > NOW()
  -- cursor_starts_at and cursor_event_id are all-or-nothing: passing one
  -- non-NULL and the other NULL makes the row comparison evaluate to NULL for
  -- rows tied on starts_at, silently dropping them. parseCursor always sets
  -- both or neither.
  AND (
      sqlc.narg(cursor_starts_at)::timestamptz IS NULL
      OR (e.starts_at, e.id) > (sqlc.narg(cursor_starts_at)::timestamptz,
                                sqlc.narg(cursor_event_id)::uuid)
  )
ORDER BY e.starts_at ASC, e.id ASC
LIMIT sqlc.arg(page_limit);
