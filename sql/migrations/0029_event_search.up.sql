CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE EXTENSION IF NOT EXISTS unaccent;

-- unaccent() is STABLE, not IMMUTABLE, so it cannot appear in an index
-- expression. The two-argument form names the dictionary explicitly, which
-- makes the result deterministic and the wrapper safe to mark IMMUTABLE --
-- the documented Postgres workaround.
--
-- What this buys: "eden munoz" finds "Edén Muñoz". Untreated, pg_trgm scores
-- that pair at 0.294 -- under the default 0.3 threshold AND under the 0.2 this
-- app uses -- so the event is simply unreachable without the accent keys.
--
-- The caveat inherent to the workaround: if the unaccent dictionary is ever
-- changed, every index below must be REINDEXed. It ships with Postgres and we
-- do not modify it.
CREATE FUNCTION immutable_unaccent(text) RETURNS text
    AS $$ SELECT public.unaccent('public.unaccent', $1) $$
    LANGUAGE sql IMMUTABLE STRICT PARALLEL SAFE;

-- One index per search leg. No lower() -- pg_trgm is already case-insensitive,
-- so the accent fold is the only transform needed. Plain CREATE INDEX, not
-- CONCURRENTLY: all three build in 0.68s at 10k rows, and CONCURRENTLY cannot
-- run inside the migration transaction anyway.
CREATE INDEX events_title_trgm ON events
    USING gin (immutable_unaccent(title) gin_trgm_ops);
CREATE INDEX event_performers_name_trgm ON event_performers
    USING gin (immutable_unaccent(performer_name) gin_trgm_ops);
CREATE INDEX venues_name_trgm ON venues
    USING gin (immutable_unaccent(name) gin_trgm_ops);
