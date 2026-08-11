-- Claim a job for generation. Returns a row ONLY when this caller won the
-- claim; sqlc surfaces "not claimed" as pgx.ErrNoRows. A job is claimable when
-- it does not exist, previously failed, is a pending row stranded by a task
-- restart (nothing else would ever clear it, since the goroutine died with the
-- task), or is a ready row and the caller passed force=true (the "regenerate
-- a poster I dislike" escape hatch — see
-- docs/superpowers/specs/2026-08-09-file-backed-poster-artifacts-design.md).
--
-- The force clause is deliberately scoped to status = 'ready' only: a fresh
-- pending row must stay un-reclaimable even under force, or two generations
-- would run concurrently for the same job and the second would stomp the
-- first's result.
--
-- The cutoff and the force flag use sqlc.arg(stale_before)/sqlc.arg(force)
-- rather than positional $5/$6: this statement both SETS columns and COMPARES
-- against new values, and sqlc names positional params after the column they
-- touch, so a bare $5/$6 would collide with an assignment and produce a
-- confusing generated field name.
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
   OR (poster_jobs.status = 'ready'   AND sqlc.arg(force)::boolean)
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
