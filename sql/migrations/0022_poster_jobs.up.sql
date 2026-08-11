-- Async poster generation jobs. Keyed on the natural
-- (user, performer, venue, date) so a POST and a later GET agree without the
-- client tracking a job id, and so a repeat request joins the existing job
-- instead of starting a second one.
--
-- user_id is part of that natural key, not just an owner column. Without it,
-- one row per show is shared by everyone, and POST's force:true — which
-- re-claims a ready row — lets any confirmed user blank any other user's
-- poster. The accepted trade is that two users wanting the same show each
-- generate their own copy.
--
-- svg_key/png_key are S3 OBJECT KEYS, never presigned URLs: a presigned URL
-- expires after an hour, so a stored one would serve a dead link. The API
-- presigns at read time.
CREATE TABLE poster_jobs (
    id             TEXT PRIMARY KEY,
    user_id        UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
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

-- Postgres does not index a foreign key automatically, and every ON DELETE
-- CASCADE from users would otherwise seq-scan this table.
CREATE INDEX poster_jobs_user_id_idx ON poster_jobs (user_id);
