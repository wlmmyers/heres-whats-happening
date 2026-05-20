-- name: DeleteEventPerformersByEvent :exec
DELETE FROM event_performers WHERE event_id = $1;

-- name: InsertEventPerformer :exec
INSERT INTO event_performers (event_id, performer_name, normalized_name)
VALUES ($1, $2, $3)
ON CONFLICT (event_id, normalized_name) DO NOTHING;

-- name: ListEventPerformersByEvent :many
SELECT performer_name, normalized_name
FROM event_performers
WHERE event_id = $1
ORDER BY performer_name ASC;

-- name: DeleteEventGenresByEvent :exec
DELETE FROM event_genres WHERE event_id = $1;

-- name: InsertEventGenre :exec
INSERT INTO event_genres (event_id, genre_slug)
VALUES ($1, $2)
ON CONFLICT DO NOTHING;

-- name: ListEventGenresByEvent :many
SELECT genre_slug FROM event_genres WHERE event_id = $1 ORDER BY genre_slug ASC;
