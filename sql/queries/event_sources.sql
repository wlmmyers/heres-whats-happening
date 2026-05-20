-- name: GetEventSourceByName :one
SELECT id, name, adapter_kind, config
FROM event_sources
WHERE name = $1;
