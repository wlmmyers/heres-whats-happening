-- name: SearchEvents :many
-- Three legs unioned, then max()-ed per event: an event whose title AND
-- performer both match scores as its best leg, not the sum, so a title hit is
-- never outranked by an event matching weakly on two fields.
WITH candidates AS (
        SELECT e.id,
               similarity(immutable_unaccent(e.title),
                          immutable_unaccent(sqlc.arg(query)::text)) AS score
        FROM events e
        WHERE immutable_unaccent(e.title) % immutable_unaccent(sqlc.arg(query)::text)
    UNION ALL
        -- 0.9: a performer match is nearly as good as a title match, and often
        -- IS the title match seen from the other side.
        SELECT p.event_id,
               similarity(immutable_unaccent(p.performer_name),
                          immutable_unaccent(sqlc.arg(query)::text)) * 0.9
        FROM event_performers p
        WHERE immutable_unaccent(p.performer_name) % immutable_unaccent(sqlc.arg(query)::text)
    UNION ALL
        -- 0.6: "Showbox" should surface shows at the Showbox, but must never
        -- outrank an artist of that name. Every event at a matched venue gets
        -- this score, so without the discount one venue hit floods the page.
        SELECT e.id,
               similarity(immutable_unaccent(v.name),
                          immutable_unaccent(sqlc.arg(query)::text)) * 0.6
        FROM venues v
        JOIN events e ON e.venue_id = v.id
        WHERE immutable_unaccent(v.name) % immutable_unaccent(sqlc.arg(query)::text)
),
best AS (SELECT id, max(score) AS rank FROM candidates GROUP BY id)
-- ::float8 on rank: the three legs' scores are real vs. double precision (the
-- 0.9/0.6 weighting promotes them), so sqlc can't statically resolve one type
-- across the UNION and falls back to `any`. The cast is a no-op at runtime --
-- Postgres already returns double precision here -- it only pins the type sqlc
-- generates (float64) so callers don't need a type assertion.
SELECT e.id, e.title, e.starts_at, e.image_url, e.segment,
       v.name AS venue_name, b.rank::float8 AS rank
FROM best b
JOIN events e ON e.id = b.id
JOIN venues v ON v.id = e.venue_id
WHERE v.city_id = sqlc.arg(city_id)
  AND e.archived_at IS NULL
  -- Same showable predicate as GetCityCalendarPage. Written after candidate
  -- generation because that is where it reads clearly; it is not a claim about
  -- the plan. event_over_at() is not indexed, and at dev scale the planner
  -- seq-scans rather than probing the trigram indexes first, so treat this as
  -- a correctness filter and re-measure before relying on it being cheap.
  AND event_over_at(e.starts_at, e.ends_at, e.time_tbd) > NOW()
-- e.id last so the order is total: two events tied on rank and starts_at would
-- otherwise come back in arbitrary order and flicker between keystrokes.
ORDER BY b.rank DESC, e.starts_at ASC, e.id ASC
LIMIT sqlc.arg(result_limit);

-- name: SearchEventsSemantic :many
-- The semantic leg, used only when SEARCH_SEMANTIC_ENABLED is on. Brute-force
-- cosine over the live rows: 4.45ms at 10k events, so no ivfflat/hnsw index is
-- warranted yet -- and adding one would trade exact results for approximate
-- ones to save time we are not short of.
--
-- Projects exactly the same display columns as SearchEvents, and deliberately
-- so. RRF fuses the two legs' ORDERINGS, so a fused id may have come from this
-- leg alone -- the "baseball" case in the design doc, where no event contains
-- the string and every hit is semantic. When this query returned bare ids the
-- handler had no display data for such a row and dropped it, which made the
-- whole semantic leg unreachable: a query with no lexical rows returned an
-- empty list, exactly as if the flag were off. Carrying the columns here costs
-- nothing (the rows are already being read) and is what lets any fused id
-- render. Do not reduce this back to `SELECT e.id`.
--
-- What still deliberately does NOT leave SQL is the distance. The handler
-- fuses by RANK, never by score -- see the Fusion note in the design for why
-- no cosine cutoff can work -- so no similarity or distance column is
-- projected from either leg.
SELECT e.id, e.title, e.starts_at, e.image_url, e.segment,
       v.name AS venue_name
FROM events e
JOIN venues v ON v.id = e.venue_id
WHERE v.city_id = sqlc.arg(city_id)
  AND e.archived_at IS NULL
  AND e.embedding IS NOT NULL
  AND event_over_at(e.starts_at, e.ends_at, e.time_tbd) > NOW()
-- e.id ASC so the order is total. Ties are the common case, not an edge
-- case: recurring events feed BuildEventText identical input and so get
-- byte-identical embeddings -- 80 of the 195 embedded events in the dev
-- catalogue fall into 26 such groups. Without the tiebreak both the order
-- WITHIN a group and which of its rows survive LIMIT are unpinned, and
-- ingestion re-upserts constantly, so the answer can change between
-- keystrokes -- the same flicker the lexical leg's e.id ASC prevents.
ORDER BY e.embedding <=> sqlc.arg(query_embedding), e.id ASC
LIMIT sqlc.arg(candidate_limit);
