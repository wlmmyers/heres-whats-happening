-- The SVG artifact is gone: only the PNG is wanted, and dropping the artifact
-- also removes the stored-XSS surface it carried (it was served as
-- image/svg+xml after only an XML well-formedness check).
ALTER TABLE poster_jobs DROP COLUMN svg_key;

-- Length bounds. These MUST match the Go handler and the Lambda's zod schema.
-- The handlers reject over-long input at the edge, so nothing should ever reach
-- these — they exist for the second writer that does not exist yet.
ALTER TABLE poster_jobs
    ADD CONSTRAINT poster_jobs_performer_len CHECK (char_length(performer) <= 200),
    ADD CONSTRAINT poster_jobs_venue_len     CHECK (char_length(venue) <= 200),
    ADD CONSTRAINT poster_jobs_date_len      CHECK (char_length(date) <= 100);
