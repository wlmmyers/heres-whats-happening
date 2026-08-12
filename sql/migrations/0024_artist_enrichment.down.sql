DROP INDEX IF EXISTS events_headline_artist_id_idx;
ALTER TABLE events DROP COLUMN IF EXISTS headline_artist_id;
DROP TABLE IF EXISTS artist_tour_snapshots;
DROP TABLE IF EXISTS artist_bios;
DROP TABLE IF EXISTS artist_images;
DROP TABLE IF EXISTS artists;
