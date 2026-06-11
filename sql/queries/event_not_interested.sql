-- name: AddNotInterested :exec
INSERT INTO user_event_not_interested (user_id, event_id)
VALUES ($1, $2)
ON CONFLICT (user_id, event_id) DO NOTHING;

-- name: ClearNotInterested :exec
DELETE FROM user_event_not_interested
WHERE user_id = $1;
