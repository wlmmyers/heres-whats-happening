DROP INDEX IF EXISTS venues_name_trgm;
DROP INDEX IF EXISTS event_performers_name_trgm;
DROP INDEX IF EXISTS events_title_trgm;
DROP FUNCTION IF EXISTS immutable_unaccent(text);
-- Extensions deliberately left in place: they are idempotent to re-create and
-- dropping them on a shared database is a wider blast radius than this
-- migration owns.
