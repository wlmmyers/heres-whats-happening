# MusicBrainz Genre Sourcing Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Restore `spotify_top_genre` interests by sourcing artist genres from MusicBrainz (Spotify having deprecated its artist `genres` field), resolved inline in the Spotify scraper behind a shared persistent cache.

**Architecture:** A new `internal/musicbrainz` HTTP client (rate-limited, User-Agent'd) resolves artist name → MBID → genres-with-counts. A `genreResolver` in the scraper package wraps it cache-first over a new `artist_genre_cache` table (long TTL for hits, short TTL for not-found). The Spotify adapter calls the resolver per top artist and aggregates `genreScore[g] = Σ RankWeight(artist.rank) × mbVoteCount`. The `InterestMessage` schema and the entire ingest side are untouched — genres still flow as `{Name, Rank}`.

**Tech Stack:** Go, pgx/v5, sqlc, Postgres, `golang.org/x/time/rate`, testify, httptest.

## Global Constraints

- Module path: `github.com/wmyers/heres-whats-happening`.
- Normalization for all cache keys and interest matching: `events.NormalizeString` (in `internal/events/genres.go`).
- Cache TTLs (verbatim): `ok` reused for **90 days**, `not_found` reused for **14 days**, else re-resolve.
- MusicBrainz rate limit: **~1 request/second**, mandatory `User-Agent` with contact info.
- MusicBrainz User-Agent value (verbatim): `heres-whats-happening/1.0 ( wlmmyers@gmail.com )`.
- Best-effort everywhere: any MusicBrainz or DB failure logs `(continuing)` and yields no genres for that artist — it must NEVER abort a scrape (mirrors the saved-tracks precedent at `internal/scraper/spotify/adapter.go:102`).
- sqlc: after editing any `sql/queries/*.sql` or migration, run `sqlc generate`; it regenerates `internal/store/models.go` and `internal/store/*.sql.go` — commit all of them together.
- Genre aggregation weight = `events.RankWeight(artist.rank) × float64(mbVoteCount)`; genres are ranked by that score desc, then name asc; `SpotifyTopGenres` carries only `{Name, Rank}` (1-based).

---

### Task 1: MusicBrainz API client

**Files:**
- Create: `internal/musicbrainz/client.go`
- Test: `internal/musicbrainz/client_test.go`
- Modify: `go.mod` / `go.sum` (add `golang.org/x/time/rate`)

**Interfaces:**
- Produces (consumed by Tasks 4 and 5):
  - `type Genre struct { Name string `json:"name"`; Count int `json:"count"` }`
  - `func New(baseURL, userAgent string) *Client`
  - `func (c *Client) SearchArtist(ctx context.Context, name string) (mbid string, err error)` — returns `""` (no error) when there is no match.
  - `func (c *Client) GetArtistGenres(ctx context.Context, mbid string) ([]Genre, error)`

- [ ] **Step 1: Add the rate dependency**

Run:
```bash
go get golang.org/x/time/rate
```
Expected: `go.mod` gains a `golang.org/x/time` require line (may be `// indirect` until used).

- [ ] **Step 2: Write the failing test**

Create `internal/musicbrainz/client_test.go`:
```go
package musicbrainz_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/musicbrainz"
)

const testUA = "hwh-test/1.0 ( test@example.com )"

func TestSearchArtist_ReturnsTopMBID(t *testing.T) {
	var gotUA, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		gotQuery = r.URL.Query().Get("query")
		require.Equal(t, "/ws/2/artist", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"artists":[{"id":"96855c21-b832-4366-ba12-0d2330c36a86","name":"Phoebe Bridgers","score":100}]}`))
	}))
	defer srv.Close()

	c := musicbrainz.New(srv.URL, testUA)
	mbid, err := c.SearchArtist(context.Background(), "Phoebe Bridgers")
	require.NoError(t, err)
	require.Equal(t, "96855c21-b832-4366-ba12-0d2330c36a86", mbid)
	require.Equal(t, testUA, gotUA)
	require.Equal(t, `artist:"Phoebe Bridgers"`, gotQuery)
}

func TestSearchArtist_NoMatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"artists":[]}`))
	}))
	defer srv.Close()

	c := musicbrainz.New(srv.URL, testUA)
	mbid, err := c.SearchArtist(context.Background(), "Nonexistent Band 9000")
	require.NoError(t, err)
	require.Equal(t, "", mbid)
}

func TestGetArtistGenres_ParsesCounts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/ws/2/artist/96855c21-b832-4366-ba12-0d2330c36a86", r.URL.Path)
		require.Equal(t, "genres", r.URL.Query().Get("inc"))
		_, _ = w.Write([]byte(`{"genres":[{"name":"indie folk","count":9},{"name":"indie rock","count":8}]}`))
	}))
	defer srv.Close()

	c := musicbrainz.New(srv.URL, testUA)
	gs, err := c.GetArtistGenres(context.Background(), "96855c21-b832-4366-ba12-0d2330c36a86")
	require.NoError(t, err)
	require.Equal(t, []musicbrainz.Genre{{Name: "indie folk", Count: 9}, {Name: "indie rock", Count: 8}}, gs)
}

func TestGet_Non2xxIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("rate limited"))
	}))
	defer srv.Close()

	c := musicbrainz.New(srv.URL, testUA)
	_, err := c.SearchArtist(context.Background(), "X")
	require.Error(t, err)
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/musicbrainz/...`
Expected: FAIL — package/`New`/`SearchArtist`/`GetArtistGenres` undefined.

- [ ] **Step 4: Write the client**

Create `internal/musicbrainz/client.go`:
```go
// Package musicbrainz is a small read-only client for the MusicBrainz web
// service, used to source artist genres (Spotify having deprecated its own
// artist genres field). One instance is safe for concurrent use and shares a
// single ~1 req/sec rate limiter, as MusicBrainz requires.
package musicbrainz

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"golang.org/x/time/rate"
)

const defaultBaseURL = "https://musicbrainz.org"

// Genre is one crowd-tagged genre for an artist, with its vote count.
type Genre struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// Client calls the MusicBrainz web service.
type Client struct {
	http      *http.Client
	baseURL   string
	userAgent string
	limiter   *rate.Limiter
}

// New builds a Client. If baseURL is "", the production MusicBrainz host is
// used; tests pass an httptest.Server URL. userAgent MUST identify the app and
// a contact — MusicBrainz rejects requests without one.
func New(baseURL, userAgent string) *Client {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return &Client{
		http:      &http.Client{Timeout: 15 * time.Second},
		baseURL:   baseURL,
		userAgent: userAgent,
		limiter:   rate.NewLimiter(rate.Every(time.Second), 1),
	}
}

// get issues a rate-limited GET and decodes the JSON body into out.
func (c *Client) get(ctx context.Context, path string, out any) error {
	if err := c.limiter.Wait(ctx); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("musicbrainz %d: %s", resp.StatusCode, string(body))
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode: %w", err)
	}
	return nil
}

// SearchArtist returns the MBID of the best-matching artist for name, or ""
// when there is no match.
func (c *Client) SearchArtist(ctx context.Context, name string) (string, error) {
	q := url.Values{}
	q.Set("query", `artist:"`+name+`"`)
	q.Set("fmt", "json")
	q.Set("limit", "1")
	var payload struct {
		Artists []struct {
			ID string `json:"id"`
		} `json:"artists"`
	}
	if err := c.get(ctx, "/ws/2/artist?"+q.Encode(), &payload); err != nil {
		return "", err
	}
	if len(payload.Artists) == 0 {
		return "", nil
	}
	return payload.Artists[0].ID, nil
}

// GetArtistGenres returns the genres (with vote counts) for an MBID.
func (c *Client) GetArtistGenres(ctx context.Context, mbid string) ([]Genre, error) {
	q := url.Values{}
	q.Set("inc", "genres")
	q.Set("fmt", "json")
	var payload struct {
		Genres []Genre `json:"genres"`
	}
	if err := c.get(ctx, "/ws/2/artist/"+mbid+"?"+q.Encode(), &payload); err != nil {
		return nil, err
	}
	return payload.Genres, nil
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/musicbrainz/...`
Expected: PASS (4 tests). Then `go mod tidy` so `golang.org/x/time` is a direct require.

- [ ] **Step 6: Commit**

```bash
git add internal/musicbrainz/ go.mod go.sum
git commit -m "Add MusicBrainz client for artist genre lookup"
```

---

### Task 2: artist_genre_cache table and queries

**Files:**
- Create: `sql/migrations/0017_artist_genre_cache.up.sql`
- Create: `sql/migrations/0017_artist_genre_cache.down.sql`
- Create: `sql/queries/artist_genre_cache.sql`
- Modify (generated): `internal/store/models.go`, `internal/store/artist_genre_cache.sql.go`

**Interfaces:**
- Produces (consumed by Task 4), generated by sqlc:
  - `store.ArtistGenreCache` row: `NameKey string`, `Mbid *string`, `Genres []byte`, `Status string`, `ResolvedAt pgtype.Timestamptz`.
  - `func (q *Queries) GetArtistGenreCache(ctx, nameKey string) (ArtistGenreCache, error)` — `pgx.ErrNoRows` on miss.
  - `func (q *Queries) UpsertArtistGenreCache(ctx, arg UpsertArtistGenreCacheParams) error` where params are `NameKey string`, `Mbid *string`, `Genres []byte`, `Status string`.

- [ ] **Step 1: Write the migration**

Create `sql/migrations/0017_artist_genre_cache.up.sql`:
```sql
CREATE TABLE artist_genre_cache (
    name_key    TEXT PRIMARY KEY,           -- events.NormalizeString(artist name)
    mbid        TEXT,                        -- NULL when status = 'not_found'
    genres      JSONB NOT NULL DEFAULT '[]'::jsonb, -- [{"name":..,"count":..}]
    status      TEXT NOT NULL CHECK (status IN ('ok', 'not_found')),
    resolved_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

Create `sql/migrations/0017_artist_genre_cache.down.sql`:
```sql
DROP TABLE artist_genre_cache;
```

- [ ] **Step 2: Write the queries**

Create `sql/queries/artist_genre_cache.sql`:
```sql
-- name: GetArtistGenreCache :one
SELECT name_key, mbid, genres, status, resolved_at
FROM artist_genre_cache
WHERE name_key = $1;

-- name: UpsertArtistGenreCache :exec
INSERT INTO artist_genre_cache (name_key, mbid, genres, status, resolved_at)
VALUES ($1, $2, $3, $4, NOW())
ON CONFLICT (name_key) DO UPDATE SET
    mbid        = EXCLUDED.mbid,
    genres      = EXCLUDED.genres,
    status      = EXCLUDED.status,
    resolved_at = NOW();
```

- [ ] **Step 3: Generate sqlc code**

Run: `sqlc generate`
Expected: creates `internal/store/artist_genre_cache.sql.go` and adds `ArtistGenreCache` to `internal/store/models.go`. Confirm `Mbid` is `*string` and `Genres` is `[]byte`:

Run: `grep -n "ArtistGenreCache" internal/store/models.go`
Expected: a struct with `NameKey`, `Mbid *string`, `Genres []byte`, `Status`, `ResolvedAt`.

- [ ] **Step 4: Verify it compiles and the schema applies**

Run: `go build ./...`
Expected: builds clean.

Run: `go test ./internal/store/... 2>&1 | tail -5` (if the repo runs migrations in a DB test; otherwise skip)
Expected: no migration error. If there is no store test, this step is just the build above.

- [ ] **Step 5: Commit**

```bash
git add sql/migrations/0017_artist_genre_cache.up.sql sql/migrations/0017_artist_genre_cache.down.sql sql/queries/artist_genre_cache.sql internal/store/
git commit -m "Add artist_genre_cache table and queries"
```

---

### Task 3: Extract RankWeight to the events package

Both ingest (existing) and the scraper (Task 5) need the artist rank→weight curve. Move it to `events` (already imported by both) as the single source of truth.

**Files:**
- Modify: `internal/events/interest.go` (add `RankWeight`)
- Create: `internal/events/weight_test.go` (moved test)
- Modify: `internal/ingest/interests.go` (delete private `rankWeight`, call `events.RankWeight`)
- Modify: `internal/ingest/weight_test.go` (remove the moved `TestRankWeight`)

**Interfaces:**
- Produces (consumed by Task 5): `func events.RankWeight(rank int) float64`.

- [ ] **Step 1: Add the moved test (failing)**

Create `internal/events/weight_test.go`:
```go
package events_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/events"
)

func TestRankWeight(t *testing.T) {
	cases := []struct {
		rank int
		want float64
	}{
		{rank: 1, want: 1.0},
		{rank: 50, want: 0.6},
		{rank: 51, want: 0.6}, // clamped at the 0.6 floor past the 50-item list
		{rank: 100, want: 0.6},
		{rank: 0, want: 1.0},  // guard: non-positive ranks get full weight
		{rank: -5, want: 1.0}, // guard
	}
	for _, c := range cases {
		require.InDelta(t, c.want, events.RankWeight(c.rank), 1e-9, "rank %d", c.rank)
	}

	prev := events.RankWeight(1)
	for r := 2; r <= 50; r++ {
		w := events.RankWeight(r)
		require.LessOrEqual(t, w, prev, "rank %d should not exceed rank %d", r, r-1)
		require.GreaterOrEqual(t, w, 0.6, "rank %d below floor", r)
		require.LessOrEqual(t, w, 1.0, "rank %d above 1.0", r)
		prev = w
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/events/... -run TestRankWeight`
Expected: FAIL — `events.RankWeight` undefined.

- [ ] **Step 3: Add RankWeight to events**

In `internal/events/interest.go`, add after the `SpotifyTopItem` type:
```go
// RankWeight maps a 1-based rank to an interest weight: rank 1 -> 1.0, ramping
// down linearly to 0.6 at rank 50 (the size of the Spotify top-artist and
// top-track lists), then holding at 0.6 for lower ranks. Shared by the ingest
// weighting of artists/track-artists and the scraper's genre aggregation.
func RankWeight(rank int) float64 {
	const (
		maxRank   = 50
		minWeight = 0.6
	)
	if rank <= 1 {
		return 1.0
	}
	w := 1.0 - float64(rank-1)*(1.0-minWeight)/(maxRank-1)
	if w < minWeight {
		return minWeight
	}
	return w
}
```

- [ ] **Step 4: Point ingest at events.RankWeight and delete the private copy**

In `internal/ingest/interests.go`:
- Replace each of the three `rankWeight(item.Rank)` call sites (lines ~63, ~81, ~99) with `events.RankWeight(item.Rank)`.
- Delete the private `func rankWeight(rank int) float64 { ... }` block (lines ~151-168, the comment and function). Leave `rankGenreWeight` intact.

In `internal/ingest/weight_test.go`:
- Delete the entire `TestRankWeight` function (lines ~9-35). Leave `TestRankGenreWeight` and the imports intact (both `testing` and `require` are still used by `TestRankGenreWeight`).

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/events/... ./internal/ingest/...`
Expected: PASS. `events` gains `TestRankWeight`; ingest keeps `TestRankGenreWeight`; the genre-weighting behavior is unchanged.

- [ ] **Step 6: Commit**

```bash
git add internal/events/interest.go internal/events/weight_test.go internal/ingest/interests.go internal/ingest/weight_test.go
git commit -m "Extract RankWeight to events for scraper reuse"
```

---

### Task 4: Genre resolver (cache-first, best-effort)

**Files:**
- Create: `internal/scraper/spotify/genres.go`
- Test: `internal/scraper/spotify/genres_test.go`

**Interfaces:**
- Consumes: `musicbrainz.Genre`, `musicbrainz.Client` (Task 1); `store.Queries.GetArtistGenreCache` / `UpsertArtistGenreCache` (Task 2); `events.NormalizeString`.
- Produces (consumed by Task 5):
  - `type GenreResolver interface { Resolve(ctx context.Context, name string) []musicbrainz.Genre }`
  - `func NewGenreResolver(q *store.Queries, mb *musicbrainz.Client) GenreResolver`
  - `func DefaultGenreResolver(q *store.Queries) GenreResolver` — builds a resolver over `musicbrainz.New("", <User-Agent const>)`.

- [ ] **Step 1: Write the failing test**

Create `internal/scraper/spotify/genres_test.go` (white-box — `package spotify` — to inject the stub client):
```go
package spotify

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/musicbrainz"
	"github.com/wmyers/heres-whats-happening/internal/store"
	"github.com/wmyers/heres-whats-happening/internal/testdb"
)

// stubMB implements mbClient with canned responses and a call counter.
type stubMB struct {
	searchID    map[string]string
	genres      map[string][]musicbrainz.Genre
	searchErr   error
	searchCalls int
}

func (s *stubMB) SearchArtist(ctx context.Context, name string) (string, error) {
	s.searchCalls++
	if s.searchErr != nil {
		return "", s.searchErr
	}
	return s.searchID[name], nil
}

func (s *stubMB) GetArtistGenres(ctx context.Context, mbid string) ([]musicbrainz.Genre, error) {
	return s.genres[mbid], nil
}

func backdate(t *testing.T, pool testdb.Pool, key string, at time.Time) {
	t.Helper()
	_, err := pool.Exec(context.Background(),
		"UPDATE artist_genre_cache SET resolved_at = $1 WHERE name_key = $2", at, key)
	require.NoError(t, err)
}

func TestResolve_MissFetchesThenCacheHitSkipsClient(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()

	mb := &stubMB{
		searchID: map[string]string{"Phoebe Bridgers": "mbid-pb"},
		genres:   map[string][]musicbrainz.Genre{"mbid-pb": {{Name: "indie folk", Count: 9}}},
	}
	r := &genreResolver{q: q, mb: mb}

	got := r.Resolve(ctx, "Phoebe Bridgers")
	require.Equal(t, []musicbrainz.Genre{{Name: "indie folk", Count: 9}}, got)
	require.Equal(t, 1, mb.searchCalls)

	// Second call served from cache — client not touched again.
	got = r.Resolve(ctx, "Phoebe Bridgers")
	require.Equal(t, []musicbrainz.Genre{{Name: "indie folk", Count: 9}}, got)
	require.Equal(t, 1, mb.searchCalls)
}

func TestResolve_NotFoundIsNegativeCached(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()

	mb := &stubMB{searchID: map[string]string{}} // no match for anyone
	r := &genreResolver{q: q, mb: mb}

	require.Nil(t, r.Resolve(ctx, "Nonexistent Band 9000"))
	require.Equal(t, 1, mb.searchCalls)

	// Within the 14-day window → served from the negative cache.
	require.Nil(t, r.Resolve(ctx, "Nonexistent Band 9000"))
	require.Equal(t, 1, mb.searchCalls)
}

func TestResolve_StaleOKRefetches(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()

	mb := &stubMB{
		searchID: map[string]string{"Radiohead": "mbid-rh"},
		genres:   map[string][]musicbrainz.Genre{"mbid-rh": {{Name: "art rock", Count: 5}}},
	}
	r := &genreResolver{q: q, mb: mb}

	require.NotNil(t, r.Resolve(ctx, "Radiohead"))
	require.Equal(t, 1, mb.searchCalls)

	// Age the row past the 90-day OK TTL → next call re-resolves.
	backdate(t, pool, "radiohead", time.Now().Add(-91*24*time.Hour))
	require.NotNil(t, r.Resolve(ctx, "Radiohead"))
	require.Equal(t, 2, mb.searchCalls)
}

func TestResolve_TransientSearchErrorNotCached(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()

	mb := &stubMB{searchErr: context.DeadlineExceeded}
	r := &genreResolver{q: q, mb: mb}

	require.Nil(t, r.Resolve(ctx, "Boygenius"))
	// Nothing cached → a retry hits the client again.
	require.Nil(t, r.Resolve(ctx, "Boygenius"))
	require.Equal(t, 2, mb.searchCalls)
}
```

> Note: `TestResolve_StaleOKRefetches` assumes `events.NormalizeString("Radiohead") == "radiohead"`. If your normalization differs, compute the key with `events.NormalizeString` in the `backdate` call instead of the literal.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/scraper/spotify/ -run TestResolve`
Expected: FAIL — `genreResolver`, `mbClient` undefined.

- [ ] **Step 3: Write the resolver**

Create `internal/scraper/spotify/genres.go`:
```go
package spotify

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/wmyers/heres-whats-happening/internal/events"
	"github.com/wmyers/heres-whats-happening/internal/musicbrainz"
	"github.com/wmyers/heres-whats-happening/internal/store"
)

// Freshness windows for the shared artist_genre_cache.
const (
	genreOKTTL       = 90 * 24 * time.Hour
	genreNotFoundTTL = 14 * 24 * time.Hour

	// genreUserAgent identifies the app to MusicBrainz (required); includes a
	// contact per their policy.
	genreUserAgent = "heres-whats-happening/1.0 ( wlmmyers@gmail.com )"
)

// mbClient is the subset of *musicbrainz.Client the resolver uses (stubbed in tests).
type mbClient interface {
	SearchArtist(ctx context.Context, name string) (string, error)
	GetArtistGenres(ctx context.Context, mbid string) ([]musicbrainz.Genre, error)
}

// GenreResolver resolves an artist name to its genres. The adapter depends on
// this interface so tests can stub it.
type GenreResolver interface {
	Resolve(ctx context.Context, name string) []musicbrainz.Genre
}

type genreResolver struct {
	q  *store.Queries
	mb mbClient
}

// NewGenreResolver builds a resolver over the shared cache and a MusicBrainz client.
func NewGenreResolver(q *store.Queries, mb *musicbrainz.Client) GenreResolver {
	return &genreResolver{q: q, mb: mb}
}

// DefaultGenreResolver builds the production resolver with the default
// MusicBrainz host and app User-Agent.
func DefaultGenreResolver(q *store.Queries) GenreResolver {
	return &genreResolver{q: q, mb: musicbrainz.New("", genreUserAgent)}
}

// Resolve returns an artist's genres, cache-first. Best-effort: any MusicBrainz
// or DB failure logs and yields no genres for that artist rather than erroring —
// genres are a soft signal and must never abort a scrape.
func (r *genreResolver) Resolve(ctx context.Context, name string) []musicbrainz.Genre {
	key := events.NormalizeString(name)
	if key == "" {
		return nil
	}

	row, err := r.q.GetArtistGenreCache(ctx, key)
	switch {
	case err == nil:
		if fresh(row) {
			return decodeGenres(row.Genres)
		}
		// stale → fall through and re-resolve
	case errors.Is(err, pgx.ErrNoRows):
		// miss → resolve
	default:
		// Cache read failed → try a live fetch without caching.
		log.Printf("scrape spotify: genre cache read %q (continuing): %v", name, err)
		return r.fetchLive(ctx, name)
	}

	return r.resolveAndCache(ctx, key, name)
}

func fresh(row store.ArtistGenreCache) bool {
	age := time.Since(row.ResolvedAt.Time)
	switch row.Status {
	case "ok":
		return age < genreOKTTL
	case "not_found":
		return age < genreNotFoundTTL
	default:
		return false
	}
}

// resolveAndCache does a live lookup and upserts the result.
func (r *genreResolver) resolveAndCache(ctx context.Context, key, name string) []musicbrainz.Genre {
	mbid, err := r.mb.SearchArtist(ctx, name)
	if err != nil {
		log.Printf("scrape spotify: mb search %q (continuing): %v", name, err)
		return nil // transient — do not cache
	}
	if mbid == "" {
		r.upsert(ctx, key, nil, nil, "not_found")
		return nil
	}
	genres, err := r.mb.GetArtistGenres(ctx, mbid)
	if err != nil {
		log.Printf("scrape spotify: mb genres %q (continuing): %v", name, err)
		return nil // transient — do not cache
	}
	r.upsert(ctx, key, &mbid, genres, "ok")
	return genres
}

// fetchLive looks up without touching the cache (used when the cache read failed).
func (r *genreResolver) fetchLive(ctx context.Context, name string) []musicbrainz.Genre {
	mbid, err := r.mb.SearchArtist(ctx, name)
	if err != nil || mbid == "" {
		return nil
	}
	genres, err := r.mb.GetArtistGenres(ctx, mbid)
	if err != nil {
		return nil
	}
	return genres
}

func (r *genreResolver) upsert(ctx context.Context, key string, mbid *string, genres []musicbrainz.Genre, status string) {
	blob := []byte("[]")
	if len(genres) > 0 {
		b, err := json.Marshal(genres)
		if err != nil {
			log.Printf("scrape spotify: marshal genres %q (continuing): %v", key, err)
			return
		}
		blob = b
	}
	if err := r.q.UpsertArtistGenreCache(ctx, store.UpsertArtistGenreCacheParams{
		NameKey: key,
		Mbid:    mbid,
		Genres:  blob,
		Status:  status,
	}); err != nil {
		log.Printf("scrape spotify: genre cache write %q (continuing): %v", key, err)
	}
}

func decodeGenres(blob []byte) []musicbrainz.Genre {
	if len(blob) == 0 {
		return nil
	}
	var gs []musicbrainz.Genre
	if err := json.Unmarshal(blob, &gs); err != nil {
		return nil
	}
	return gs
}
```

- [ ] **Step 4: Confirm the testdb helper type used in the test**

Run: `grep -n "func MustOpen\|type Pool" internal/testdb/*.go`
Expected: `MustOpen(t)` returns a pool type with an `Exec(ctx, sql, args...)` method. If the returned type is `*pgxpool.Pool` (not a local `Pool` alias), change `backdate`'s signature in the test from `pool testdb.Pool` to `pool *pgxpool.Pool` and import `github.com/jackc/pgx/v5/pgxpool`.

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/scraper/spotify/ -run TestResolve`
Expected: PASS (4 tests).

- [ ] **Step 6: Commit**

```bash
git add internal/scraper/spotify/genres.go internal/scraper/spotify/genres_test.go
git commit -m "Add cache-first MusicBrainz genre resolver"
```

---

### Task 5: Wire the resolver into the adapter and aggregate genres

**Files:**
- Modify: `internal/scraper/spotify/adapter.go` (struct field, `NewAdapter` signature, genre aggregation)
- Modify: `internal/scraper/spotify/adapter_test.go` (stub resolver; genre assertions)
- Modify: `cmd/app/main.go:252` (pass resolver)
- Modify: `internal/http/handlers/spotify.go:155` (pass resolver)

**Interfaces:**
- Consumes: `GenreResolver`, `DefaultGenreResolver` (Task 4); `events.RankWeight` (Task 3).
- Produces: `func NewAdapter(q *store.Queries, c *crypto.Cipher, client *spotify.Client, pub Publisher, queueURL string, genres GenreResolver) *Adapter`.

- [ ] **Step 1: Update the adapter test first (failing)**

In `internal/scraper/spotify/adapter_test.go`:

Add imports (in the existing import block): `"context"` (if not already present) and `"github.com/wmyers/heres-whats-happening/internal/musicbrainz"`.

Add a stub resolver near `fakePublisher`:
```go
type stubGenreResolver struct {
	byName map[string][]musicbrainz.Genre
}

func (s stubGenreResolver) Resolve(ctx context.Context, name string) []musicbrainz.Genre {
	return s.byName[name]
}
```

In `TestScrapeOne_PublishesInterestMessage` (the artists returned are "Phoebe Bridgers" rank 1 and "MUNA" rank 2), replace the `NewAdapter` call at line ~105:
```go
	resolver := stubGenreResolver{byName: map[string][]musicbrainz.Genre{
		"Phoebe Bridgers": {{Name: "indie folk", Count: 9}, {Name: "indie rock", Count: 8}},
		"MUNA":            {{Name: "indie pop", Count: 5}},
	}}
	adapter := spotifyscrape.NewAdapter(q, cipher, client, pub, "http://localhost/interests-queue", resolver)
```

Replace the two genre assertions at lines ~122-123 with score-ordered expectations
(`indie folk = RankWeight(1)*9 = 9`, `indie rock = RankWeight(1)*8 = 8`,
`indie pop = RankWeight(2)*5 ≈ 4.96` → folk, rock, pop):
```go
	require.Len(t, msg.SpotifyTopGenres, 3)
	require.Equal(t, "indie folk", msg.SpotifyTopGenres[0].Name)
	require.Equal(t, 1, msg.SpotifyTopGenres[0].Rank)
	require.Equal(t, "indie rock", msg.SpotifyTopGenres[1].Name)
	require.Equal(t, 2, msg.SpotifyTopGenres[1].Rank)
	require.Equal(t, "indie pop", msg.SpotifyTopGenres[2].Name)
	require.Equal(t, 3, msg.SpotifyTopGenres[2].Rank)
```

In the other three tests, add a resolver arg (an empty stub — those tests don't assert genres) to each `NewAdapter` call at lines ~177, ~251, ~317:
```go
	adapter := spotifyscrape.NewAdapter(q, cipher, client, pub, "http://localhost/q", stubGenreResolver{})
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/scraper/spotify/ -run TestScrapeOne`
Expected: FAIL — `NewAdapter` arg count mismatch / genre assertions unmet.

- [ ] **Step 3: Update the adapter**

In `internal/scraper/spotify/adapter.go`:

Add the field to the `Adapter` struct:
```go
type Adapter struct {
	q        *store.Queries
	cipher   *crypto.Cipher
	client   *spotify.Client
	pub      Publisher
	queueURL string
	genres   GenreResolver
}
```

Update the constructor:
```go
func NewAdapter(q *store.Queries, c *crypto.Cipher, client *spotify.Client, pub Publisher, queueURL string, genres GenreResolver) *Adapter {
	return &Adapter{q: q, cipher: c, client: client, pub: pub, queueURL: queueURL, genres: genres}
}
```

Replace the top-artists loop that built `genreCount` (the block at lines ~113-123) with one that also aggregates genre scores via the resolver:
```go
	msg.SpotifyTopArtists = make([]events.SpotifyTopItem, 0, len(artists))
	genreScore := map[string]float64{}
	for i, ar := range artists {
		rank := i + 1
		msg.SpotifyTopArtists = append(msg.SpotifyTopArtists, events.SpotifyTopItem{
			Name: ar.Name,
			Rank: rank,
		})
		for _, g := range a.genres.Resolve(ctx, ar.Name) {
			genreScore[g.Name] += events.RankWeight(rank) * float64(g.Count)
		}
	}
```

Replace the old genre ranking block (the `type gc struct`, `gs` slice, sort, and `SpotifyTopGenres` build at lines ~166-186) with score-based ranking:
```go
	type scored struct {
		name  string
		score float64
	}
	ranked := make([]scored, 0, len(genreScore))
	for name, score := range genreScore {
		ranked = append(ranked, scored{name, score})
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		return ranked[i].name < ranked[j].name
	})
	msg.SpotifyTopGenres = make([]events.SpotifyTopItem, 0, len(ranked))
	for i, g := range ranked {
		msg.SpotifyTopGenres = append(msg.SpotifyTopGenres, events.SpotifyTopItem{
			Name: g.name,
			Rank: i + 1,
		})
	}
```

(The `spotify.Artist.Genres` field is now unused by the adapter — leave it; the client still parses it harmlessly.)

- [ ] **Step 4: Update the two production call sites**

In `cmd/app/main.go`, replace the `NewAdapter` call at line ~252:
```go
	adapter := spotifyscrape.NewAdapter(q, cipher, spClient, qClient, cfg.InterestsQueueURL, spotifyscrape.DefaultGenreResolver(q))
```
(No new import needed — `spotifyscrape` is already imported.)

In `internal/http/handlers/spotify.go`, replace the `NewAdapter` call at line ~155:
```go
			adapter := spotifyscrape.NewAdapter(q, cipher, client, pub, queueURL, spotifyscrape.DefaultGenreResolver(q))
```

- [ ] **Step 5: Run the full affected test set**

Run: `go test ./internal/scraper/spotify/... ./cmd/... ./internal/http/...`
Expected: PASS. Then a whole-repo build:

Run: `go build ./...`
Expected: builds clean.

- [ ] **Step 6: Commit**

```bash
git add internal/scraper/spotify/adapter.go internal/scraper/spotify/adapter_test.go cmd/app/main.go internal/http/handlers/spotify.go
git commit -m "Source Spotify genre interests from MusicBrainz"
```

---

## Final verification

- [ ] Run `go build ./...` — clean.
- [ ] Run `go test ./...` — all pass (DB tests serialize via the existing advisory lock).
- [ ] Run `go vet ./...` — clean.
- [ ] Sanity: `grep -rn "ar.Genres" internal/scraper/spotify/adapter.go` returns nothing (the deprecated Spotify genre field is no longer read for aggregation).
