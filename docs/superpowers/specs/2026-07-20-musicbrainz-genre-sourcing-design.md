# Sourcing artist genres from MusicBrainz

**Date:** 2026-07-20
**Status:** Approved (design)

## Problem

Spotify has deprecated the `genres` array on the artist object returned by
`GET /v1/me/top/artists`; it now comes back empty even for well-classified
artists. As a result the Spotify scraper builds an empty `genreCount`, the
ingest handler's genre loop iterates zero times, and **no `spotify_top_genre`
rows are ever written**. Confirmed against the live DB: 50 `spotify_top_artist`
rows, 25 `spotify_top_track_artist`, 200 `spotify_saved_song_artist`, and 0
`spotify_top_genre`. This also silently degrades matching — `matcher/text.go`
only emits a "Top genres:" section when the list is non-empty, so user
embeddings are built from artist names alone.

## Goal

Restore `spotify_top_genre` interests by sourcing genres from MusicBrainz,
keyed off the artist names Spotify still returns.

## Scope boundary

All new logic lives in the scraper / MusicBrainz layer. **The
`events.InterestMessage` schema, the `internal/ingest` handler, the
`user_interests` SQL, and the `spotify_top_genre` kind are untouched.**
`SpotifyTopGenres` remains `[]events.SpotifyTopItem{Name, Rank}`; ingest still
applies `rankGenreWeight(rank)`. The new relevance score only determines the
rank *ordering* of genres, not the message shape.

## Data flow

```
ScrapeOne(user)
  -> GetTopArtists (names only; Spotify genres now empty)
  -> for each artist name:
        genreResolver.Resolve(name):
          cache hit (fresh) -> return cached genres
          miss / stale      -> MB search (name -> MBID)
                               -> MB lookup (MBID -> genres w/ vote counts)
                               -> upsert cache row -> return genres
  -> aggregate:
        genreScore[g] = SUM over user's artists carrying g:
                          rankWeight(artist.rank) * mbVoteCount(artist, g)
  -> rank genres by score desc, then name asc
  -> msg.SpotifyTopGenres = [{Name, Rank}] (Rank = 1-based position)
  -> publish InterestMessage (unchanged shape)
```

`rankWeight` is the existing artist weight function in `internal/ingest`
(rank 1 -> 1.0 ramping to 0.6 at rank 50). The aggregation reuses that same
curve to weight each artist's contribution. Because genres flow through the
message as `{Name, Rank}`, the ingest side continues to derive the stored
weight from rank via the existing `rankGenreWeight` (rank 1 -> 1.0 decaying
0.02/rank to a 0.1 floor).

## Components

### a) `internal/musicbrainz` — API client

New package mirroring `internal/spotify/client.go`.

```go
type Client struct {
    http      *http.Client
    baseURL   string // "" -> https://musicbrainz.org (test passes httptest URL)
    userAgent string
    limiter   *rate.Limiter
}

func New(baseURL, userAgent string) *Client

// SearchArtist returns the best-match MBID for a name (top result), or "" if
// there is no match.
func (c *Client) SearchArtist(ctx context.Context, name string) (mbid string, err error)

// GetArtistGenres returns genres with crowd-vote counts for an MBID.
func (c *Client) GetArtistGenres(ctx context.Context, mbid string) ([]Genre, error)

type Genre struct {
    Name  string
    Count int
}
```

Endpoints (verified live 2026-07-20):
- Search: `GET /ws/2/artist?query=artist:"<name>"&fmt=json&limit=1` ->
  `artists[0].id`, `artists[0].score`.
- Genres: `GET /ws/2/artist/<mbid>?inc=genres&fmt=json` -> `genres[] {name, count}`.

Requirements:
- Mandatory `User-Agent` header with contact info on every request
  (e.g. `heres-whats-happening/1.0 ( wlmmyers@gmail.com )`). Value supplied by
  config/caller.
- Non-2xx (including 503 rate-limit) -> error.
- `baseURL` seam so tests point at an `httptest.Server`, exactly like the
  Spotify client's `apiBase()`.

### b) `artist_genre_cache` table — migration `0017`

```sql
CREATE TABLE artist_genre_cache (
    name_key    TEXT PRIMARY KEY,           -- events.NormalizeString(artist name)
    mbid        TEXT,                        -- NULL when status = 'not_found'
    genres      JSONB NOT NULL DEFAULT '[]', -- [{"name":..,"count":..}]
    status      TEXT NOT NULL CHECK (status IN ('ok','not_found')),
    resolved_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

- Keyed by `events.NormalizeString(name)` — the same normalization ingest uses
  for `normalized_value` — so casing/diacritic variants collapse to one row and
  are shared across all users.
- Distinct from the existing curated `genres` (event-taxonomy) table; no
  overlap.
- Down migration drops the table.

sqlc queries in a new `sql/queries/artist_genre_cache.sql`:
- `GetArtistGenreCache :one` — by `name_key`.
- `UpsertArtistGenreCache :exec` — insert-or-replace all columns, setting
  `resolved_at = NOW()`.

Running `sqlc generate` also regenerates `internal/store/models.go`; commit it
alongside the generated `*.sql.go` and the query file.

### c) `internal/scraper/spotify/genres.go` — resolver

The one unit holding cache + API + TTL logic.

```go
type genreResolver struct {
    q  *store.Queries
    mb *musicbrainz.Client
}

func (r *genreResolver) Resolve(ctx context.Context, name string) ([]musicbrainz.Genre, error)
```

Logic:
1. `key = events.NormalizeString(name)`; if empty, return nil.
2. Read cache row. If present and fresh, return it:
   - `status='ok'` and `resolved_at` within **90 days** -> return `genres`.
   - `status='not_found'` and `resolved_at` within **14 days** -> return nil.
3. Otherwise resolve live:
   - `mbid, err := mb.SearchArtist`; on error -> log `continuing`, return nil
     (do not upsert — transient).
   - No match -> upsert `status='not_found'`, mbid NULL, empty genres; return nil.
   - `genres, err := mb.GetArtistGenres(mbid)`; on error -> log `continuing`,
     return nil (do not upsert).
   - Success -> upsert `status='ok'`, mbid, genres; return genres.
4. Cache read/write DB error -> log, fall back to a live fetch (read error) or
   just return the fetched genres without caching (write error); never abort.

Best-effort throughout: a MusicBrainz outage or DB hiccup yields empty genres
for that artist, never a failed scrape — mirroring the saved-tracks
`continuing` precedent at `adapter.go:102`.

## Aggregation (in the Spotify adapter)

Replaces the current `genreCount` block in `internal/scraper/spotify/adapter.go`
(around lines 114-186). For each top artist (which carries its Spotify `rank`),
call `genreResolver.Resolve(name)` and accumulate:

```
genreScore[g] += rankWeight(artist.rank) * float64(mbCount)
```

Then sort genres by `score` desc, then `name` asc, and emit
`msg.SpotifyTopGenres` as `{Name, Rank}` with `Rank` the 1-based position —
same structure the code produces today.

Note: `rankWeight` currently lives in `internal/ingest`. The scraper needs the
same curve. Extract it to a small shared helper (e.g. a tiny exported function
in a package both can import) rather than duplicating the constants, so the
artist-weight curve stays defined once.

## Rate limiting

A `golang.org/x/time/rate` limiter inside the MusicBrainz client (~1 req/sec,
small burst) whose `Wait(ctx)` gates every HTTP call. Because the resolver only
calls the client on a cache miss, a warm cache does zero waiting. On a cold
`ScrapeAll`, requests self-pace at ~1/sec; shared artists warm the cache so
later users are fast. Confirm `x/time/rate` is in `go.mod`; add it if not.

## Error handling

Best-effort at every layer; genres degrade to the current (empty) behavior
under failure, never worse:
- MB search/lookup HTTP error or 503 -> log `continuing`, no genres for that
  artist, no cache write.
- Search yields no match -> cache `not_found`, 14-day negative window.
- Cache DB read/write error -> log, live-fetch fallback / skip caching; never
  abort the scrape.

## Testing

No live MusicBrainz calls in any test.

- **`musicbrainz` client** — `httptest.Server` serving canned search + genre
  JSON (captured from the real API on 2026-07-20). Assert: MBID selection from
  search, genre+count parsing, `User-Agent` header is sent, non-2xx -> error.
- **resolver** — repo's existing Postgres test harness (serialized via the
  advisory lock) with real `store.Queries` + a stub client. Assert: fresh cache
  hit skips the client; stale `ok` and missing rows trigger fetch+upsert;
  `not_found` negative caching within 14d skips the client; DB-read error falls
  back to a live fetch.
- **adapter** — extend `internal/scraper/spotify/adapter_test.go` with a stub
  resolver returning genres per artist; assert `SpotifyTopGenres` ordering
  equals `SUM(rankWeight * mbCount)` desc then name. This replaces the existing
  assertion at `adapter_test.go:122-123`, which currently expects genres from
  the Spotify response body.

## Out of scope

- Backfilling genres for already-scraped users (next scrape cycle restores
  them).
- Any change to ingest, matching, or the interest-message schema.
- Sourcing genres from any provider other than MusicBrainz.
