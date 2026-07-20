-- name: GetArtistGenreCache :one
SELECT name_key, mbid, genres, status, resolved_at
FROM artist_genre_cache
WHERE name_key = $1;

-- name: UpsertArtistGenreCache :exec
INSERT INTO artist_genre_cache (name_key, mbid, genres, status, resolved_at)
VALUES ($1, $2, $3, $4, NOW())
ON CONFLICT (name_key) DO UPDATE SET
    mbid        = EXCLUDED.mbid,
    genres      = EXCLUDED.genres,
    status      = EXCLUDED.status,
    resolved_at = NOW();
