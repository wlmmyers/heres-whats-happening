DELETE FROM event_sources WHERE name = 'email_newsletter';
ALTER TABLE event_sources DROP COLUMN IF EXISTS exempt_from_stale_archive;
