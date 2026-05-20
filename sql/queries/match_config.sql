-- name: ListMatchConfig :many
SELECT key, value FROM match_config ORDER BY key ASC;
