-- name: AddNotInterested :exec
INSERT INTO user_event_not_interested (user_id, event_id)
VALUES ($1, $2)
ON CONFLICT (user_id, event_id) DO NOTHING;

-- name: ClearNotInterested :exec
DELETE FROM user_event_not_interested
WHERE user_id = $1;

-- name: ListNotInterested :many
SELECT ni.event_id
FROM user_event_not_interested ni
JOIN events e ON e.id = ni.event_id
WHERE ni.user_id = $1
  -- Only events that have not happened yet. Uses the same "still showable"
  -- predicate as the calendar queries: a date-only event runs until its local
  -- day is out, a timed one until it ends.
  AND event_over_at(e.starts_at, e.ends_at, e.time_tbd) > NOW();
