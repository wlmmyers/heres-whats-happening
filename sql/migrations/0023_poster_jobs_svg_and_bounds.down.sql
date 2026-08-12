ALTER TABLE poster_jobs
    DROP CONSTRAINT poster_jobs_performer_len,
    DROP CONSTRAINT poster_jobs_venue_len,
    DROP CONSTRAINT poster_jobs_date_len;

-- Nullable: rows written after the drop have no svg key to restore.
ALTER TABLE poster_jobs ADD COLUMN svg_key TEXT;
