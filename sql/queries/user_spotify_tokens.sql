-- name: UpsertUserSpotifyTokens :exec
INSERT INTO user_spotify_tokens (
    user_id, access_token_enc, refresh_token_enc, expires_at, scope
)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (user_id)
DO UPDATE SET
    access_token_enc  = EXCLUDED.access_token_enc,
    refresh_token_enc = EXCLUDED.refresh_token_enc,
    expires_at        = EXCLUDED.expires_at,
    scope             = EXCLUDED.scope,
    updated_at        = NOW();

-- name: GetUserSpotifyTokens :one
SELECT user_id, access_token_enc, refresh_token_enc, expires_at, scope, last_synced_at
FROM user_spotify_tokens
WHERE user_id = $1;

-- name: DeleteUserSpotifyTokens :exec
DELETE FROM user_spotify_tokens WHERE user_id = $1;

-- name: ListUserSpotifyTokens :many
SELECT user_id, access_token_enc, refresh_token_enc, expires_at, scope, last_synced_at
FROM user_spotify_tokens
ORDER BY user_id ASC;

-- name: UpdateUserSpotifyTokensLastSynced :exec
UPDATE user_spotify_tokens
SET last_synced_at = NOW(), updated_at = NOW()
WHERE user_id = $1;

-- name: DeleteSpotifyDerivedInterests :exec
DELETE FROM user_interests
WHERE user_id = $1 AND kind IN ('spotify_top_artist', 'spotify_top_track_artist', 'spotify_top_genre');

-- name: ReplaceSpotifyArtistInterests :exec
DELETE FROM user_interests
WHERE user_id = $1 AND kind = 'spotify_top_artist';

-- name: ReplaceSpotifyTrackArtistInterests :exec
DELETE FROM user_interests
WHERE user_id = $1 AND kind = 'spotify_top_track_artist';

-- name: ReplaceSpotifyGenreInterests :exec
DELETE FROM user_interests
WHERE user_id = $1 AND kind = 'spotify_top_genre';

-- name: InsertSpotifyInterest :exec
INSERT INTO user_interests (user_id, kind, value, normalized_value, weight)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (user_id, kind, normalized_value) DO UPDATE SET
    value      = EXCLUDED.value,
    weight     = EXCLUDED.weight,
    updated_at = NOW();
