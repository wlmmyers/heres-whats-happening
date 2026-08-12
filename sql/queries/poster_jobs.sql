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
-- The cutoff, the force flag and the user id use sqlc.arg(...) rather than
-- bare positional placeholders: this statement both SETS columns and COMPARES
-- against new values, and sqlc names positional params after the column they
-- touch, so a bare placeholder would collide with an assignment and produce a
-- confusing generated field name. sqlc.arg(user_id) additionally lets one
-- parameter serve both the INSERT value and the conflict guard.
--
-- The trailing "poster_jobs.user_id = sqlc.arg(user_id)" is belt and braces.
-- id is already a digest that includes the user, so a row with this id can
-- only be this user's — but that is a property of poster.JobID, and a
-- regression there (one has already happened: fields used to be joined with a
-- NUL that could be smuggled in through the request body) must not be enough
-- on its own to let one user re-claim and blank another user's poster. Here
-- the scoping is enforced by the statement.
-- name: ClaimPosterJob :one
INSERT INTO poster_jobs (id, user_id, performer, venue, date, status, updated_at)
VALUES ($1, sqlc.arg(user_id), $2, $3, $4, 'pending', NOW())
ON CONFLICT (id) DO UPDATE SET
    status         = 'pending',
    png_key        = NULL,
    artist         = NULL,
    credit         = NULL,
    failure_stage  = NULL,
    failure_reason = NULL,
    updated_at     = NOW()
WHERE poster_jobs.user_id = sqlc.arg(user_id)
  AND (poster_jobs.status = 'failed'
   OR (poster_jobs.status = 'pending' AND poster_jobs.updated_at < sqlc.arg(stale_before))
   OR (poster_jobs.status = 'ready'   AND sqlc.arg(force)::boolean))
RETURNING *;

-- Scoped by user for the same belt-and-braces reason as the claim: a job id
-- collision across two users must not be able to hand one of them the other's
-- presigned artifact URLs.
-- name: GetPosterJob :one
SELECT * FROM poster_jobs WHERE id = $1 AND user_id = $2;

-- name: MarkPosterJobReady :exec
UPDATE poster_jobs
SET status = 'ready', png_key = $2, artist = $3, credit = $4,
    failure_stage = NULL, failure_reason = NULL, updated_at = NOW()
WHERE id = $1;

-- name: MarkPosterJobFailed :exec
UPDATE poster_jobs
SET status = 'failed', failure_stage = $2, failure_reason = $3, updated_at = NOW()
WHERE id = $1;
