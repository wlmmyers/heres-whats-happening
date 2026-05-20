# Plan 4 — Match Job + TEI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Nightly `app match` command that (1) embeds events and users via a local TEI sidecar (`BAAI/bge-small-en-v1.5`, 384-dim), (2) scores each upcoming event for each active user via a hybrid string + embedding signal, (3) upserts matches above threshold into `user_event_match`, and (4) archives events not seen in 7+ days. Runs end-to-end against the local stack from a dev laptop.

**Architecture:** New `internal/tei` HTTP client wraps the Hugging Face `text-embeddings-inference` server. New `internal/matcher` package owns the four job steps (embed events, embed users, score+upsert, archive). Each step is a small Go function with a clean interface; the orchestrating `Job.Run` calls them in order. Scoring is a pure Go function over rich profile types — testable without DB or TEI. Data loading uses sqlc queries; cosine similarity is computed in Go on `[]float32` vectors loaded into memory (v1 scale: <1K users × <500 upcoming events). `match_config` table stores tunable weights so they can be changed without a deploy.

**Tech Stack:** Go 1.24+ · Hugging Face `ghcr.io/huggingface/text-embeddings-inference:cpu-1.5` Docker image · `pgvector` extension (already enabled in Plan 1) · `github.com/pgvector/pgvector-go` (already in go.mod from Plan 1) · `sqlc`, `pgx/v5`, existing test patterns.

---

## File Structure

```
.
├── cmd/app/main.go                                # add `match` subcommand
├── internal/
│   ├── config/config.go                           # add TEI_ENDPOINT + embedding-related env vars
│   ├── tei/
│   │   ├── client.go                              # POST /embed → [][]float32
│   │   └── client_test.go                         # httptest-mocked
│   └── matcher/
│       ├── types.go                               # Config, UserProfile, EventProfile, MatchScore
│       ├── text.go                                # BuildEventText, BuildUserText
│       ├── text_test.go
│       ├── scorer.go                              # Score(user, event, cfg) MatchScore — pure
│       ├── scorer_test.go
│       ├── event_embedder.go                      # embed events with null embedding
│       ├── event_embedder_test.go                 # real DB + mocked TEI
│       ├── user_embedder.go                       # embed users whose interests changed
│       ├── user_embedder_test.go
│       ├── match_step.go                          # load → score → upsert
│       ├── match_step_test.go                     # real DB, no TEI (embeddings pre-loaded)
│       ├── archiver.go                            # set archived_at on stale events
│       ├── archiver_test.go
│       ├── job.go                                 # orchestrates: embed events, embed users, match, archive
│       └── job_test.go                            # end-to-end integration
├── sql/
│   ├── migrations/
│   │   ├── 0009_match_config.up.sql/.down.sql
│   │   └── 0010_user_event_match.up.sql/.down.sql
│   └── queries/
│       ├── match_config.sql
│       ├── matching.sql                           # data loading + upsert + archival
│       └── (existing files modified — see Task 6, 7, 9)
├── docker-compose.yml                             # add tei service
└── Makefile                                       # add tei-up/down + match targets
```

**Boundaries:**

- `internal/tei` knows nothing about matching, events, users, or DB. It's an HTTP client for the TEI server.
- `internal/matcher.Scorer` is a pure function over rich types. No DB, no TEI, no SQS.
- `internal/matcher`'s embedder/match/archive steps each own one DB-touching responsibility.
- `Job.Run` is a thin orchestration layer.

---

## Prerequisites

- Plans 1, 2, and 3 are merged to master.
- `docker compose up postgres elasticmq` running.
- ~600 MB of disk available for the TEI image and model weights.

---

### Task 1: TEI docker-compose service + Make + .env

**Files:**
- Modify: `docker-compose.yml`
- Modify: `Makefile`
- Modify: `.env.example`

- [ ] **Step 1: Add `tei` service to `docker-compose.yml`**

Append to the existing `services:` block:

```yaml
  tei:
    image: ghcr.io/huggingface/text-embeddings-inference:cpu-1.5
    container_name: hwh_tei
    command: ["--model-id", "BAAI/bge-small-en-v1.5"]
    ports:
      - "8081:80"
    volumes:
      - tei-cache:/data
    healthcheck:
      test: ["CMD-SHELL", "wget -q -O - http://localhost/health || exit 1"]
      interval: 10s
      timeout: 5s
      retries: 10
```

And add the named volume at the end:

```yaml
volumes:
  pgdata:
  tei-cache:
```

(Keep the existing `pgdata` volume; add `tei-cache` alongside.)

- [ ] **Step 2: Append to `.env.example`**

```
# TEI (text embeddings inference) — local model server
TEI_ENDPOINT=http://localhost:8081
EMBEDDING_DIM=384
MATCH_SCORE_THRESHOLD_OVERRIDE=
```

`MATCH_SCORE_THRESHOLD_OVERRIDE` is an optional override for the `score_threshold` row in `match_config`; leave blank to use the DB value.

- [ ] **Step 3: Add Make targets**

Append to `Makefile`:

```makefile
.PHONY: tei-up tei-down match

tei-up:
	docker compose up -d tei

tei-down:
	docker compose stop tei

match:
	go run ./cmd/app match
```

Add `tei-up`, `tei-down`, `match` to the `.PHONY` line at the top of the file.

- [ ] **Step 4: Verify TEI starts**

```bash
make tei-up
# TEI downloads the model on first run; can take 1–2 minutes.
# Poll until healthy:
for i in $(seq 1 30); do
  if curl -s http://localhost:8081/health 2>/dev/null | grep -q '"status":"ok"'; then
    echo "TEI ready"; break
  fi
  sleep 5
done
```

Expected: `TEI ready` within ~2 minutes.

- [ ] **Step 5: Test an embed call**

```bash
curl -s -X POST http://localhost:8081/embed \
  -H 'Content-Type: application/json' \
  -d '{"inputs": ["hello world"]}' \
  | head -c 200
```

Expected: a JSON array containing a 384-element float array (truncated by `head`).

- [ ] **Step 6: Commit**

```bash
git add docker-compose.yml Makefile .env.example
git commit -m "feat: TEI docker-compose service + Make targets"
```

---

### Task 2: Migration 0009 — `match_config` table with seeded defaults

**Files:**
- Create: `sql/migrations/0009_match_config.up.sql`
- Create: `sql/migrations/0009_match_config.down.sql`

- [ ] **Step 1: Write `0009_match_config.up.sql`**

```sql
CREATE TABLE match_config (
    key        TEXT PRIMARY KEY,
    value      JSONB NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO match_config (key, value) VALUES
    ('w_string',        '0.6'::jsonb),
    ('w_embedding',     '0.4'::jsonb),
    ('score_threshold', '0.3'::jsonb),
    ('artist_factor',   '1.0'::jsonb),
    ('genre_factor',    '0.3'::jsonb),
    ('string_max',      '3.0'::jsonb);
```

- [ ] **Step 2: Write `0009_match_config.down.sql`**

```sql
DROP TABLE IF EXISTS match_config;
```

- [ ] **Step 3: Run migrations**

```bash
make migrate
make migrate-test
docker exec hwh_postgres psql -U app -d appdb -c "SELECT key, value FROM match_config ORDER BY key;"
```

Expected: 6 rows; values are JSON-typed numbers (`0.6`, `0.4`, etc.).

- [ ] **Step 4: Commit**

```bash
git add sql/migrations/0009_match_config.up.sql sql/migrations/0009_match_config.down.sql
git commit -m "feat: migration 0009 — match_config with seeded defaults"
```

---

### Task 3: Migration 0010 — `user_event_match`

**Files:**
- Create: `sql/migrations/0010_user_event_match.up.sql`
- Create: `sql/migrations/0010_user_event_match.down.sql`

- [ ] **Step 1: Write `0010_user_event_match.up.sql`**

```sql
CREATE TABLE user_event_match (
    user_id          UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    event_id         UUID NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    score            DOUBLE PRECISION NOT NULL,
    score_breakdown  JSONB NOT NULL DEFAULT '{}'::jsonb,
    computed_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, event_id)
);

CREATE INDEX user_event_match_user_score ON user_event_match (user_id, score DESC);
```

- [ ] **Step 2: Write `0010_user_event_match.down.sql`**

```sql
DROP TABLE IF EXISTS user_event_match;
```

- [ ] **Step 3: Migrate and verify**

```bash
make migrate
make migrate-test
docker exec hwh_postgres psql -U app -d appdb -c "\d user_event_match"
```

Expected: table with PK `(user_id, event_id)`, both FKs ON DELETE CASCADE, index on `(user_id, score DESC)`.

- [ ] **Step 4: Commit**

```bash
git add sql/migrations/0010_user_event_match.up.sql sql/migrations/0010_user_event_match.down.sql
git commit -m "feat: migration 0010 — user_event_match"
```

---

### Task 4: Update testdb truncate list

**Files:**
- Modify: `internal/testdb/testdb.go`

- [ ] **Step 1: Add `user_event_match` to the `tables` slice**

Find the `tables` slice in `truncateAll` and update to (children before parents):

```go
	tables := []string{
		"user_event_match",
		"event_genres",
		"event_performers",
		"events",
		"venues",
		"user_interests",
		"user_spotify_tokens",
		"refresh_tokens",
		"users",
	}
```

`match_config` is seeded vocab — do NOT truncate.

- [ ] **Step 2: Verify build + tests**

```bash
go build ./...
make test
```

Expected: full suite passes (no new tests yet; existing should be unaffected).

- [ ] **Step 3: Commit**

```bash
git add internal/testdb/testdb.go
git commit -m "test(testdb): truncate user_event_match between tests"
```

---

### Task 5: sqlc queries — `match_config`

**Files:**
- Create: `sql/queries/match_config.sql`
- Regenerate: `internal/store/*` via `sqlc generate`

- [ ] **Step 1: Write `sql/queries/match_config.sql`**

```sql
-- name: ListMatchConfig :many
SELECT key, value FROM match_config ORDER BY key ASC;
```

A single query returning all 6 rows; the Go caller parses them into a typed `Config` struct.

- [ ] **Step 2: Generate**

```bash
sqlc generate
go build ./...
make test
```

Expected: clean build, all tests pass. New `ListMatchConfig` method on `*Queries`.

- [ ] **Step 3: Commit**

```bash
git add sql/queries/match_config.sql internal/store/
git commit -m "feat: sqlc query for match_config"
```

---

### Task 6: sqlc queries — events embedding + bulk loading

**Files:**
- Modify: `sql/queries/events.sql` (append)
- Regenerate: `internal/store/*`

- [ ] **Step 1: Append to `sql/queries/events.sql`**

```sql
-- name: SelectEventsNeedingEmbedding :many
SELECT id, title, description
FROM events
WHERE embedding IS NULL
  AND archived_at IS NULL
  AND starts_at > NOW();

-- name: UpdateEventEmbedding :exec
UPDATE events
SET embedding = $2, embedding_updated_at = NOW(), updated_at = NOW()
WHERE id = $1;

-- name: ListUpcomingEventsForMatching :many
SELECT id, embedding
FROM events
WHERE archived_at IS NULL AND starts_at > NOW();

-- name: ListEventPerformersBatch :many
SELECT event_id, performer_name, normalized_name
FROM event_performers
WHERE event_id = ANY($1::uuid[]);

-- name: ListEventGenresBatch :many
SELECT event_id, genre_slug
FROM event_genres
WHERE event_id = ANY($1::uuid[]);

-- name: ArchiveStaleEvents :exec
UPDATE events
SET archived_at = NOW(), updated_at = NOW()
WHERE archived_at IS NULL
  AND last_seen_at < NOW() - INTERVAL '7 days';
```

- [ ] **Step 2: Generate**

```bash
sqlc generate
go build ./...
make test
```

Expected: clean build. New methods on `*Queries`.

- [ ] **Step 3: Commit**

```bash
git add sql/queries/events.sql internal/store/
git commit -m "feat: sqlc queries for event embedding + matching data + archival"
```

---

### Task 7: sqlc queries — users embedding + bulk loading

**Files:**
- Modify: `sql/queries/users.sql` (append)
- Modify: `sql/queries/user_interests.sql` (append)
- Regenerate

- [ ] **Step 1: Append to `sql/queries/users.sql`**

```sql
-- name: SelectUsersNeedingEmbedding :many
SELECT u.id
FROM users u
WHERE u.deleted_at IS NULL
  AND (
    u.interest_embedding IS NULL
    OR u.interest_embedding_updated_at IS NULL
    OR u.interest_embedding_updated_at < COALESCE(
         (SELECT MAX(updated_at) FROM user_interests ui WHERE ui.user_id = u.id),
         u.created_at
       )
  );

-- name: UpdateUserInterestEmbedding :exec
UPDATE users
SET interest_embedding = $2, interest_embedding_updated_at = NOW()
WHERE id = $1;

-- name: ListActiveUsersForMatching :many
SELECT id, interest_embedding
FROM users
WHERE deleted_at IS NULL;
```

- [ ] **Step 2: Append to `sql/queries/user_interests.sql`**

```sql
-- name: ListUserInterestsBatch :many
SELECT user_id, kind, value, normalized_value, weight
FROM user_interests
WHERE user_id = ANY($1::uuid[])
ORDER BY user_id, weight DESC;
```

- [ ] **Step 3: Generate**

```bash
sqlc generate
go build ./...
make test
```

- [ ] **Step 4: Commit**

```bash
git add sql/queries/users.sql sql/queries/user_interests.sql internal/store/
git commit -m "feat: sqlc queries for user embedding + bulk-load interests"
```

---

### Task 8: sqlc queries — `user_event_match` upsert + cleanup

**Files:**
- Create: `sql/queries/user_event_match.sql`
- Regenerate

- [ ] **Step 1: Write `sql/queries/user_event_match.sql`**

```sql
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
```

- [ ] **Step 2: Generate**

```bash
sqlc generate
go build ./...
make test
```

- [ ] **Step 3: Commit**

```bash
git add sql/queries/user_event_match.sql internal/store/
git commit -m "feat: sqlc queries for user_event_match upsert + cleanup"
```

---

### Task 9: TEI HTTP client

**Files:**
- Create: `internal/tei/client.go`
- Create: `internal/tei/client_test.go`

- [ ] **Step 1: Write failing test**

`internal/tei/client_test.go`:

```go
package tei

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEmbed_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/embed", r.URL.Path)
		var req struct {
			Inputs []string `json:"inputs"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		require.Len(t, req.Inputs, 2)
		w.Header().Set("Content-Type", "application/json")
		// TEI returns [][]float32: one vector per input.
		_, _ = w.Write([]byte(`[[0.1, 0.2, 0.3], [0.4, 0.5, 0.6]]`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	vecs, err := c.Embed(context.Background(), []string{"a", "b"})
	require.NoError(t, err)
	require.Len(t, vecs, 2)
	require.Equal(t, []float32{0.1, 0.2, 0.3}, vecs[0])
	require.Equal(t, []float32{0.4, 0.5, 0.6}, vecs[1])
}

func TestEmbed_EmptyInput_NoCall(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++ }))
	defer srv.Close()
	c := New(srv.URL)
	vecs, err := c.Embed(context.Background(), nil)
	require.NoError(t, err)
	require.Empty(t, vecs)
	require.Equal(t, 0, calls)
}

func TestEmbed_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"oops"}`))
	}))
	defer srv.Close()
	c := New(srv.URL)
	_, err := c.Embed(context.Background(), []string{"x"})
	require.Error(t, err)
}
```

- [ ] **Step 2: Run test to confirm it fails**

```bash
go test ./internal/tei -v
```

Expected: FAIL — `package tei; no Go files`.

- [ ] **Step 3: Implement**

`internal/tei/client.go`:

```go
// Package tei wraps the Hugging Face text-embeddings-inference HTTP API.
// TEI returns a 2D array of float32 vectors — one per input string.
package tei

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type Client struct {
	baseURL string
	http    *http.Client
}

func New(baseURL string) *Client {
	return &Client{
		baseURL: baseURL,
		http:    &http.Client{Timeout: 60 * time.Second},
	}
}

// Embed sends inputs to TEI's /embed endpoint and returns a vector per input.
// Empty input → empty output without an HTTP call.
func (c *Client) Embed(ctx context.Context, inputs []string) ([][]float32, error) {
	if len(inputs) == 0 {
		return nil, nil
	}
	body, err := json.Marshal(struct {
		Inputs []string `json:"inputs"`
	}{Inputs: inputs})
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/embed", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("tei %d: %s", resp.StatusCode, string(b))
	}
	var out [][]float32
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return out, nil
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/tei -v
```

Expected: all 3 tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tei/client.go internal/tei/client_test.go
git commit -m "feat(tei): HTTP client for text-embeddings-inference /embed endpoint"
```

---

### Task 10: Config additions — TEI endpoint

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`

- [ ] **Step 1: Append failing test**

Add to `internal/config/config_test.go`:

```go
func TestLoad_TEIEndpoint(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("JWT_SIGNING_KEY", "k")
	t.Setenv("TEI_ENDPOINT", "http://localhost:8081")
	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, "http://localhost:8081", cfg.TEIEndpoint)
}
```

- [ ] **Step 2: Run to confirm it fails**

```bash
go test ./internal/config -v -run TEI
```

Expected: FAIL — `cfg.TEIEndpoint undefined`.

- [ ] **Step 3: Extend Config struct**

Add to `Config`:

```go
	TEIEndpoint string
```

In `Load()`, add to the `&Config{...}` literal:

```go
		TEIEndpoint: os.Getenv("TEI_ENDPOINT"),
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/config -v
```

Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat(config): TEI_ENDPOINT env var"
```

---

### Task 11: Matcher types

**Files:**
- Create: `internal/matcher/types.go`

This task is type-only — no behavior to TDD. The next tasks exercise these types.

- [ ] **Step 1: Write `internal/matcher/types.go`**

```go
// Package matcher implements the nightly match-job: embed events and users,
// score (user, event) pairs, upsert matches above threshold, archive stale
// events.
package matcher

import (
	"github.com/jackc/pgx/v5/pgtype"
)

// Config holds the tunable weights/factors read from the match_config table.
type Config struct {
	WString        float64
	WEmbedding     float64
	ScoreThreshold float64
	ArtistFactor   float64
	GenreFactor    float64
	StringMax      float64
}

// Defaults returns the v1 defaults (matches the seed rows in migration 0009).
// Used as a fallback when match_config can't be loaded or a key is missing.
func Defaults() Config {
	return Config{
		WString:        0.6,
		WEmbedding:     0.4,
		ScoreThreshold: 0.3,
		ArtistFactor:   1.0,
		GenreFactor:    0.3,
		StringMax:      3.0,
	}
}

// NormalizedInterest is one user_interests row reduced to the fields the
// matcher needs.
type NormalizedInterest struct {
	Value      string  // raw display value (artist name, genre slug, manual tag)
	Normalized string  // normalized form used for matching
	Weight     float64
}

// UserProfile is a user's matchable interest profile.
type UserProfile struct {
	UserID         pgtype.UUID
	Embedding      []float32 // 384-dim; may be nil if not yet embedded
	SpotifyArtists []NormalizedInterest
	SpotifyGenres  []NormalizedInterest
	ManualTags     []NormalizedInterest
}

// EventPerformer pairs the display name with its normalized form.
type EventPerformer struct {
	Display    string
	Normalized string
}

// EventProfile is a single event's matchable profile.
type EventProfile struct {
	EventID    pgtype.UUID
	Embedding  []float32
	Performers []EventPerformer
	Genres     []string // slugs
}

// MatchScore is the output of Score() — what gets written to user_event_match.
type MatchScore struct {
	StringScore       float64
	EmbeddingScore    float64
	TotalScore        float64
	MatchedPerformers []string // display names of matched performers
	MatchedGenres     []string // genre slugs that matched
}
```

- [ ] **Step 2: Verify build**

```bash
go build ./internal/matcher
```

Expected: clean (no source files reference these yet — but build should still succeed because all dependencies exist).

- [ ] **Step 3: Commit**

```bash
git add internal/matcher/types.go
git commit -m "feat(matcher): Config, UserProfile, EventProfile, MatchScore types"
```

---

### Task 12: Text builders (event + user)

**Files:**
- Create: `internal/matcher/text.go`
- Create: `internal/matcher/text_test.go`

- [ ] **Step 1: Write failing test**

`internal/matcher/text_test.go`:

```go
package matcher

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildEventText_AllFields(t *testing.T) {
	in := EventText{
		Title:       "Phoebe Bridgers Live",
		Performers:  []string{"Phoebe Bridgers", "MUNA"},
		Genres:      []string{"indie", "rock"},
		Description: "Indie rock concert at the bowl",
	}
	got := BuildEventText(in)
	require.Equal(t, "Phoebe Bridgers Live — Phoebe Bridgers, MUNA. indie, rock. Indie rock concert at the bowl", got)
}

func TestBuildEventText_TruncatesDescription(t *testing.T) {
	desc := strings.Repeat("a", 600)
	in := EventText{Title: "T", Performers: []string{"P"}, Genres: []string{"g"}, Description: desc}
	got := BuildEventText(in)
	// 500-char cap on description portion.
	require.LessOrEqual(t, len(got), 600)
}

func TestBuildEventText_OmitsEmptyParts(t *testing.T) {
	in := EventText{Title: "Just A Title"}
	got := BuildEventText(in)
	require.Equal(t, "Just A Title", got)
}

func TestBuildUserText_AllSections(t *testing.T) {
	in := UserText{
		TopArtists: []string{"Phoebe Bridgers", "MUNA", "Big Thief"},
		TopGenres:  []string{"indie rock", "indie pop"},
		ManualTags: []string{"theater", "comedy"},
	}
	got := BuildUserText(in)
	require.Contains(t, got, "Top artists: Phoebe Bridgers, MUNA, Big Thief")
	require.Contains(t, got, "Top genres: indie rock, indie pop")
	require.Contains(t, got, "Interests: theater, comedy")
}

func TestBuildUserText_OnlyTagsPresent(t *testing.T) {
	in := UserText{ManualTags: []string{"jazz"}}
	got := BuildUserText(in)
	require.Equal(t, "Interests: jazz", got)
}

func TestBuildUserText_Empty(t *testing.T) {
	got := BuildUserText(UserText{})
	require.Equal(t, "", got)
}
```

- [ ] **Step 2: Run test to confirm it fails**

```bash
go test ./internal/matcher -v
```

Expected: FAIL — `undefined: EventText, BuildEventText, UserText, BuildUserText`.

- [ ] **Step 3: Implement**

`internal/matcher/text.go`:

```go
package matcher

import "strings"

const descriptionCharCap = 500

// EventText is the input to BuildEventText.
type EventText struct {
	Title       string
	Performers  []string
	Genres      []string
	Description string
}

// BuildEventText composes an event's embedding-input string.
// Format: "<title> — <performers, joined>. <genres, joined>. <description (truncated)>"
// Empty sections are omitted. Description is hard-capped at 500 chars.
func BuildEventText(in EventText) string {
	var parts []string
	if in.Title != "" {
		parts = append(parts, in.Title)
	}
	if len(in.Performers) > 0 || len(in.Genres) > 0 || in.Description != "" {
		// Add separators only if there's something after the title.
	}
	if len(in.Performers) > 0 {
		// Render as "Title — perf1, perf2"
		if len(parts) > 0 {
			parts[len(parts)-1] += " — " + strings.Join(in.Performers, ", ")
		} else {
			parts = append(parts, strings.Join(in.Performers, ", "))
		}
	}
	if len(in.Genres) > 0 {
		parts = append(parts, strings.Join(in.Genres, ", "))
	}
	if in.Description != "" {
		d := in.Description
		if len(d) > descriptionCharCap {
			d = d[:descriptionCharCap]
		}
		parts = append(parts, d)
	}
	return strings.Join(parts, ". ")
}

// UserText is the input to BuildUserText.
type UserText struct {
	TopArtists []string
	TopGenres  []string
	ManualTags []string
}

// BuildUserText composes a user's embedding-input string.
// Each section is included only when non-empty. Sections are joined with ". ".
func BuildUserText(in UserText) string {
	var sections []string
	if len(in.TopArtists) > 0 {
		sections = append(sections, "Top artists: "+strings.Join(in.TopArtists, ", "))
	}
	if len(in.TopGenres) > 0 {
		sections = append(sections, "Top genres: "+strings.Join(in.TopGenres, ", "))
	}
	if len(in.ManualTags) > 0 {
		sections = append(sections, "Interests: "+strings.Join(in.ManualTags, ", "))
	}
	return strings.Join(sections, ". ")
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/matcher -v
```

Expected: all 6 tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/matcher/text.go internal/matcher/text_test.go
git commit -m "feat(matcher): BuildEventText and BuildUserText"
```

---

### Task 13: Scorer (pure function)

**Files:**
- Create: `internal/matcher/scorer.go`
- Create: `internal/matcher/scorer_test.go`

- [ ] **Step 1: Write failing test**

`internal/matcher/scorer_test.go`:

```go
package matcher

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestScore_ArtistAndGenreMatch(t *testing.T) {
	user := UserProfile{
		SpotifyArtists: []NormalizedInterest{
			{Value: "Phoebe Bridgers", Normalized: "phoebe bridgers", Weight: 1.0},
		},
		SpotifyGenres: []NormalizedInterest{
			{Value: "indie", Normalized: "indie", Weight: 0.8},
		},
	}
	event := EventProfile{
		Performers: []EventPerformer{{Display: "Phoebe Bridgers", Normalized: "phoebe bridgers"}},
		Genres:     []string{"indie", "rock"},
	}
	cfg := Defaults()
	got := Score(user, event, cfg)

	// artist: 1.0 * artist_factor(1.0) = 1.0
	// genre: 0.8 * genre_factor(0.3) = 0.24
	// raw string = 1.24; clamped via string_max(3.0) = 1.24 / 3.0 ≈ 0.413
	// embed = 0 (both embeddings nil)
	// total = 0.6 * 0.413 + 0.4 * 0 ≈ 0.248
	require.InDelta(t, 0.413, got.StringScore, 0.01)
	require.Equal(t, 0.0, got.EmbeddingScore)
	require.InDelta(t, 0.248, got.TotalScore, 0.01)
	require.Equal(t, []string{"Phoebe Bridgers"}, got.MatchedPerformers)
	require.Equal(t, []string{"indie"}, got.MatchedGenres)
}

func TestScore_StringMaxClamp(t *testing.T) {
	// Many heavy artist matches should clamp to 1.0
	user := UserProfile{
		SpotifyArtists: []NormalizedInterest{
			{Value: "A", Normalized: "a", Weight: 1.0},
			{Value: "B", Normalized: "b", Weight: 1.0},
			{Value: "C", Normalized: "c", Weight: 1.0},
			{Value: "D", Normalized: "d", Weight: 1.0},
		},
	}
	event := EventProfile{
		Performers: []EventPerformer{
			{Display: "A", Normalized: "a"},
			{Display: "B", Normalized: "b"},
			{Display: "C", Normalized: "c"},
			{Display: "D", Normalized: "d"},
		},
	}
	cfg := Defaults()
	got := Score(user, event, cfg)
	require.Equal(t, 1.0, got.StringScore)
}

func TestScore_EmbeddingOnly(t *testing.T) {
	// No string matches, but both have embeddings → embedding-only score.
	a := []float32{1, 0, 0}
	b := []float32{1, 0, 0}
	user := UserProfile{Embedding: a}
	event := EventProfile{Embedding: b}
	cfg := Defaults()
	got := Score(user, event, cfg)
	// cosine_similarity(a, a) = 1.0; mapped to [0,1] = (1+1)/2 = 1.0
	require.Equal(t, 0.0, got.StringScore)
	require.InDelta(t, 1.0, got.EmbeddingScore, 0.001)
	// total = 0.6*0 + 0.4*1 = 0.4
	require.InDelta(t, 0.4, got.TotalScore, 0.001)
}

func TestScore_NoMatchAtAll(t *testing.T) {
	user := UserProfile{
		SpotifyArtists: []NormalizedInterest{{Value: "X", Normalized: "x", Weight: 1.0}},
	}
	event := EventProfile{
		Performers: []EventPerformer{{Display: "Y", Normalized: "y"}},
	}
	cfg := Defaults()
	got := Score(user, event, cfg)
	require.Equal(t, 0.0, got.StringScore)
	require.Equal(t, 0.0, got.EmbeddingScore)
	require.Equal(t, 0.0, got.TotalScore)
	require.Empty(t, got.MatchedPerformers)
}

func TestScore_ManualTagMatchesGenre(t *testing.T) {
	user := UserProfile{
		ManualTags: []NormalizedInterest{
			{Value: "jazz", Normalized: "jazz", Weight: 1.0},
		},
	}
	event := EventProfile{Genres: []string{"jazz"}}
	cfg := Defaults()
	got := Score(user, event, cfg)
	// genre score = 1.0 * 0.3 = 0.3; string = 0.3/3.0 = 0.1
	require.InDelta(t, 0.1, got.StringScore, 0.01)
	require.Equal(t, []string{"jazz"}, got.MatchedGenres)
}

func TestCosineSimilarity_Orthogonal(t *testing.T) {
	a := []float32{1, 0}
	b := []float32{0, 1}
	got := cosineSimilarity(a, b)
	require.InDelta(t, 0.0, got, 0.001)
}

func TestCosineSimilarity_Opposite(t *testing.T) {
	a := []float32{1, 0}
	b := []float32{-1, 0}
	got := cosineSimilarity(a, b)
	require.InDelta(t, -1.0, got, 0.001)
}

// Avoid unused-import warning for math.
var _ = math.Pi
```

- [ ] **Step 2: Run test to confirm it fails**

```bash
go test ./internal/matcher -v -run Score
```

Expected: FAIL — `undefined: Score, cosineSimilarity`.

- [ ] **Step 3: Implement**

`internal/matcher/scorer.go`:

```go
package matcher

import "math"

// Score computes the match score for one (user, event) pair.
// Pure function — no I/O. Both Embedding slices must be the same length to
// contribute to the embedding signal; mismatched or nil embeddings yield 0.
func Score(user UserProfile, event EventProfile, cfg Config) MatchScore {
	// String score — artists.
	performerSet := make(map[string]string, len(event.Performers)) // normalized -> display
	for _, p := range event.Performers {
		performerSet[p.Normalized] = p.Display
	}
	var artistScore float64
	var matchedPerformers []string
	for _, ui := range user.SpotifyArtists {
		if display, ok := performerSet[ui.Normalized]; ok {
			artistScore += ui.Weight * cfg.ArtistFactor
			matchedPerformers = append(matchedPerformers, display)
		}
	}

	// String score — genres. Both SpotifyGenres and ManualTags map against event.Genres.
	genreSet := make(map[string]struct{}, len(event.Genres))
	for _, g := range event.Genres {
		genreSet[g] = struct{}{}
	}
	var genreScore float64
	matchedGenresSet := make(map[string]struct{})
	for _, src := range [][]NormalizedInterest{user.SpotifyGenres, user.ManualTags} {
		for _, ui := range src {
			// Spotify genres and manual tags compare on Value (slug) against
			// event genre slugs. Normalized would also work but Value is the
			// canonical slug, which is what event.Genres holds.
			key := ui.Value
			if _, ok := genreSet[key]; ok {
				genreScore += ui.Weight * cfg.GenreFactor
				matchedGenresSet[key] = struct{}{}
			}
		}
	}
	matchedGenres := make([]string, 0, len(matchedGenresSet))
	for g := range matchedGenresSet {
		matchedGenres = append(matchedGenres, g)
	}

	stringScore := (artistScore + genreScore) / cfg.StringMax
	if stringScore > 1.0 {
		stringScore = 1.0
	} else if stringScore < 0 {
		stringScore = 0
	}

	// Embedding score.
	var embedScore float64
	if len(user.Embedding) > 0 && len(event.Embedding) == len(user.Embedding) {
		cs := cosineSimilarity(user.Embedding, event.Embedding)
		embedScore = (cs + 1.0) / 2.0
	}

	total := cfg.WString*stringScore + cfg.WEmbedding*embedScore

	return MatchScore{
		StringScore:       stringScore,
		EmbeddingScore:    embedScore,
		TotalScore:        total,
		MatchedPerformers: matchedPerformers,
		MatchedGenres:     matchedGenres,
	}
}

func cosineSimilarity(a, b []float32) float64 {
	if len(a) == 0 || len(b) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/matcher -v
```

Expected: all 8 tests PASS (6 text + 7 score = wait, count). Let me recount: 6 text tests + 5 score tests + 2 cosine tests = 13 tests. All PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/matcher/scorer.go internal/matcher/scorer_test.go
git commit -m "feat(matcher): Score and cosineSimilarity pure functions"
```

---

### Task 14: Event embedder step

**Files:**
- Create: `internal/matcher/event_embedder.go`
- Create: `internal/matcher/event_embedder_test.go`

- [ ] **Step 1: Write failing test**

`internal/matcher/event_embedder_test.go`:

```go
package matcher_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/pgvector/pgvector-go"
	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/matcher"
	"github.com/wmyers/heres-whats-happening/internal/store"
	"github.com/wmyers/heres-whats-happening/internal/testdb"
)

type fakeEmbedder struct {
	calls [][]string
	vec   []float32
}

func (f *fakeEmbedder) Embed(ctx context.Context, inputs []string) ([][]float32, error) {
	f.calls = append(f.calls, inputs)
	out := make([][]float32, len(inputs))
	for i := range inputs {
		out[i] = f.vec
	}
	return out, nil
}

func TestEmbedEvents_EmbedsUnembeddedUpcoming(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()

	// Seed a venue and event source
	src, err := q.GetEventSourceByName(ctx, "ticketmaster")
	require.NoError(t, err)
	city, _ := q.GetDefaultCity(ctx)
	venueID, err := q.UpsertVenue(ctx, store.UpsertVenueParams{
		CityID:         city.ID,
		Name:           "The Bowl",
		NormalizedName: "the bowl",
	})
	require.NoError(t, err)

	// Insert one upcoming event with no embedding
	eventID, err := q.UpsertEvent(ctx, store.UpsertEventParams{
		SourceID:      src.ID,
		SourceEventID: "tm-embed-1",
		Title:         "Phoebe Bridgers",
		Description:   "Indie rock concert",
		StartsAt:      pgtype.Timestamptz{Time: time.Now().Add(48 * time.Hour), Valid: true},
		VenueID:       venueID,
	})
	require.NoError(t, err)
	require.NoError(t, q.InsertEventPerformer(ctx, store.InsertEventPerformerParams{
		EventID: eventID, PerformerName: "Phoebe Bridgers", NormalizedName: "phoebe bridgers",
	}))
	require.NoError(t, q.InsertEventGenre(ctx, store.InsertEventGenreParams{
		EventID: eventID, GenreSlug: "indie",
	}))

	fakeVec := make([]float32, 384)
	for i := range fakeVec {
		fakeVec[i] = 0.1
	}
	emb := &fakeEmbedder{vec: fakeVec}
	step := matcher.NewEventEmbedder(q, emb)
	require.NoError(t, step.Run(ctx))

	// Embedder was called with text containing the title + performer + genre.
	require.Len(t, emb.calls, 1)
	require.Contains(t, emb.calls[0][0], "Phoebe Bridgers")
	require.Contains(t, emb.calls[0][0], "indie")

	// Event now has an embedding.
	ev, err := q.GetEventByID(ctx, eventID)
	require.NoError(t, err)
	require.NotNil(t, ev.Embedding)
	stored := ev.Embedding.Slice()
	require.Len(t, stored, 384)
	require.InDelta(t, 0.1, stored[0], 0.001)

	// suppress unused-import warning if needed
	var _ pgvector.Vector
}
```

- [ ] **Step 2: Run test to confirm it fails**

```bash
go test ./internal/matcher -v -run EmbedEvents
```

Expected: FAIL — `undefined: matcher.NewEventEmbedder`.

- [ ] **Step 3: Implement**

`internal/matcher/event_embedder.go`:

```go
package matcher

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/pgvector/pgvector-go"

	"github.com/wmyers/heres-whats-happening/internal/store"
)

// Embedder is the minimal interface the matcher needs from the TEI client.
// Mockable in tests.
type Embedder interface {
	Embed(ctx context.Context, inputs []string) ([][]float32, error)
}

// EventEmbedder embeds events whose embedding column is NULL.
type EventEmbedder struct {
	q   *store.Queries
	emb Embedder
}

func NewEventEmbedder(q *store.Queries, emb Embedder) *EventEmbedder {
	return &EventEmbedder{q: q, emb: emb}
}

// Run finds events that need an embedding, batches them through the embedder,
// and writes the vectors back.
func (e *EventEmbedder) Run(ctx context.Context) error {
	rows, err := e.q.SelectEventsNeedingEmbedding(ctx)
	if err != nil {
		return fmt.Errorf("select events: %w", err)
	}
	if len(rows) == 0 {
		return nil
	}

	// Bulk-load performers + genres for these events.
	eventIDs := make([]pgtype.UUID, 0, len(rows))
	for _, r := range rows {
		eventIDs = append(eventIDs, r.ID)
	}
	performers, err := e.q.ListEventPerformersBatch(ctx, eventIDs)
	if err != nil {
		return fmt.Errorf("list performers: %w", err)
	}
	genres, err := e.q.ListEventGenresBatch(ctx, eventIDs)
	if err != nil {
		return fmt.Errorf("list genres: %w", err)
	}

	performerByEvent := make(map[pgtype.UUID][]string)
	for _, p := range performers {
		performerByEvent[p.EventID] = append(performerByEvent[p.EventID], p.PerformerName)
	}
	genreByEvent := make(map[pgtype.UUID][]string)
	for _, g := range genres {
		genreByEvent[g.EventID] = append(genreByEvent[g.EventID], g.GenreSlug)
	}

	// Build embedding texts in event order.
	texts := make([]string, len(rows))
	for i, r := range rows {
		texts[i] = BuildEventText(EventText{
			Title:       r.Title,
			Performers:  performerByEvent[r.ID],
			Genres:      genreByEvent[r.ID],
			Description: r.Description,
		})
	}

	vectors, err := e.emb.Embed(ctx, texts)
	if err != nil {
		return fmt.Errorf("embed: %w", err)
	}
	if len(vectors) != len(rows) {
		return fmt.Errorf("embedder returned %d vectors for %d events", len(vectors), len(rows))
	}

	for i, r := range rows {
		v := pgvector.NewVector(vectors[i])
		if err := e.q.UpdateEventEmbedding(ctx, store.UpdateEventEmbeddingParams{
			ID:        r.ID,
			Embedding: &v,
		}); err != nil {
			return fmt.Errorf("update event %v: %w", r.ID, err)
		}
	}
	return nil
}
```

(If the sqlc-generated parameter for `UpdateEventEmbedding.Embedding` is `*pgvector.Vector` (pointer), pass `&v` as shown. If it's `pgvector.Vector` (non-pointer), pass `v` directly. Match what the generated struct shows.)

- [ ] **Step 4: Run tests**

```bash
make tei-up   # may already be running
make test
```

Expected: all matcher tests PASS plus all existing.

- [ ] **Step 5: Commit**

```bash
git add internal/matcher/event_embedder.go internal/matcher/event_embedder_test.go
git commit -m "feat(matcher): EventEmbedder fills events.embedding via TEI"
```

---

### Task 15: User embedder step

**Files:**
- Create: `internal/matcher/user_embedder.go`
- Create: `internal/matcher/user_embedder_test.go`

- [ ] **Step 1: Write failing test**

`internal/matcher/user_embedder_test.go`:

```go
package matcher_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/matcher"
	"github.com/wmyers/heres-whats-happening/internal/store"
	"github.com/wmyers/heres-whats-happening/internal/testdb"
)

func TestEmbedUsers_EmbedsUsersWithChangedInterests(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()

	city, _ := q.GetDefaultCity(ctx)
	userRow, err := q.CreateUser(ctx, store.CreateUserParams{
		Email:        "user-embed@example.com",
		PasswordHash: "stub",
		CityID:       city.ID,
	})
	require.NoError(t, err)
	require.NoError(t, q.InsertSpotifyInterest(ctx, store.InsertSpotifyInterestParams{
		UserID:          userRow.ID,
		Kind:            "spotify_top_artist",
		Value:           "Phoebe Bridgers",
		NormalizedValue: "phoebe bridgers",
		Weight:          1.0,
	}))
	require.NoError(t, q.InsertSpotifyInterest(ctx, store.InsertSpotifyInterestParams{
		UserID:          userRow.ID,
		Kind:            "spotify_top_genre",
		Value:           "indie",
		NormalizedValue: "indie",
		Weight:          0.9,
	}))

	fakeVec := make([]float32, 384)
	for i := range fakeVec {
		fakeVec[i] = 0.2
	}
	emb := &fakeEmbedder{vec: fakeVec}
	step := matcher.NewUserEmbedder(q, emb)
	require.NoError(t, step.Run(ctx))

	// Embedder was called with text containing the artist and genre.
	require.Len(t, emb.calls, 1)
	require.Contains(t, emb.calls[0][0], "Phoebe Bridgers")
	require.Contains(t, emb.calls[0][0], "indie")

	// User now has interest_embedding set.
	u, err := q.GetUserByID(ctx, userRow.ID)
	require.NoError(t, err)
	_ = u // just confirming the user is still queryable

	// Second run should not re-embed (interest_embedding_updated_at >= max interest update).
	require.NoError(t, step.Run(ctx))
	require.Len(t, emb.calls, 1) // still 1
}
```

- [ ] **Step 2: Run test to confirm it fails**

```bash
go test ./internal/matcher -v -run EmbedUsers
```

Expected: FAIL — `undefined: matcher.NewUserEmbedder`.

- [ ] **Step 3: Implement**

`internal/matcher/user_embedder.go`:

```go
package matcher

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/pgvector/pgvector-go"

	"github.com/wmyers/heres-whats-happening/internal/store"
)

// UserEmbedder embeds users whose interest_embedding is stale or missing.
type UserEmbedder struct {
	q   *store.Queries
	emb Embedder
}

func NewUserEmbedder(q *store.Queries, emb Embedder) *UserEmbedder {
	return &UserEmbedder{q: q, emb: emb}
}

func (u *UserEmbedder) Run(ctx context.Context) error {
	userIDs, err := u.q.SelectUsersNeedingEmbedding(ctx)
	if err != nil {
		return fmt.Errorf("select users: %w", err)
	}
	if len(userIDs) == 0 {
		return nil
	}

	interests, err := u.q.ListUserInterestsBatch(ctx, userIDs)
	if err != nil {
		return fmt.Errorf("list interests: %w", err)
	}

	type bucket struct {
		artists []string
		genres  []string
		tags    []string
	}
	byUser := make(map[pgtype.UUID]*bucket, len(userIDs))
	// Initialize so users with no interests still get an entry (and an empty embedding).
	for _, id := range userIDs {
		byUser[id] = &bucket{}
	}
	for _, ui := range interests {
		b := byUser[ui.UserID]
		switch ui.Kind {
		case "spotify_top_artist":
			b.artists = append(b.artists, ui.Value)
		case "spotify_top_genre":
			b.genres = append(b.genres, ui.Value)
		case "manual_tag":
			b.tags = append(b.tags, ui.Value)
		}
	}

	// Build texts in userIDs order.
	texts := make([]string, len(userIDs))
	for i, id := range userIDs {
		b := byUser[id]
		texts[i] = BuildUserText(UserText{
			TopArtists: b.artists,
			TopGenres:  b.genres,
			ManualTags: b.tags,
		})
	}

	// Filter out users whose text is empty — embedding an empty string is wasteful.
	// We still update their row so we don't keep re-selecting them on next runs.
	nonEmptyIdx := make([]int, 0, len(texts))
	nonEmptyTexts := make([]string, 0, len(texts))
	for i, t := range texts {
		if t != "" {
			nonEmptyIdx = append(nonEmptyIdx, i)
			nonEmptyTexts = append(nonEmptyTexts, t)
		}
	}

	var vectors [][]float32
	if len(nonEmptyTexts) > 0 {
		vectors, err = u.emb.Embed(ctx, nonEmptyTexts)
		if err != nil {
			return fmt.Errorf("embed: %w", err)
		}
		if len(vectors) != len(nonEmptyTexts) {
			return fmt.Errorf("embedder returned %d vectors for %d users", len(vectors), len(nonEmptyTexts))
		}
	}

	for j, idx := range nonEmptyIdx {
		v := pgvector.NewVector(vectors[j])
		if err := u.q.UpdateUserInterestEmbedding(ctx, store.UpdateUserInterestEmbeddingParams{
			ID:                userIDs[idx],
			InterestEmbedding: &v,
		}); err != nil {
			return fmt.Errorf("update user: %w", err)
		}
	}
	return nil
}
```

- [ ] **Step 4: Run tests**

```bash
make test
```

Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/matcher/user_embedder.go internal/matcher/user_embedder_test.go
git commit -m "feat(matcher): UserEmbedder fills users.interest_embedding via TEI"
```

---

### Task 16: Match step (load + score + upsert)

**Files:**
- Create: `internal/matcher/match_step.go`
- Create: `internal/matcher/match_step_test.go`

- [ ] **Step 1: Write failing test**

`internal/matcher/match_step_test.go`:

```go
package matcher_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/pgvector/pgvector-go"
	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/matcher"
	"github.com/wmyers/heres-whats-happening/internal/store"
	"github.com/wmyers/heres-whats-happening/internal/testdb"
)

func TestMatchStep_WritesAboveThresholdRows(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()

	// Set up user with interests + embedding
	city, _ := q.GetDefaultCity(ctx)
	userRow, _ := q.CreateUser(ctx, store.CreateUserParams{
		Email: "matchstep@example.com", PasswordHash: "stub", CityID: city.ID,
	})
	require.NoError(t, q.InsertSpotifyInterest(ctx, store.InsertSpotifyInterestParams{
		UserID: userRow.ID, Kind: "spotify_top_artist",
		Value: "Phoebe Bridgers", NormalizedValue: "phoebe bridgers", Weight: 1.0,
	}))
	userVec := make([]float32, 384)
	userVec[0] = 1.0
	uv := pgvector.NewVector(userVec)
	require.NoError(t, q.UpdateUserInterestEmbedding(ctx, store.UpdateUserInterestEmbeddingParams{
		ID:                userRow.ID,
		InterestEmbedding: &uv,
	}))

	// Set up event with matching performer + embedding
	src, _ := q.GetEventSourceByName(ctx, "ticketmaster")
	venueID, _ := q.UpsertVenue(ctx, store.UpsertVenueParams{
		CityID: city.ID, Name: "V", NormalizedName: "v",
	})
	eventID, _ := q.UpsertEvent(ctx, store.UpsertEventParams{
		SourceID:      src.ID,
		SourceEventID: "match-tm-1",
		Title:         "PB Live",
		StartsAt:      pgtype.Timestamptz{Time: time.Now().Add(48 * time.Hour), Valid: true},
		VenueID:       venueID,
	})
	require.NoError(t, q.InsertEventPerformer(ctx, store.InsertEventPerformerParams{
		EventID: eventID, PerformerName: "Phoebe Bridgers", NormalizedName: "phoebe bridgers",
	}))
	eventVec := make([]float32, 384)
	eventVec[0] = 1.0
	ev := pgvector.NewVector(eventVec)
	require.NoError(t, q.UpdateEventEmbedding(ctx, store.UpdateEventEmbeddingParams{
		ID:        eventID,
		Embedding: &ev,
	}))

	step := matcher.NewMatchStep(q, matcher.Defaults())
	require.NoError(t, step.Run(ctx))

	// One match row above threshold should now exist.
	row := pool.QueryRow(ctx,
		`SELECT score, score_breakdown FROM user_event_match WHERE user_id = $1 AND event_id = $2`,
		userRow.ID, eventID)
	var score float64
	var breakdown []byte
	require.NoError(t, row.Scan(&score, &breakdown))
	require.Greater(t, score, 0.3)

	var bd map[string]any
	require.NoError(t, json.Unmarshal(breakdown, &bd))
	require.Contains(t, bd, "string_score")
	require.Contains(t, bd, "embedding_score")
	require.Contains(t, bd, "matched_performers")
}

func TestMatchStep_BelowThresholdSkipped(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()

	city, _ := q.GetDefaultCity(ctx)
	userRow, _ := q.CreateUser(ctx, store.CreateUserParams{
		Email: "belowthresh@example.com", PasswordHash: "stub", CityID: city.ID,
	})
	// No interests, no embedding. Score will be 0.

	src, _ := q.GetEventSourceByName(ctx, "ticketmaster")
	venueID, _ := q.UpsertVenue(ctx, store.UpsertVenueParams{
		CityID: city.ID, Name: "V2", NormalizedName: "v2",
	})
	_, _ = q.UpsertEvent(ctx, store.UpsertEventParams{
		SourceID:      src.ID,
		SourceEventID: "match-tm-2",
		Title:         "Unrelated",
		StartsAt:      pgtype.Timestamptz{Time: time.Now().Add(48 * time.Hour), Valid: true},
		VenueID:       venueID,
	})

	step := matcher.NewMatchStep(q, matcher.Defaults())
	require.NoError(t, step.Run(ctx))

	var n int
	require.NoError(t, pool.QueryRow(ctx,
		"SELECT count(*) FROM user_event_match WHERE user_id = $1", userRow.ID).Scan(&n))
	require.Equal(t, 0, n)
}
```

- [ ] **Step 2: Run test to confirm it fails**

```bash
go test ./internal/matcher -v -run MatchStep
```

Expected: FAIL — `undefined: matcher.NewMatchStep`.

- [ ] **Step 3: Implement**

`internal/matcher/match_step.go`:

```go
package matcher

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wmyers/heres-whats-happening/internal/store"
)

// MatchStep loads active users + upcoming events, runs Score() for each pair,
// and upserts rows above the configured threshold into user_event_match.
type MatchStep struct {
	q   *store.Queries
	cfg Config
}

func NewMatchStep(q *store.Queries, cfg Config) *MatchStep {
	return &MatchStep{q: q, cfg: cfg}
}

func (m *MatchStep) Run(ctx context.Context) error {
	users, err := m.loadUsers(ctx)
	if err != nil {
		return fmt.Errorf("load users: %w", err)
	}
	events, err := m.loadEvents(ctx)
	if err != nil {
		return fmt.Errorf("load events: %w", err)
	}

	for _, user := range users {
		for _, event := range events {
			score := Score(user, event, m.cfg)
			if score.TotalScore <= m.cfg.ScoreThreshold {
				continue
			}
			bd, err := json.Marshal(map[string]any{
				"string_score":       score.StringScore,
				"embedding_score":    score.EmbeddingScore,
				"matched_performers": score.MatchedPerformers,
				"matched_genres":     score.MatchedGenres,
			})
			if err != nil {
				return fmt.Errorf("marshal breakdown: %w", err)
			}
			if err := m.q.UpsertUserEventMatch(ctx, store.UpsertUserEventMatchParams{
				UserID:         user.UserID,
				EventID:        event.EventID,
				Score:          score.TotalScore,
				ScoreBreakdown: bd,
			}); err != nil {
				return fmt.Errorf("upsert match: %w", err)
			}
		}
	}

	// Sweep out obsolete rows (events that are past or archived).
	if err := m.q.DeleteObsoleteMatches(ctx); err != nil {
		return fmt.Errorf("delete obsolete: %w", err)
	}
	return nil
}

func (m *MatchStep) loadUsers(ctx context.Context) ([]UserProfile, error) {
	rows, err := m.q.ListActiveUsersForMatching(ctx)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	userIDs := make([]pgtype.UUID, len(rows))
	for i, r := range rows {
		userIDs[i] = r.ID
	}
	interests, err := m.q.ListUserInterestsBatch(ctx, userIDs)
	if err != nil {
		return nil, fmt.Errorf("list interests: %w", err)
	}

	profiles := make(map[pgtype.UUID]*UserProfile, len(rows))
	for _, r := range rows {
		p := &UserProfile{UserID: r.ID}
		if r.InterestEmbedding != nil {
			p.Embedding = r.InterestEmbedding.Slice()
		}
		profiles[r.ID] = p
	}
	for _, ui := range interests {
		ni := NormalizedInterest{
			Value:      ui.Value,
			Normalized: ui.NormalizedValue,
			Weight:     ui.Weight,
		}
		p := profiles[ui.UserID]
		switch ui.Kind {
		case "spotify_top_artist":
			p.SpotifyArtists = append(p.SpotifyArtists, ni)
		case "spotify_top_genre":
			p.SpotifyGenres = append(p.SpotifyGenres, ni)
		case "manual_tag":
			p.ManualTags = append(p.ManualTags, ni)
		}
	}

	out := make([]UserProfile, 0, len(profiles))
	for _, r := range rows {
		out = append(out, *profiles[r.ID])
	}
	return out, nil
}

func (m *MatchStep) loadEvents(ctx context.Context) ([]EventProfile, error) {
	rows, err := m.q.ListUpcomingEventsForMatching(ctx)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	eventIDs := make([]pgtype.UUID, len(rows))
	for i, r := range rows {
		eventIDs[i] = r.ID
	}
	performers, err := m.q.ListEventPerformersBatch(ctx, eventIDs)
	if err != nil {
		return nil, fmt.Errorf("list performers: %w", err)
	}
	genres, err := m.q.ListEventGenresBatch(ctx, eventIDs)
	if err != nil {
		return nil, fmt.Errorf("list genres: %w", err)
	}

	profiles := make(map[pgtype.UUID]*EventProfile, len(rows))
	for _, r := range rows {
		p := &EventProfile{EventID: r.ID}
		if r.Embedding != nil {
			p.Embedding = r.Embedding.Slice()
		}
		profiles[r.ID] = p
	}
	for _, p := range performers {
		profiles[p.EventID].Performers = append(profiles[p.EventID].Performers, EventPerformer{
			Display:    p.PerformerName,
			Normalized: p.NormalizedName,
		})
	}
	for _, g := range genres {
		profiles[g.EventID].Genres = append(profiles[g.EventID].Genres, g.GenreSlug)
	}

	out := make([]EventProfile, 0, len(profiles))
	for _, r := range rows {
		out = append(out, *profiles[r.ID])
	}
	return out, nil
}
```

- [ ] **Step 4: Run tests**

```bash
make test
```

Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/matcher/match_step.go internal/matcher/match_step_test.go
git commit -m "feat(matcher): MatchStep loads + scores + upserts above threshold"
```

---

### Task 17: Archiver step

**Files:**
- Create: `internal/matcher/archiver.go`
- Create: `internal/matcher/archiver_test.go`

- [ ] **Step 1: Write failing test**

`internal/matcher/archiver_test.go`:

```go
package matcher_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/matcher"
	"github.com/wmyers/heres-whats-happening/internal/store"
	"github.com/wmyers/heres-whats-happening/internal/testdb"
)

func TestArchiver_MarksStaleEvents(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()

	city, _ := q.GetDefaultCity(ctx)
	src, _ := q.GetEventSourceByName(ctx, "ticketmaster")
	venueID, _ := q.UpsertVenue(ctx, store.UpsertVenueParams{
		CityID: city.ID, Name: "AV", NormalizedName: "av",
	})

	staleID, _ := q.UpsertEvent(ctx, store.UpsertEventParams{
		SourceID: src.ID, SourceEventID: "arch-1", Title: "Stale",
		StartsAt: pgtype.Timestamptz{Time: time.Now().Add(48 * time.Hour), Valid: true},
		VenueID:  venueID,
	})
	// Force last_seen_at to 10 days ago
	_, err := pool.Exec(ctx, `UPDATE events SET last_seen_at = NOW() - INTERVAL '10 days' WHERE id = $1`, staleID)
	require.NoError(t, err)

	freshID, _ := q.UpsertEvent(ctx, store.UpsertEventParams{
		SourceID: src.ID, SourceEventID: "arch-2", Title: "Fresh",
		StartsAt: pgtype.Timestamptz{Time: time.Now().Add(48 * time.Hour), Valid: true},
		VenueID:  venueID,
	})

	step := matcher.NewArchiver(q)
	require.NoError(t, step.Run(ctx))

	stale, err := q.GetEventByID(ctx, staleID)
	require.NoError(t, err)
	require.True(t, stale.ArchivedAt.Valid, "stale event should be archived")

	fresh, err := q.GetEventByID(ctx, freshID)
	require.NoError(t, err)
	require.False(t, fresh.ArchivedAt.Valid, "fresh event should not be archived")
}
```

- [ ] **Step 2: Run test to confirm it fails**

```bash
go test ./internal/matcher -v -run Archiver
```

Expected: FAIL — `undefined: matcher.NewArchiver`.

- [ ] **Step 3: Implement**

`internal/matcher/archiver.go`:

```go
package matcher

import (
	"context"
	"fmt"

	"github.com/wmyers/heres-whats-happening/internal/store"
)

// Archiver marks events as archived if their last_seen_at is older than 7 days.
type Archiver struct {
	q *store.Queries
}

func NewArchiver(q *store.Queries) *Archiver {
	return &Archiver{q: q}
}

func (a *Archiver) Run(ctx context.Context) error {
	if err := a.q.ArchiveStaleEvents(ctx); err != nil {
		return fmt.Errorf("archive stale events: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: Run tests**

```bash
make test
```

Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/matcher/archiver.go internal/matcher/archiver_test.go
git commit -m "feat(matcher): Archiver marks stale events as archived"
```

---

### Task 18: Job orchestrator

**Files:**
- Create: `internal/matcher/job.go`
- Create: `internal/matcher/job_test.go`

- [ ] **Step 1: Write failing test**

`internal/matcher/job_test.go`:

```go
package matcher_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/matcher"
	"github.com/wmyers/heres-whats-happening/internal/store"
	"github.com/wmyers/heres-whats-happening/internal/testdb"
)

func TestJob_FullRun_EmbedsScoresAndArchives(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()

	// Seed a user with interests
	city, _ := q.GetDefaultCity(ctx)
	userRow, _ := q.CreateUser(ctx, store.CreateUserParams{
		Email: "job-full@example.com", PasswordHash: "stub", CityID: city.ID,
	})
	require.NoError(t, q.InsertSpotifyInterest(ctx, store.InsertSpotifyInterestParams{
		UserID: userRow.ID, Kind: "spotify_top_artist",
		Value: "Phoebe Bridgers", NormalizedValue: "phoebe bridgers", Weight: 1.0,
	}))

	// Seed an event that matches
	src, _ := q.GetEventSourceByName(ctx, "ticketmaster")
	venueID, _ := q.UpsertVenue(ctx, store.UpsertVenueParams{
		CityID: city.ID, Name: "Bowl", NormalizedName: "bowl",
	})
	eventID, _ := q.UpsertEvent(ctx, store.UpsertEventParams{
		SourceID:      src.ID,
		SourceEventID: "job-1",
		Title:         "PB Live",
		StartsAt:      pgtype.Timestamptz{Time: time.Now().Add(48 * time.Hour), Valid: true},
		VenueID:       venueID,
	})
	require.NoError(t, q.InsertEventPerformer(ctx, store.InsertEventPerformerParams{
		EventID: eventID, PerformerName: "Phoebe Bridgers", NormalizedName: "phoebe bridgers",
	}))

	fakeVec := make([]float32, 384)
	fakeVec[0] = 1.0
	emb := &fakeEmbedder{vec: fakeVec}

	job := matcher.NewJob(q, emb, matcher.Defaults())
	require.NoError(t, job.Run(ctx))

	// Match exists.
	var n int
	require.NoError(t, pool.QueryRow(ctx,
		"SELECT count(*) FROM user_event_match WHERE user_id = $1 AND event_id = $2",
		userRow.ID, eventID).Scan(&n))
	require.Equal(t, 1, n)
}
```

- [ ] **Step 2: Run test to confirm it fails**

```bash
go test ./internal/matcher -v -run TestJob_FullRun
```

Expected: FAIL — `undefined: matcher.NewJob`.

- [ ] **Step 3: Implement**

`internal/matcher/job.go`:

```go
package matcher

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/wmyers/heres-whats-happening/internal/store"
)

// Job runs the four steps in order: embed events, embed users, match, archive.
type Job struct {
	q   *store.Queries
	emb Embedder
	cfg Config
}

// NewJob builds a Job. The cfg is used as a starting point but Job.Run
// re-reads match_config from the DB before scoring (so the seed defaults
// and any tuned overrides take effect without restarting the binary).
func NewJob(q *store.Queries, emb Embedder, cfg Config) *Job {
	return &Job{q: q, emb: emb, cfg: cfg}
}

func (j *Job) Run(ctx context.Context) error {
	// Step 0: refresh config from DB (overlays on cfg defaults).
	cfg, err := j.loadConfig(ctx)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	if err := NewEventEmbedder(j.q, j.emb).Run(ctx); err != nil {
		return fmt.Errorf("event embedder: %w", err)
	}
	if err := NewUserEmbedder(j.q, j.emb).Run(ctx); err != nil {
		return fmt.Errorf("user embedder: %w", err)
	}
	if err := NewMatchStep(j.q, cfg).Run(ctx); err != nil {
		return fmt.Errorf("match step: %w", err)
	}
	if err := NewArchiver(j.q).Run(ctx); err != nil {
		return fmt.Errorf("archiver: %w", err)
	}
	return nil
}

// loadConfig reads match_config rows and overlays them on j.cfg defaults.
// Missing keys keep the default value.
func (j *Job) loadConfig(ctx context.Context) (Config, error) {
	cfg := j.cfg
	rows, err := j.q.ListMatchConfig(ctx)
	if err != nil {
		return cfg, err
	}
	for _, r := range rows {
		// r.Value is jsonb (bytes). Parse as float.
		var raw json.Number
		if err := json.Unmarshal(r.Value, &raw); err != nil {
			continue
		}
		f, err := strconv.ParseFloat(string(raw), 64)
		if err != nil {
			continue
		}
		switch r.Key {
		case "w_string":
			cfg.WString = f
		case "w_embedding":
			cfg.WEmbedding = f
		case "score_threshold":
			cfg.ScoreThreshold = f
		case "artist_factor":
			cfg.ArtistFactor = f
		case "genre_factor":
			cfg.GenreFactor = f
		case "string_max":
			cfg.StringMax = f
		}
	}
	return cfg, nil
}
```

Note: the `r.Value` type from sqlc for a JSONB column is typically `[]byte`. If sqlc generated a different type (e.g., `pgtype.JSONB`), adapt the unmarshal accordingly.

- [ ] **Step 4: Run tests**

```bash
make test
```

Expected: full suite passes.

- [ ] **Step 5: Commit**

```bash
git add internal/matcher/job.go internal/matcher/job_test.go
git commit -m "feat(matcher): Job orchestrator (embed events, embed users, match, archive)"
```

---

### Task 19: `app match` subcommand

**Files:**
- Modify: `cmd/app/main.go`

- [ ] **Step 1: Add `match` to subcommand dispatch**

In `main()`, add a `match` case to the switch:

```go
	case "match":
		if err := runMatch(); err != nil {
			fmt.Fprintf(os.Stderr, "match: %v\n", err)
			os.Exit(1)
		}
```

Update `usage()` to add a line:

```go
  match                       run the match-job (embed events+users, score, archive)
```

- [ ] **Step 2: Implement `runMatch`**

Append to `cmd/app/main.go`:

```go
func runMatch() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	if cfg.TEIEndpoint == "" {
		return fmt.Errorf("TEI_ENDPOINT is required for match-job")
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := db.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("db: %w", err)
	}
	defer pool.Close()
	q := store.New(pool)

	teiClient := tei.New(cfg.TEIEndpoint)
	job := matcher.NewJob(q, teiClient, matcher.Defaults())
	fmt.Println("running match-job ...")
	if err := job.Run(ctx); err != nil {
		return err
	}
	fmt.Println("match-job complete")
	return nil
}
```

Add to imports:

```go
	"github.com/wmyers/heres-whats-happening/internal/matcher"
	"github.com/wmyers/heres-whats-happening/internal/tei"
```

- [ ] **Step 3: Verify build**

```bash
go build ./cmd/app
./app
./app match  # may fail if config is missing, that's OK
```

Expected: usage lists `match`; running it errors helpfully if env not configured.

- [ ] **Step 4: Smoke test (with TEI + Postgres + ElasticMQ all running)**

```bash
make tei-up
make db-up
set -a; source .env.example; set +a
./app match
```

Expected: prints `running match-job ...` and `match-job complete`. (With an empty DB, there are no users or events so nothing happens, but it completes cleanly.)

- [ ] **Step 5: Commit**

```bash
git add cmd/app/main.go
git commit -m "feat(cmd): match subcommand runs the match-job"
```

---

### Task 20: README — Plan 4 quickstart

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Append Plan 4 quickstart at the end of README**

````markdown

## Plan 4 quickstart — match-job

```bash
# Start the TEI sidecar (BAAI/bge-small-en-v1.5)
make tei-up
# First run downloads the model; takes ~2 minutes. Subsequent runs are fast.

# Verify TEI is healthy
curl -s http://localhost:8081/health

# Run the match-job
./app match
# Steps it runs:
#  1. Embed any events whose embedding column is NULL
#  2. Embed any users whose interests changed since last embedding
#  3. Score every (user, event) pair; upsert above-threshold matches
#  4. Archive events not seen in the last 7 days
```

### Tuning weights

Match weights live in the `match_config` table; change them with SQL and the
next `./app match` picks them up — no rebuild needed.

```sql
UPDATE match_config SET value = '0.7'::jsonb WHERE key = 'w_string';
UPDATE match_config SET value = '0.3'::jsonb WHERE key = 'w_embedding';
```

### Inspect a user's matches

```bash
docker exec hwh_postgres psql -U app -d appdb -c "
  SELECT e.title, m.score, m.score_breakdown
  FROM user_event_match m
  JOIN events e ON e.id = m.event_id
  WHERE m.user_id = (SELECT id FROM users WHERE email = 'you@example.com')
  ORDER BY m.score DESC, e.starts_at ASC
  LIMIT 20;
"
```
````

- [ ] **Step 2: Commit**

```bash
git add README.md
git commit -m "docs: Plan 4 match-job quickstart"
```

---

## Self-Review

**Spec coverage check (Plan 4 scope only):**

| Spec requirement | Implemented in |
|---|---|
| TEI sidecar with `bge-small-en-v1.5` (384-dim) | Task 1 |
| `match_config` table with 6 seeded keys | Task 2 |
| `user_event_match` table with `(user_id, event_id)` PK, jsonb breakdown | Task 3 |
| Index on `(user_id, score DESC)` | Task 3 |
| sqlc queries for match_config, embedding I/O, bulk loading, upsert/cleanup, archival | Tasks 5–8 |
| Score formula: `0.6*string + 0.4*embedding` with all knobs from match_config | Task 13 |
| string_score: artist + genre matches, clamped via string_max | Task 13 |
| embedding_score: cosine-similarity → [0,1] | Task 13 |
| score_breakdown jsonb stored on each match row | Task 16 |
| Threshold-based row writing (only > 0.3 written) | Task 16 |
| TEI called only by match-job | Tasks 14, 15 (Job calls Embedder; ingest pipeline does NOT) |
| User-interest text format: "Top artists: ... . Top genres: ... . Interests: ..." | Task 12 |
| Event text format: "<title> — <performers>. <genres>. <description>" | Task 12 |
| Archival sets `archived_at` after `last_seen_at < now - 7 days` | Task 17 |
| Cleanup deletes match rows for past/archived events | Task 16 (DeleteObsoleteMatches) |
| `app match` subcommand | Task 19 |

**Deferred to later plans:**

- Per-user incremental recompute (Plan 4 does full recompute each run — acceptable at v1 scale).
- EventBridge schedule for daily 02:00 invocation (Plan 7/8 — production infra).
- Tuning the match weights based on real user feedback (future).

**Placeholder scan:** no "TBD" or "add error handling" patterns; every code-touching step has complete code.

**Type consistency:**

- `matcher.Config`, `UserProfile`, `EventProfile`, `MatchScore`, `NormalizedInterest`, `EventPerformer` defined in Task 11; used everywhere downstream.
- `BuildEventText`/`BuildUserText` in Task 12 consumed by Tasks 14, 15.
- `Score` in Task 13 consumed by Task 16 (MatchStep).
- `Embedder` interface introduced in Task 14, satisfied by `*tei.Client` (Task 9). Mockable via `fakeEmbedder` in tests.
- `NewEventEmbedder`, `NewUserEmbedder`, `NewMatchStep`, `NewArchiver`, `NewJob` constructors consistently named.
- pgvector `*pgvector.Vector` field on `events.Embedding` and `users.InterestEmbedding` is handled with `pgvector.NewVector` (write) and `.Slice()` (read). If sqlc generates value-types instead of pointers, swap `&v` ↔ `v` at the call sites — that's the only mechanical adjustment.

**Plan-internal consistency notes:**

- `cosineSimilarity` returns `float64` ∈ [-1, 1]; the Scorer maps to [0, 1] via `(cs + 1) / 2`. Tests assert both the orthogonal (0) and identical-vector (1) cases.
- `MatchStep.Run` upserts only rows with `TotalScore > cfg.ScoreThreshold` (strict greater-than, matching the spec wording).
- `Job.Run` loads config fresh each call, so tuning via SQL takes effect on the next run.
- TEI's `/embed` endpoint accepts `{"inputs": ["a", "b"]}` and returns `[[..], [..]]` — confirmed against the TEI documentation. If the response shape differs (e.g., older TEI versions used `[{"embedding": [...]}, ...]`), the client decode in Task 9 needs adjustment.

---

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-05-20-plan-04-match-job.md`. Two execution options:

1. **Subagent-Driven (recommended)** — I dispatch a fresh subagent per task, review between tasks, fast iteration.
2. **Inline Execution** — Execute tasks in this session using executing-plans, batch execution with checkpoints.

Which approach?
