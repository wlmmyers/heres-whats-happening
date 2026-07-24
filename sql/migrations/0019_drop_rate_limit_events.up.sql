-- The rate limiter is now entirely in-process (internal/ratelimit/memory.go),
-- so nothing reads or writes this table.
DROP TABLE IF EXISTS rate_limit_events;
