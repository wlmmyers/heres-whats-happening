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

-- name: GetDefaultCity :one
SELECT id, slug, name, timezone
FROM cities
WHERE slug = 'v1-city';

-- name: SelectUsersNeedingEmbedding :many
SELECT u.id
FROM users u
WHERE u.deleted_at IS NULL
  AND (
    u.interest_embedding IS NULL
    OR u.interest_embedding_updated_at IS NULL
    OR u.interest_embedding_updated_at < COALESCE(
         (SELECT MAX(updated_at) FROM user_interests ui WHERE ui.user_id = u.id),
         u.created_at
       )
  );

-- name: UpdateUserInterestEmbedding :exec
UPDATE users
SET interest_embedding = $2, interest_embedding_updated_at = NOW()
WHERE id = $1;

-- name: ListActiveUsersForMatching :many
SELECT id, interest_embedding
FROM users
WHERE deleted_at IS NULL;
