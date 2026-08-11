-- Async poster generation jobs. Keyed on the natural (performer, venue, date)
-- so a POST and a later GET agree without the client tracking a job id, and so
-- a repeat request joins the existing job instead of starting a second one.
--
-- svg_key/png_key are S3 OBJECT KEYS, never presigned URLs: a presigned URL
-- expires after an hour, so a stored one would serve a dead link. The API
-- presigns at read time.
CREATE TABLE poster_jobs (
    id             TEXT PRIMARY KEY,
    performer      TEXT NOT NULL,
    venue          TEXT NOT NULL,
    -- Free text, matching the Lambda's contract ("Thursday, August 20").
    date           TEXT NOT NULL,
    status         TEXT NOT NULL CHECK (status IN ('pending', 'ready', 'failed')),
    svg_key        TEXT,
    png_key        TEXT,
    artist         JSONB,
    credit         JSONB,
    failure_stage  TEXT,
    failure_reason TEXT,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
