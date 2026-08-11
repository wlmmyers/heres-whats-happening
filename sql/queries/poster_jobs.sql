-- Claim a job for generation. Returns a row ONLY when this caller won the
-- claim; sqlc surfaces "not claimed" as pgx.ErrNoRows. A job is claimable when
-- it does not exist, previously failed, or is a pending row stranded by a task
-- restart (nothing else would ever clear it, since the goroutine died with the
-- task).
--
-- The cutoff uses sqlc.arg(stale_before) rather than a positional $5: this
-- statement both SETS updated_at and COMPARES it, and sqlc names positional
-- params after the column they touch, so a bare $5 would collide with the
-- assignment and produce a confusing generated field name.
-- name: ClaimPosterJob :one
INSERT INTO poster_jobs (id, performer, venue, date, status, updated_at)
VALUES ($1, $2, $3, $4, 'pending', NOW())
ON CONFLICT (id) DO UPDATE SET
    status         = 'pending',
    svg_key        = NULL,
    png_key        = NULL,
    artist         = NULL,
    credit         = NULL,
    failure_stage  = NULL,
    failure_reason = NULL,
    updated_at     = NOW()
WHERE poster_jobs.status = 'failed'
   OR (poster_jobs.status = 'pending' AND poster_jobs.updated_at < sqlc.arg(stale_before))
RETURNING *;

-- name: GetPosterJob :one
SELECT * FROM poster_jobs WHERE id = $1;

-- name: MarkPosterJobReady :exec
UPDATE poster_jobs
SET status = 'ready', svg_key = $2, png_key = $3, artist = $4, credit = $5,
    failure_stage = NULL, failure_reason = NULL, updated_at = NOW()
WHERE id = $1;

-- name: MarkPosterJobFailed :exec
UPDATE poster_jobs
SET status = 'failed', failure_stage = $2, failure_reason = $3, updated_at = NOW()
WHERE id = $1;
