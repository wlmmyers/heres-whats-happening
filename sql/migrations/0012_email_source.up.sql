-- Email-newsletter ingestion source. A single shared source so the content-hash
-- dedup (source_event_id) spans all promoters.
ALTER TABLE event_sources
    ADD COLUMN exempt_from_stale_archive BOOLEAN NOT NULL DEFAULT false;

INSERT INTO event_sources (name, adapter_kind, config, exempt_from_stale_archive)
VALUES ('email_newsletter', 'email_inbound', '{}'::jsonb, true);
