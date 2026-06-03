-- Allow a third Spotify-derived interest kind: artists extracted from the
-- user's top tracks (distinct signal from their top artists). The CHECK
-- constraint is anonymous, so it carries Postgres's default name.
ALTER TABLE user_interests
    DROP CONSTRAINT user_interests_kind_check;

ALTER TABLE user_interests
    ADD CONSTRAINT user_interests_kind_check
    CHECK (kind IN ('spotify_top_artist', 'spotify_top_track_artist', 'spotify_top_genre', 'manual_tag'));
