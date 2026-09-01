-- name: AddGoing :exec
INSERT INTO user_event_going (user_id, event_id)
VALUES ($1, $2)
ON CONFLICT (user_id, event_id) DO NOTHING;

-- name: ClearGoing :exec
DELETE FROM user_event_going
WHERE user_id = $1 AND event_id = $2;

-- name: ListGoing :many
SELECT event_id
FROM user_event_going
WHERE user_id = $1;

-- name: ListGoingEvents :many
-- Every event the user has marked going, in full calendar-row shape. Past and
-- future alike: the client splits the list on the event's own start time, so a
-- time filter here would cost it the "went" half. LEFT JOIN on the match so an
-- event marked going from the all-city calendar -- which matches nothing -- is
-- still returned, with a NULL score.
SELECT
    e.id              AS event_id,
    e.title,
    e.description,
    e.starts_at,
    e.ends_at,
    e.image_url,
    e.url,
    e.segment,
    e.headline_artist_id,
    v.name            AS venue_name,
    v.address         AS venue_address,
    m.score,
    m.score_breakdown
FROM user_event_going g
JOIN events e ON e.id = g.event_id
JOIN venues v ON v.id = e.venue_id
LEFT JOIN user_event_match m ON m.event_id = e.id AND m.user_id = g.user_id
WHERE g.user_id = $1
  AND e.archived_at IS NULL
-- id breaks the tie so events starting at the same instant keep a stable order.
ORDER BY e.starts_at ASC, e.id ASC;
