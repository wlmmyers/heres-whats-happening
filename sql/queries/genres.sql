-- name: ListGenres :many
SELECT slug, label FROM genres ORDER BY label ASC;

-- name: GenreExists :one
SELECT EXISTS (SELECT 1 FROM genres WHERE slug = $1) AS exists;
