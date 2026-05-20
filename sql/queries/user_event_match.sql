-- name: UpsertUserEventMatch :exec
INSERT INTO user_event_match (user_id, event_id, score, score_breakdown, computed_at)
VALUES ($1, $2, $3, $4, NOW())
ON CONFLICT (user_id, event_id) DO UPDATE SET
    score           = EXCLUDED.score,
    score_breakdown = EXCLUDED.score_breakdown,
    computed_at     = NOW();

-- name: DeleteObsoleteMatches :exec
DELETE FROM user_event_match
WHERE event_id IN (
    SELECT id FROM events
    WHERE archived_at IS NOT NULL OR starts_at <= NOW()
);
