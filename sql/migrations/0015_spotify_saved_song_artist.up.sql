-- Allow a fourth Spotify-derived interest kind: artists drawn from the user's
-- saved tracks ("/me/tracks"), ranked by how recently each track was saved.
-- The CHECK constraint is anonymous, so it carries Postgres's default name.
ALTER TABLE user_interests
    DROP CONSTRAINT user_interests_kind_check;

ALTER TABLE user_interests
    ADD CONSTRAINT user_interests_kind_check
    CHECK (kind IN ('spotify_top_artist', 'spotify_top_track_artist', 'spotify_saved_song_artist', 'spotify_top_genre', 'manual_tag'));
