-- Events whose source gave a date but no start time. These are stored as local
-- midnight, which is NOT a real start time — without this flag every predicate
-- comparing starts_at to NOW() treats such an event as past from 00:00 on the
-- day it actually happens.
ALTER TABLE events ADD COLUMN time_tbd BOOLEAN NOT NULL DEFAULT FALSE;

-- The instant after which an event is genuinely over and can stop being
-- surfaced. Defined once here so the queries that delete matches and the
-- queries that re-create them can never drift out of sync: a row deleted by
-- one is exactly a row the other declines to return.
--
-- Date-only events get their whole local day (they are stored as local
-- midnight). Timed events get ends_at, or a 4h default runtime when the source
-- did not supply one.
CREATE FUNCTION event_over_at(starts_at TIMESTAMPTZ, ends_at TIMESTAMPTZ, time_tbd BOOLEAN)
RETURNS TIMESTAMPTZ
LANGUAGE SQL
IMMUTABLE
AS $$
    SELECT CASE
        WHEN time_tbd THEN starts_at + INTERVAL '1 day'
        ELSE COALESCE(ends_at, starts_at + INTERVAL '4 hours')
    END;
$$;
