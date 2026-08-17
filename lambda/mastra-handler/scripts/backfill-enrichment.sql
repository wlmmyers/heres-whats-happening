-- Extract upcoming, never-enriched events as JSON rows for the enrichment
-- backfill. READ ONLY — writes nothing.
--
-- Run through the bastion tunnel (see `make bastion-tunnel`), e.g.:
--
--   PGPASSWORD=$(aws secretsmanager get-secret-value --profile servant \
--       --region us-west-2 --secret-id "$PROD_SECRET_ARN" \
--       --query SecretString --output text | jq -r '.password') \
--   psql "host=127.0.0.1 port=5432 dbname=appdb user=app sslmode=require" \
--       --no-align --tuples-only --file scripts/backfill-enrichment.sql \
--       > /tmp/backfill.jsonl
--
-- Output is JSONL: one JSON object per line, matching EventRow in backfill.ts.
-- Nothing but data rows is emitted — no SET, no command tags — so the output
-- pipes straight into the runner.
SELECT jsonb_build_object(
    -- The wire carries the source NAME: internal/ingest/events.go resolves it
    -- with GetEventSourceByName. Sending events.source_id (a UUID) would fail
    -- that lookup and DLQ every message.
    'source_name',       s.name,
    'source_event_id',   e.source_event_id,
    'title',             e.title,
    'description',       e.description,
    -- Rendered explicitly in UTC rather than via to_json(), whose offset follows
    -- the session TimeZone. A start time that came back without a zone, or with
    -- the wrong one, would silently shift the event on re-ingest.
    'starts_at',         to_char(e.starts_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
    'ends_at',           to_char(e.ends_at   AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
    'time_tbd',          e.time_tbd,
    'image_url',         e.image_url,
    'url',               e.url,
    -- The performer name behind headline_artist_id, if any. NULL for every row
    -- this query returns today (the filter below requires it to be NULL), but
    -- kept so the extract stays correct if the scope is ever widened.
    'headline_performer', (
        SELECT ep2.performer_name
        FROM event_performers ep2
        JOIN artists a ON a.name_key = ep2.normalized_name
        WHERE ep2.event_id = e.id AND a.id = e.headline_artist_id
    ),
    'venue_name',        v.name,
    'venue_address',     v.address,
    'venue_lat',         v.lat,
    'venue_lng',         v.lng,
    'venue_website_url', v.website_url,
    -- Physical row order stands in for the original performers array: ingest
    -- bulk-deletes then reinserts these in array order, so ctid usually
    -- preserves it. backfill.ts treats it as the weakest signal.
    'performers',        (
        SELECT COALESCE(jsonb_agg(ep.performer_name ORDER BY ep.ctid), '[]'::jsonb)
        FROM event_performers ep
        WHERE ep.event_id = e.id
    ),
    -- Must be carried: the consumer deletes and reinserts event_genres from the
    -- message, so omitting them would wipe every genre on the event.
    'genres',            (
        SELECT COALESCE(jsonb_agg(eg.genre_slug ORDER BY eg.genre_slug), '[]'::jsonb)
        FROM event_genres eg
        WHERE eg.event_id = e.id
    )
)
FROM events e
JOIN event_sources s ON s.id = e.source_id
JOIN venues v        ON v.id = e.venue_id
WHERE e.archived_at IS NULL
  -- Never enriched. This is the whole point of the backfill, and it also keeps
  -- the run off events that already have a good headline_artist_id.
  AND e.headline_artist_id IS NULL
  -- Upcoming only. Republishing a past event would bump last_seen_at and clear
  -- archived_at for no user-facing gain.
  AND event_over_at(e.starts_at, e.ends_at, e.time_tbd) > NOW()
  -- enrichEvent returns early when performers[0] is undefined, so an event with
  -- no performers would rewrite its own row and enrich nothing.
  AND EXISTS (SELECT 1 FROM event_performers ep WHERE ep.event_id = e.id)
ORDER BY e.starts_at;
