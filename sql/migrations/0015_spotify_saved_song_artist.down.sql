-- Remove any rows of the new kind first so the narrower constraint can be
-- re-applied without violation.
DELETE FROM user_interests WHERE kind = 'spotify_saved_song_artist';

ALTER TABLE user_interests
    DROP CONSTRAINT user_interests_kind_check;

ALTER TABLE user_interests
    ADD CONSTRAINT user_interests_kind_check
    CHECK (kind IN ('spotify_top_artist', 'spotify_top_track_artist', 'spotify_top_genre', 'manual_tag'));
