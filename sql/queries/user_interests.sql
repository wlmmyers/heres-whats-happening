-- name: ListManualInterestsByUser :many
SELECT id, value, normalized_value, weight, created_at
FROM user_interests
WHERE user_id = $1 AND kind = 'manual_tag'
ORDER BY created_at ASC;

-- name: CreateManualInterest :one
INSERT INTO user_interests (user_id, kind, value, normalized_value, weight)
VALUES ($1, 'manual_tag', $2, $3, 1.0)
RETURNING id, value, normalized_value, weight, created_at;

-- name: DeleteInterestByIDForUser :exec
DELETE FROM user_interests
WHERE id = $1 AND user_id = $2;

-- name: ListInterestsByUserAndKind :many
SELECT id, kind, value, normalized_value, weight
FROM user_interests
WHERE user_id = $1 AND kind = $2
ORDER BY weight DESC, normalized_value ASC;
