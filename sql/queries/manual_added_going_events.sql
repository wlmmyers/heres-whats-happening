-- name: ListManualAddedGoingEventsByUser :many
-- Newest show first. The client re-sorts anyway once these are merged with the
-- real going events, but a stable server order keeps the list from shuffling
-- between fetches. id breaks the tie so two shows on the same day hold still.
SELECT id, show_date, event_name, venue_name, created_at
FROM user_manual_added_going_events
WHERE user_id = $1
ORDER BY show_date DESC, id ASC;

-- name: CreateManualAddedGoingEvent :one
INSERT INTO user_manual_added_going_events (user_id, show_date, event_name, venue_name)
VALUES ($1, $2, $3, $4)
RETURNING id, show_date, event_name, venue_name, created_at;

-- name: UpdateManualAddedGoingEventForUser :one
-- user_id in the WHERE, not just the id: without it any confirmed user could
-- rewrite any other user's row by guessing a uuid. No row returned means the
-- id is unknown OR belongs to someone else, and the handler answers 404 to
-- both so the endpoint never confirms that a stranger's id exists.
UPDATE user_manual_added_going_events
SET show_date  = $3,
    event_name = $4,
    venue_name = $5,
    updated_at = NOW()
WHERE id = $1 AND user_id = $2
RETURNING id, show_date, event_name, venue_name, created_at;

-- name: DeleteManualAddedGoingEventForUser :exec
-- Scoped by user for the same reason as the update above. Idempotent: deleting
-- an unknown or someone else's id affects no rows and is not an error.
DELETE FROM user_manual_added_going_events
WHERE id = $1 AND user_id = $2;
