-- name: CreateUser :one
INSERT INTO users (email, password_hash, city_id)
VALUES ($1, $2, $3)
RETURNING id, email, city_id, created_at;

-- name: GetUserByEmail :one
SELECT id, email, password_hash, city_id, created_at
FROM users
WHERE email = $1 AND deleted_at IS NULL;

-- name: GetUserByID :one
SELECT id, email, city_id, created_at
FROM users
WHERE id = $1 AND deleted_at IS NULL;

-- name: SoftDeleteUser :exec
UPDATE users
SET deleted_at = NOW()
WHERE id = $1 AND deleted_at IS NULL;
