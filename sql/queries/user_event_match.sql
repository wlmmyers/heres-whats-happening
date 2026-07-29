-- name: UpsertUserEventMatch :exec
INSERT INTO user_event_match (user_id, event_id, score, score_breakdown, computed_at)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (user_id, event_id) DO UPDATE SET
    score           = EXCLUDED.score,
    score_breakdown = EXCLUDED.score_breakdown,
    computed_at     = EXCLUDED.computed_at;

-- name: DeleteObsoleteMatches :exec
-- Inverse of ListUpcomingEventsForMatching: this deletes exactly the rows that
-- query would decline to re-create. Keep the two predicates mirrored.
DELETE FROM user_event_match
WHERE event_id IN (
    SELECT id FROM events
    WHERE archived_at IS NOT NULL
       OR event_over_at(starts_at, ends_at, time_tbd) <= NOW()
);

-- name: DeleteStaleMatchesForUsers :exec
DELETE FROM user_event_match
WHERE user_id = ANY(@user_ids::uuid[])
  AND computed_at < @cutoff;
