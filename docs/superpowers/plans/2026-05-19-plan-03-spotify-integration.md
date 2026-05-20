# Plan 3 — Spotify Integration + Interest Ingest Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** End-to-end Spotify integration: users connect Spotify via OAuth (PKCE), tokens are encrypted at rest, the scraper publishes their top artists / top genres to a new `interests-queue`, and the ingest consumer writes them to `user_interests`.

**Architecture:** Three new flows on top of Plan 2's pipeline: (1) `GET /integrations/spotify/{connect,callback}` and `DELETE /integrations/spotify` for the OAuth dance; tokens encrypted with AES-GCM using a key from env. (2) `app scrape spotify` iterates users with non-revoked Spotify tokens, calls Spotify Web API, publishes one `InterestMessage` per user to the new `interests-queue`. The OAuth callback also triggers an immediate one-user scrape so new connections don't wait until the next periodic run. (3) `ingest.Consumer` is generalized to dispatch by message type, with a new `InterestHandler` that replaces a user's `spotify_top_artist` and `spotify_top_genre` rows in `user_interests` atomically per ingest.

**Tech Stack:** Go 1.24+ · `crypto/aes` + `crypto/cipher` (AES-256-GCM) · `golang.org/x/oauth2` is **not** used — we drive OAuth manually for PKCE clarity · Spotify Web API · existing `chi`, `sqlc`, `pgx/v5`, `aws-sdk-go-v2/sqs`, integration-tests-against-real-Postgres patterns.

---

## File Structure

```
.
├── cmd/app/main.go                                  # add `scrape spotify` dispatch; wire interest consumer
├── internal/
│   ├── config/config.go                             # add Spotify + interests-queue + crypto-key vars
│   ├── crypto/
│   │   ├── aesgcm.go                                # NewCipher, Encrypt, Decrypt
│   │   └── aesgcm_test.go
│   ├── events/
│   │   ├── interest.go                              # InterestMessage + SpotifyTopItem
│   │   └── interest_test.go
│   ├── spotify/
│   │   ├── client.go                                # OAuth code-exchange, refresh, GetTopArtists
│   │   ├── client_test.go                           # httptest-mocked tests
│   │   ├── pkce.go                                  # NewVerifier, Challenge
│   │   ├── pkce_test.go
│   │   ├── oauth_state.go                           # signed state-cookie helpers (HMAC)
│   │   └── oauth_state_test.go
│   ├── scraper/spotify/
│   │   ├── adapter.go                               # ScrapeOne(userID) → publish InterestMessage
│   │   └── adapter_test.go                          # mocks Spotify client + integration DB
│   ├── http/handlers/
│   │   ├── spotify.go                               # Connect / Callback / Disconnect
│   │   └── spotify_test.go
│   ├── ingest/
│   │   ├── consumer.go                              # generalized: takes MessageHandler interface
│   │   ├── events.go                                # renamed Handler → EventHandler; impl MessageHandler
│   │   ├── interests.go                             # InterestHandler — impl MessageHandler
│   │   └── interests_test.go
│   └── store/                                       # sqlc regenerated
├── sql/
│   ├── migrations/
│   │   └── 0008_user_spotify_tokens.up.sql/.down.sql
│   └── queries/
│       └── user_spotify_tokens.sql
├── scripts/elasticmq.conf                           # add interests-queue + interests-dlq
└── .env.example                                     # SPOTIFY_*, SPOTIFY_TOKEN_ENC_KEY, INTERESTS_QUEUE_URL
```

**Boundaries:**

- `internal/crypto` — AES-GCM primitive. Knows nothing about Spotify.
- `internal/spotify` — OAuth + Web API client. No DB, no SQS, no encryption (caller encrypts).
- `internal/scraper/spotify` — the bridge from DB (read tokens) → spotify.Client (fetch) → queue (publish).
- `internal/ingest` — same role as Plan 2: queue → DB. Now dispatches via a generic `MessageHandler` interface.
- HTTP handlers are the only place that touches both `spotify.Client` and the DB during the OAuth dance.

---

## Prerequisites

- Plans 1 and 2 merged to master.
- `docker compose up postgres elasticmq` running. Plan 2's migrations 0001-0007 applied.
- A Spotify developer app: register at https://developer.spotify.com/dashboard, set the redirect URI to `http://localhost:8080/integrations/spotify/callback`, copy Client ID + Client Secret into `.env`.
- For the smoke test at end of Task 14, you'll connect Spotify from a browser via that callback URL.

---

### Task 1: Migration 0008 — `user_spotify_tokens`

**Files:**
- Create: `sql/migrations/0008_user_spotify_tokens.up.sql`
- Create: `sql/migrations/0008_user_spotify_tokens.down.sql`

- [ ] **Step 1: Write `0008_user_spotify_tokens.up.sql`**

```sql
CREATE TABLE user_spotify_tokens (
    user_id            UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    access_token_enc   BYTEA NOT NULL,
    refresh_token_enc  BYTEA NOT NULL,
    expires_at         TIMESTAMPTZ NOT NULL,
    scope              TEXT NOT NULL,
    last_synced_at     TIMESTAMPTZ,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

- [ ] **Step 2: Write `0008_user_spotify_tokens.down.sql`**

```sql
DROP TABLE IF EXISTS user_spotify_tokens;
```

- [ ] **Step 3: Run migrations and verify**

```bash
make migrate
make migrate-test
docker exec hwh_postgres psql -U app -d appdb -c "\d user_spotify_tokens"
```

Expected: `8/u user_spotify_tokens` applied to both DBs; table shows the documented columns.

- [ ] **Step 4: Update testdb truncate list**

Edit `internal/testdb/testdb.go` and add `"user_spotify_tokens"` to the `tables` slice (before `users` since it FKs `users`):

```go
	tables := []string{
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

- [ ] **Step 5: Verify build + full test suite**

```bash
go build ./...
make test
```

Expected: all pass.

- [ ] **Step 6: Commit**

```bash
git add sql/migrations/0008_user_spotify_tokens.up.sql sql/migrations/0008_user_spotify_tokens.down.sql internal/testdb/testdb.go
git commit -m "feat: migration 0008 — user_spotify_tokens"
```

---

### Task 2: sqlc queries for `user_spotify_tokens`

**Files:**
- Create: `sql/queries/user_spotify_tokens.sql`
- Regenerate: `internal/store/*` via `sqlc generate`

- [ ] **Step 1: Write `sql/queries/user_spotify_tokens.sql`**

```sql
-- name: UpsertUserSpotifyTokens :exec
INSERT INTO user_spotify_tokens (
    user_id, access_token_enc, refresh_token_enc, expires_at, scope
)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (user_id)
DO UPDATE SET
    access_token_enc  = EXCLUDED.access_token_enc,
    refresh_token_enc = EXCLUDED.refresh_token_enc,
    expires_at        = EXCLUDED.expires_at,
    scope             = EXCLUDED.scope,
    updated_at        = NOW();

-- name: GetUserSpotifyTokens :one
SELECT user_id, access_token_enc, refresh_token_enc, expires_at, scope, last_synced_at
FROM user_spotify_tokens
WHERE user_id = $1;

-- name: DeleteUserSpotifyTokens :exec
DELETE FROM user_spotify_tokens WHERE user_id = $1;

-- name: ListUserSpotifyTokens :many
SELECT user_id, access_token_enc, refresh_token_enc, expires_at, scope, last_synced_at
FROM user_spotify_tokens
ORDER BY user_id ASC;

-- name: UpdateUserSpotifyTokensLastSynced :exec
UPDATE user_spotify_tokens
SET last_synced_at = NOW(), updated_at = NOW()
WHERE user_id = $1;

-- name: DeleteSpotifyDerivedInterests :exec
DELETE FROM user_interests
WHERE user_id = $1 AND kind IN ('spotify_top_artist', 'spotify_top_genre');

-- name: ReplaceSpotifyArtistInterests :exec
-- caller wraps in a tx: deletes then inserts each new row via InsertUserInterest below
DELETE FROM user_interests
WHERE user_id = $1 AND kind = 'spotify_top_artist';

-- name: ReplaceSpotifyGenreInterests :exec
DELETE FROM user_interests
WHERE user_id = $1 AND kind = 'spotify_top_genre';

-- name: InsertSpotifyInterest :exec
INSERT INTO user_interests (user_id, kind, value, normalized_value, weight)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (user_id, kind, normalized_value) DO UPDATE SET
    value      = EXCLUDED.value,
    weight     = EXCLUDED.weight,
    updated_at = NOW();
```

- [ ] **Step 2: Generate and verify build**

```bash
sqlc generate
go build ./...
make test
```

Expected: no errors. New file `internal/store/user_spotify_tokens.sql.go` exists.

- [ ] **Step 3: Commit**

```bash
git add sql/queries/user_spotify_tokens.sql internal/store/
git commit -m "feat: sqlc queries for user_spotify_tokens + Spotify interest writes"
```

---

### Task 3: AES-GCM helper

**Files:**
- Create: `internal/crypto/aesgcm.go`
- Create: `internal/crypto/aesgcm_test.go`

- [ ] **Step 1: Write failing test**

`internal/crypto/aesgcm_test.go`:

```go
package crypto

import (
	"crypto/rand"
	"testing"

	"github.com/stretchr/testify/require"
)

func newKey(t *testing.T) []byte {
	t.Helper()
	k := make([]byte, 32)
	_, err := rand.Read(k)
	require.NoError(t, err)
	return k
}

func TestEncryptDecrypt_RoundTrip(t *testing.T) {
	c, err := NewCipher(newKey(t))
	require.NoError(t, err)

	plain := []byte("BQDdK0z...spotify access token...")
	ciphertext, err := c.Encrypt(plain)
	require.NoError(t, err)
	require.NotEqual(t, plain, ciphertext)

	out, err := c.Decrypt(ciphertext)
	require.NoError(t, err)
	require.Equal(t, plain, out)
}

func TestEncrypt_UniqueNoncesProduceDifferentCiphertexts(t *testing.T) {
	c, err := NewCipher(newKey(t))
	require.NoError(t, err)
	a, err := c.Encrypt([]byte("same"))
	require.NoError(t, err)
	b, err := c.Encrypt([]byte("same"))
	require.NoError(t, err)
	require.NotEqual(t, a, b)
}

func TestNewCipher_WrongKeySize(t *testing.T) {
	_, err := NewCipher([]byte("too short"))
	require.Error(t, err)
}

func TestDecrypt_TamperedCiphertextRejected(t *testing.T) {
	c, err := NewCipher(newKey(t))
	require.NoError(t, err)
	ciphertext, err := c.Encrypt([]byte("hello"))
	require.NoError(t, err)
	ciphertext[len(ciphertext)-1] ^= 0xFF
	_, err = c.Decrypt(ciphertext)
	require.Error(t, err)
}
```

- [ ] **Step 2: Run test to confirm it fails**

```bash
go test ./internal/crypto -v
```

Expected: FAIL — `package crypto; no Go files`.

- [ ] **Step 3: Implement**

`internal/crypto/aesgcm.go`:

```go
// Package crypto provides authenticated symmetric encryption for at-rest
// secrets (e.g., OAuth refresh tokens). 32-byte key → AES-256-GCM.
// The output is [nonce | ciphertext+tag]; Decrypt unpacks the nonce.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
)

type Cipher struct {
	aead cipher.AEAD
}

// NewCipher builds an AES-256-GCM cipher from a 32-byte key.
func NewCipher(key []byte) (*Cipher, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("key must be 32 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("aes new: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("gcm new: %w", err)
	}
	return &Cipher{aead: aead}, nil
}

// Encrypt returns nonce||ciphertext||tag (12-byte nonce).
func (c *Cipher) Encrypt(plaintext []byte) ([]byte, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("nonce: %w", err)
	}
	return c.aead.Seal(nonce, nonce, plaintext, nil), nil
}

// Decrypt unpacks nonce||ciphertext, verifying the GCM tag.
func (c *Cipher) Decrypt(blob []byte) ([]byte, error) {
	ns := c.aead.NonceSize()
	if len(blob) < ns {
		return nil, errors.New("ciphertext too short")
	}
	nonce, ciphertext := blob[:ns], blob[ns:]
	out, err := c.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("open: %w", err)
	}
	return out, nil
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/crypto -v
```

Expected: all 4 tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/crypto/aesgcm.go internal/crypto/aesgcm_test.go
git commit -m "feat(crypto): AES-256-GCM helper for at-rest secrets"
```

---

### Task 4: Config additions — Spotify + crypto + interests queue

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`
- Modify: `.env.example`

- [ ] **Step 1: Append `.env.example`**

```
# Spotify integration
SPOTIFY_CLIENT_ID=your-client-id
SPOTIFY_CLIENT_SECRET=your-client-secret
SPOTIFY_REDIRECT_URI=http://localhost:8080/integrations/spotify/callback
# 32 bytes, base64-encoded. Generate: openssl rand -base64 32
SPOTIFY_TOKEN_ENC_KEY=ZGV2LW9ubHkta2V5LWRldi1vbmx5LWtleS1kZXYtb25seS1rZXkxMjM=

# Interests queue (ElasticMQ in dev; real SQS in prod)
INTERESTS_QUEUE_URL=http://localhost:9324/000000000000/interests-queue
```

(The default `SPOTIFY_TOKEN_ENC_KEY` is exactly 32 bytes when base64-decoded; replace with a real key for non-dev use.)

- [ ] **Step 2: Append failing tests to `internal/config/config_test.go`**

```go
func TestLoad_SpotifyAndCryptoFields(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("JWT_SIGNING_KEY", "k")
	t.Setenv("SPOTIFY_CLIENT_ID", "cid")
	t.Setenv("SPOTIFY_CLIENT_SECRET", "secret")
	t.Setenv("SPOTIFY_REDIRECT_URI", "http://localhost:8080/x")
	t.Setenv("SPOTIFY_TOKEN_ENC_KEY", "ZGV2LW9ubHkta2V5LWRldi1vbmx5LWtleS1kZXYtb25seS1rZXkxMjM=")
	t.Setenv("INTERESTS_QUEUE_URL", "http://localhost:9324/000000000000/interests-queue")

	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, "cid", cfg.SpotifyClientID)
	require.Equal(t, "secret", cfg.SpotifyClientSecret)
	require.Equal(t, "http://localhost:8080/x", cfg.SpotifyRedirectURI)
	require.Len(t, cfg.SpotifyTokenEncKey, 32)
	require.Equal(t, "http://localhost:9324/000000000000/interests-queue", cfg.InterestsQueueURL)
}

func TestLoad_BadEncKey(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("JWT_SIGNING_KEY", "k")
	t.Setenv("SPOTIFY_TOKEN_ENC_KEY", "not-valid-base64!@#")
	_, err := Load()
	require.Error(t, err)
}
```

- [ ] **Step 3: Run tests to confirm they fail**

```bash
go test ./internal/config -v -run "Spotify|BadEncKey"
```

Expected: FAIL — `cfg.SpotifyClientID undefined`.

- [ ] **Step 4: Extend `internal/config/config.go`**

Add fields to the `Config` struct (preserve existing):

```go
type Config struct {
	// ... existing fields ...

	// Plan 3 additions
	SpotifyClientID     string
	SpotifyClientSecret string
	SpotifyRedirectURI  string
	SpotifyTokenEncKey  []byte
	InterestsQueueURL   string
}
```

In `Load()`, after the existing Plan 2 block and before the return, add:

```go
	var encKey []byte
	if v := os.Getenv("SPOTIFY_TOKEN_ENC_KEY"); v != "" {
		decoded, err := base64.StdEncoding.DecodeString(v)
		if err != nil {
			return nil, fmt.Errorf("SPOTIFY_TOKEN_ENC_KEY: %w", err)
		}
		encKey = decoded
	}
```

Then add the new fields to the `&Config{...}` literal:

```go
		SpotifyClientID:     os.Getenv("SPOTIFY_CLIENT_ID"),
		SpotifyClientSecret: os.Getenv("SPOTIFY_CLIENT_SECRET"),
		SpotifyRedirectURI:  os.Getenv("SPOTIFY_REDIRECT_URI"),
		SpotifyTokenEncKey:  encKey,
		InterestsQueueURL:   os.Getenv("INTERESTS_QUEUE_URL"),
```

Add `"encoding/base64"` to the imports.

- [ ] **Step 5: Run all config tests**

```bash
go test ./internal/config -v
```

Expected: all PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go .env.example
git commit -m "feat(config): Spotify OAuth + token-encryption + interests-queue env vars"
```

---

### Task 5: Add `interests-queue` to ElasticMQ config

**Files:**
- Modify: `scripts/elasticmq.conf`

- [ ] **Step 1: Update `scripts/elasticmq.conf`**

Replace the existing `queues { ... }` block with:

```hocon
queues {
  events-queue {
    defaultVisibilityTimeout = 30 seconds
    receiveMessageWait       = 20 seconds
    deadLettersQueue {
      name              = events-dlq
      maxReceiveCount   = 3
    }
  }
  events-dlq {}

  interests-queue {
    defaultVisibilityTimeout = 30 seconds
    receiveMessageWait       = 20 seconds
    deadLettersQueue {
      name              = interests-dlq
      maxReceiveCount   = 3
    }
  }
  interests-dlq {}
}
```

- [ ] **Step 2: Restart ElasticMQ and verify**

```bash
make queue-reset
sleep 3
curl -s "http://localhost:9324/?Action=ListQueues" | grep -E "(events|interests)"
```

Expected: lists all four queues — events-queue, events-dlq, interests-queue, interests-dlq.

- [ ] **Step 3: Commit**

```bash
git add scripts/elasticmq.conf
git commit -m "feat: add interests-queue + interests-dlq to ElasticMQ"
```

---

### Task 6: `InterestMessage` canonical type

**Files:**
- Create: `internal/events/interest.go`
- Create: `internal/events/interest_test.go`

- [ ] **Step 1: Write failing test**

`internal/events/interest_test.go`:

```go
package events

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestInterestMessage_JSONRoundTrip(t *testing.T) {
	m := InterestMessage{
		UserID: "11111111-1111-1111-1111-111111111111",
		SpotifyTopArtists: []SpotifyTopItem{
			{Name: "Phoebe Bridgers", Rank: 1},
			{Name: "MUNA", Rank: 2},
		},
		SpotifyTopGenres: []SpotifyTopItem{
			{Name: "indie rock", Rank: 1},
		},
		FetchedAt: time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
	}
	data, err := json.Marshal(m)
	require.NoError(t, err)

	var out InterestMessage
	require.NoError(t, json.Unmarshal(data, &out))
	require.Equal(t, m.UserID, out.UserID)
	require.Equal(t, m.SpotifyTopArtists, out.SpotifyTopArtists)
	require.Equal(t, m.SpotifyTopGenres, out.SpotifyTopGenres)
}

func TestInterestMessage_OmitsEmptyArrays(t *testing.T) {
	m := InterestMessage{UserID: "u", FetchedAt: time.Now()}
	data, err := json.Marshal(m)
	require.NoError(t, err)
	require.NotContains(t, string(data), `"spotify_top_artists"`)
	require.NotContains(t, string(data), `"spotify_top_genres"`)
}
```

- [ ] **Step 2: Run test to confirm it fails**

```bash
go test ./internal/events -v -run Interest
```

Expected: FAIL — `undefined: InterestMessage`.

- [ ] **Step 3: Implement**

`internal/events/interest.go`:

```go
package events

import "time"

// InterestMessage carries one user's snapshot of Spotify-derived interests
// from the spotify-scraper to the ingest consumer. Manual tags do not flow
// through this message; they're written directly by the API.
type InterestMessage struct {
	UserID            string           `json:"user_id"`
	SpotifyTopArtists []SpotifyTopItem `json:"spotify_top_artists,omitempty"`
	SpotifyTopGenres  []SpotifyTopItem `json:"spotify_top_genres,omitempty"`
	FetchedAt         time.Time        `json:"fetched_at"`
}

// SpotifyTopItem represents a ranked Spotify entity (artist name or genre tag)
// where Rank starts at 1 for the most-listened.
type SpotifyTopItem struct {
	Name string `json:"name"`
	Rank int    `json:"rank"`
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/events -v
```

Expected: all events tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/events/interest.go internal/events/interest_test.go
git commit -m "feat(events): InterestMessage and SpotifyTopItem"
```

---

### Task 7: PKCE helpers

**Files:**
- Create: `internal/spotify/pkce.go`
- Create: `internal/spotify/pkce_test.go`

- [ ] **Step 1: Write failing test**

`internal/spotify/pkce_test.go`:

```go
package spotify

import (
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewVerifier_RFC7636Length(t *testing.T) {
	v, err := NewVerifier()
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(v), 43)
	require.LessOrEqual(t, len(v), 128)
}

func TestNewVerifier_UniquePerCall(t *testing.T) {
	a, err := NewVerifier()
	require.NoError(t, err)
	b, err := NewVerifier()
	require.NoError(t, err)
	require.NotEqual(t, a, b)
}

func TestChallenge_MatchesSpec(t *testing.T) {
	verifier := "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGH" // 44 chars
	h := sha256.Sum256([]byte(verifier))
	want := base64.RawURLEncoding.EncodeToString(h[:])

	got := Challenge(verifier)
	require.Equal(t, want, got)
	require.False(t, strings.Contains(got, "="), "padding must be stripped")
}
```

- [ ] **Step 2: Run test**

```bash
go test ./internal/spotify -v
```

Expected: FAIL — `undefined: NewVerifier, Challenge`.

- [ ] **Step 3: Implement**

`internal/spotify/pkce.go`:

```go
// Package spotify implements the OAuth + Web API client for Spotify.
// This file: PKCE helpers per RFC 7636.
package spotify

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
)

// NewVerifier returns a fresh PKCE code_verifier: a base64url-encoded 48-byte
// random string (64 chars without padding, within the 43..128 spec range).
func NewVerifier() (string, error) {
	buf := make([]byte, 48)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("rand: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// Challenge returns the PKCE code_challenge for a verifier using the S256 method.
func Challenge(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/spotify -v
```

Expected: all 3 tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/spotify/pkce.go internal/spotify/pkce_test.go
git commit -m "feat(spotify): PKCE verifier + S256 challenge helpers"
```

---

### Task 8: Spotify Web API client (token exchange + top artists)

**Files:**
- Create: `internal/spotify/client.go`
- Create: `internal/spotify/client_test.go`

- [ ] **Step 1: Write failing test**

`internal/spotify/client_test.go`:

```go
package spotify

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAuthorizeURL_IncludesPKCEAndScopes(t *testing.T) {
	c := New("client-id", "client-secret", "http://localhost/cb", "")
	u := c.AuthorizeURL("state-xyz", "challenge-xyz")
	require.True(t, strings.HasPrefix(u, "https://accounts.spotify.com/authorize?"))
	require.Contains(t, u, "client_id=client-id")
	require.Contains(t, u, "redirect_uri=http%3A%2F%2Flocalhost%2Fcb")
	require.Contains(t, u, "response_type=code")
	require.Contains(t, u, "state=state-xyz")
	require.Contains(t, u, "code_challenge=challenge-xyz")
	require.Contains(t, u, "code_challenge_method=S256")
	require.Contains(t, u, "scope=user-top-read+user-read-recently-played")
}

func TestExchangeCode_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/token", r.URL.Path)
		require.NoError(t, r.ParseForm())
		require.Equal(t, "authorization_code", r.Form.Get("grant_type"))
		require.Equal(t, "the-code", r.Form.Get("code"))
		require.Equal(t, "the-verifier", r.Form.Get("code_verifier"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
		  "access_token": "AT",
		  "refresh_token": "RT",
		  "expires_in": 3600,
		  "scope": "user-top-read user-read-recently-played",
		  "token_type": "Bearer"
		}`))
	}))
	defer srv.Close()

	c := New("cid", "csec", "http://localhost/cb", srv.URL)
	tok, err := c.ExchangeCode(context.Background(), "the-code", "the-verifier")
	require.NoError(t, err)
	require.Equal(t, "AT", tok.AccessToken)
	require.Equal(t, "RT", tok.RefreshToken)
	require.Equal(t, 3600, tok.ExpiresIn)
	require.Contains(t, tok.Scope, "user-top-read")
}

func TestExchangeCode_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	}))
	defer srv.Close()
	c := New("cid", "csec", "http://localhost/cb", srv.URL)
	_, err := c.ExchangeCode(context.Background(), "x", "y")
	require.Error(t, err)
}

func TestRefreshToken_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/token", r.URL.Path)
		require.NoError(t, r.ParseForm())
		require.Equal(t, "refresh_token", r.Form.Get("grant_type"))
		require.Equal(t, "old-RT", r.Form.Get("refresh_token"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
		  "access_token": "NEW-AT",
		  "expires_in": 3600,
		  "scope": "user-top-read",
		  "token_type": "Bearer"
		}`))
	}))
	defer srv.Close()
	c := New("cid", "csec", "http://localhost/cb", srv.URL)
	tok, err := c.RefreshToken(context.Background(), "old-RT")
	require.NoError(t, err)
	require.Equal(t, "NEW-AT", tok.AccessToken)
	// Spotify often omits refresh_token on refresh; client returns "" for it.
	require.Equal(t, "", tok.RefreshToken)
}

func TestGetTopArtists_Parses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/me/top/artists", r.URL.Path)
		require.Equal(t, "Bearer AT", r.Header.Get("Authorization"))
		require.Equal(t, "medium_term", r.URL.Query().Get("time_range"))
		require.Equal(t, "50", r.URL.Query().Get("limit"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
		  "items": [
		    {"name": "Phoebe Bridgers", "genres": ["indie pop", "indie rock"]},
		    {"name": "MUNA", "genres": ["indie pop"]}
		  ]
		}`))
	}))
	defer srv.Close()
	c := New("cid", "csec", "http://localhost/cb", srv.URL)
	artists, err := c.GetTopArtists(context.Background(), "AT", 50)
	require.NoError(t, err)
	require.Len(t, artists, 2)
	require.Equal(t, "Phoebe Bridgers", artists[0].Name)
	require.ElementsMatch(t, []string{"indie pop", "indie rock"}, artists[0].Genres)
}
```

- [ ] **Step 2: Run test to confirm it fails**

```bash
go test ./internal/spotify -v
```

Expected: FAIL — `undefined: New, AuthorizeURL, ExchangeCode, RefreshToken, GetTopArtists`.

- [ ] **Step 3: Implement**

`internal/spotify/client.go`:

```go
package spotify

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	defaultAuthBase = "https://accounts.spotify.com"
	defaultAPIBase  = "https://api.spotify.com"
	scopes          = "user-top-read user-read-recently-played"
)

// Client is the Spotify OAuth + Web API client. Stateless; one instance can
// serve many users.
type Client struct {
	clientID     string
	clientSecret string
	redirectURI  string
	baseURL      string // overrides BOTH auth + api for tests
	http         *http.Client
}

// New builds a Client. If baseURL is empty, production Spotify endpoints are
// used. In tests, pass an httptest.Server URL — the client routes both
// /api/token and /v1/* through it.
func New(clientID, clientSecret, redirectURI, baseURL string) *Client {
	return &Client{
		clientID:     clientID,
		clientSecret: clientSecret,
		redirectURI:  redirectURI,
		baseURL:      baseURL,
		http:         &http.Client{Timeout: 15 * time.Second},
	}
}

func (c *Client) authBase() string {
	if c.baseURL != "" {
		return c.baseURL
	}
	return defaultAuthBase
}

func (c *Client) apiBase() string {
	if c.baseURL != "" {
		return c.baseURL
	}
	return defaultAPIBase
}

// AuthorizeURL returns the Spotify authorize URL the user must visit.
func (c *Client) AuthorizeURL(state, codeChallenge string) string {
	q := url.Values{}
	q.Set("client_id", c.clientID)
	q.Set("response_type", "code")
	q.Set("redirect_uri", c.redirectURI)
	q.Set("scope", scopes)
	q.Set("state", state)
	q.Set("code_challenge", codeChallenge)
	q.Set("code_challenge_method", "S256")
	return c.authBase() + "/authorize?" + q.Encode()
}

// Token represents the response from /api/token. On refresh the RefreshToken
// field may be empty — Spotify only rotates it when policy requires.
type Token struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
	TokenType    string `json:"token_type"`
}

// ExchangeCode trades an authorization code (+ PKCE verifier) for a Token.
func (c *Client) ExchangeCode(ctx context.Context, code, verifier string) (*Token, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", c.redirectURI)
	form.Set("client_id", c.clientID)
	form.Set("code_verifier", verifier)
	return c.tokenRequest(ctx, form)
}

// RefreshToken trades a refresh token for a new access token.
func (c *Client) RefreshToken(ctx context.Context, refreshToken string) (*Token, error) {
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)
	form.Set("client_id", c.clientID)
	return c.tokenRequest(ctx, form)
}

func (c *Client) tokenRequest(ctx context.Context, form url.Values) (*Token, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.authBase()+"/api/token", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("token endpoint %d: %s", resp.StatusCode, string(body))
	}
	var t Token
	if err := json.NewDecoder(resp.Body).Decode(&t); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return &t, nil
}

// Artist is one entry in GetTopArtists.
type Artist struct {
	Name   string   `json:"name"`
	Genres []string `json:"genres"`
}

// GetTopArtists returns the user's top artists (medium_term, max 50).
func (c *Client) GetTopArtists(ctx context.Context, accessToken string, limit int) ([]Artist, error) {
	if limit <= 0 || limit > 50 {
		limit = 50
	}
	endpoint := fmt.Sprintf("%s/v1/me/top/artists?time_range=medium_term&limit=%s",
		c.apiBase(), strconv.Itoa(limit))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("top artists %d: %s", resp.StatusCode, string(body))
	}
	var payload struct {
		Items []Artist `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return payload.Items, nil
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/spotify -v
```

Expected: all 5 tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/spotify/client.go internal/spotify/client_test.go
git commit -m "feat(spotify): OAuth + Web API client (authorize, exchange, refresh, top artists)"
```

---

### Task 9: OAuth state cookie helper (HMAC-signed)

**Files:**
- Create: `internal/spotify/oauth_state.go`
- Create: `internal/spotify/oauth_state_test.go`

The Connect handler stamps a short-TTL cookie with the PKCE verifier and the state value. The Callback handler verifies and reads back. We HMAC-sign with the existing JWT key so we don't introduce another secret.

- [ ] **Step 1: Write failing test**

`internal/spotify/oauth_state_test.go`:

```go
package spotify

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSealOpen_RoundTrip(t *testing.T) {
	key := []byte("test-key-test-key-test-key-32xx")
	cookie, err := SealOAuthState(key, "the-state", "the-verifier", time.Minute)
	require.NoError(t, err)
	require.NotEmpty(t, cookie)

	state, verifier, err := OpenOAuthState(key, cookie)
	require.NoError(t, err)
	require.Equal(t, "the-state", state)
	require.Equal(t, "the-verifier", verifier)
}

func TestOpen_ExpiredRejected(t *testing.T) {
	key := []byte("test-key-test-key-test-key-32xx")
	cookie, err := SealOAuthState(key, "s", "v", -time.Minute)
	require.NoError(t, err)
	_, _, err = OpenOAuthState(key, cookie)
	require.Error(t, err)
}

func TestOpen_TamperedRejected(t *testing.T) {
	key := []byte("test-key-test-key-test-key-32xx")
	cookie, err := SealOAuthState(key, "s", "v", time.Minute)
	require.NoError(t, err)
	// flip one character of the cookie value
	bad := cookie[:len(cookie)-1] + "X"
	_, _, err = OpenOAuthState(key, bad)
	require.Error(t, err)
}
```

- [ ] **Step 2: Run test to confirm it fails**

```bash
go test ./internal/spotify -v -run OAuthState
```

Expected: FAIL.

- [ ] **Step 3: Implement**

`internal/spotify/oauth_state.go`:

```go
package spotify

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// SealOAuthState packs state + verifier into a base64url-encoded JSON
// blob and appends an HMAC-SHA256 signature. The full cookie value is
// "base64(json).base64(mac)".
func SealOAuthState(key []byte, state, verifier string, ttl time.Duration) (string, error) {
	payload := struct {
		State    string `json:"s"`
		Verifier string `json:"v"`
		Expires  int64  `json:"e"`
	}{
		State:    state,
		Verifier: verifier,
		Expires:  time.Now().Add(ttl).Unix(),
	}
	body, err := json.Marshal(&payload)
	if err != nil {
		return "", fmt.Errorf("marshal: %w", err)
	}
	encoded := base64.RawURLEncoding.EncodeToString(body)

	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(encoded))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	return encoded + "." + sig, nil
}

// OpenOAuthState reverses SealOAuthState, verifying the signature and
// expiration. Returns (state, verifier) on success.
func OpenOAuthState(key []byte, cookie string) (string, string, error) {
	var encoded, sig string
	for i := 0; i < len(cookie); i++ {
		if cookie[i] == '.' {
			encoded, sig = cookie[:i], cookie[i+1:]
			break
		}
	}
	if encoded == "" || sig == "" {
		return "", "", errors.New("malformed cookie")
	}

	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(encoded))
	want := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(sig), []byte(want)) {
		return "", "", errors.New("bad signature")
	}

	body, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return "", "", fmt.Errorf("decode: %w", err)
	}
	var payload struct {
		State    string `json:"s"`
		Verifier string `json:"v"`
		Expires  int64  `json:"e"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", "", fmt.Errorf("unmarshal: %w", err)
	}
	if time.Now().Unix() > payload.Expires {
		return "", "", errors.New("expired")
	}
	return payload.State, payload.Verifier, nil
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/spotify -v
```

Expected: all spotify package tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/spotify/oauth_state.go internal/spotify/oauth_state_test.go
git commit -m "feat(spotify): HMAC-signed OAuth state cookie (state + verifier)"
```

---

### Task 10: Spotify scraper adapter (`ScrapeOne(userID) → publish InterestMessage`)

**Files:**
- Create: `internal/scraper/spotify/adapter.go`
- Create: `internal/scraper/spotify/adapter_test.go`

This is the bridge from DB (read encrypted tokens) → spotify.Client (fetch, refreshing if needed) → queue (publish InterestMessage). Unlike Plan 2's event scraper, this one is parameterized by user (you can scrape one user on-connect, or iterate all users for a daily run).

- [ ] **Step 1: Write failing test**

`internal/scraper/spotify/adapter_test.go`:

```go
package spotify_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/crypto"
	"github.com/wmyers/heres-whats-happening/internal/events"
	spotifyclient "github.com/wmyers/heres-whats-happening/internal/spotify"
	spotifyscrape "github.com/wmyers/heres-whats-happening/internal/scraper/spotify"
	"github.com/wmyers/heres-whats-happening/internal/store"
	"github.com/wmyers/heres-whats-happening/internal/testdb"
)

func makeTestKey(t *testing.T) []byte {
	t.Helper()
	k := make([]byte, 32)
	for i := range k {
		k[i] = byte(i)
	}
	return k
}

type fakePublisher struct {
	sent [][]byte
}

func (p *fakePublisher) Send(ctx context.Context, queueURL string, body []byte) error {
	p.sent = append(p.sent, body)
	return nil
}

func TestScrapeOne_PublishesInterestMessage(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)

	// Create a user
	ctx := context.Background()
	city, err := q.GetDefaultCity(ctx)
	require.NoError(t, err)
	userRow, err := q.CreateUser(ctx, store.CreateUserParams{
		Email:        "spotify-scrape@example.com",
		PasswordHash: "stub",
		CityID:       city.ID,
	})
	require.NoError(t, err)

	// Encrypt + store fake Spotify tokens
	key := makeTestKey(t)
	cipher, err := crypto.NewCipher(key)
	require.NoError(t, err)
	at, _ := cipher.Encrypt([]byte("AT-original"))
	rt, _ := cipher.Encrypt([]byte("RT-original"))
	require.NoError(t, q.UpsertUserSpotifyTokens(ctx, store.UpsertUserSpotifyTokensParams{
		UserID:           userRow.ID,
		AccessTokenEnc:   at,
		RefreshTokenEnc:  rt,
		ExpiresAt:        pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
		Scope:            "user-top-read user-read-recently-played",
	}))

	// Mock Spotify Web API
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/me/top/artists", r.URL.Path)
		require.Equal(t, "Bearer AT-original", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
		  "items": [
		    {"name": "Phoebe Bridgers", "genres": ["indie pop", "indie rock"]},
		    {"name": "MUNA", "genres": ["indie pop"]}
		  ]
		}`))
	}))
	defer srv.Close()

	client := spotifyclient.New("cid", "csec", "http://localhost/cb", srv.URL)
	pub := &fakePublisher{}
	adapter := spotifyscrape.NewAdapter(q, cipher, client, pub, "http://localhost/interests-queue")

	require.NoError(t, adapter.ScrapeOne(ctx, userRow.ID))
	require.Len(t, pub.sent, 1)

	var msg events.InterestMessage
	require.NoError(t, json.Unmarshal(pub.sent[0], &msg))
	require.Len(t, msg.SpotifyTopArtists, 2)
	require.Equal(t, "Phoebe Bridgers", msg.SpotifyTopArtists[0].Name)
	require.Equal(t, 1, msg.SpotifyTopArtists[0].Rank)
	// Genres aggregated unique across artists, ranked by frequency.
	// indie pop appears in 2 artists → rank 1; indie rock in 1 → rank 2.
	require.Equal(t, "indie pop", msg.SpotifyTopGenres[0].Name)
	require.Equal(t, "indie rock", msg.SpotifyTopGenres[1].Name)
}

func TestScrapeOne_RefreshesExpiredToken(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()
	city, err := q.GetDefaultCity(ctx)
	require.NoError(t, err)
	userRow, err := q.CreateUser(ctx, store.CreateUserParams{
		Email:        "refresh@example.com",
		PasswordHash: "stub",
		CityID:       city.ID,
	})
	require.NoError(t, err)

	key := makeTestKey(t)
	cipher, _ := crypto.NewCipher(key)
	at, _ := cipher.Encrypt([]byte("AT-expired"))
	rt, _ := cipher.Encrypt([]byte("RT-current"))
	require.NoError(t, q.UpsertUserSpotifyTokens(ctx, store.UpsertUserSpotifyTokensParams{
		UserID:          userRow.ID,
		AccessTokenEnc:  at,
		RefreshTokenEnc: rt,
		ExpiresAt:       pgtype.Timestamptz{Time: time.Now().Add(-time.Minute), Valid: true}, // expired
		Scope:           "user-top-read",
	}))

	tokenCalls := 0
	apiCalls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/token":
			tokenCalls++
			require.NoError(t, r.ParseForm())
			require.Equal(t, "refresh_token", r.Form.Get("grant_type"))
			require.Equal(t, "RT-current", r.Form.Get("refresh_token"))
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"AT-new","expires_in":3600,"scope":"user-top-read","token_type":"Bearer"}`))
		case "/v1/me/top/artists":
			apiCalls++
			require.Equal(t, "Bearer AT-new", r.Header.Get("Authorization"))
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"items":[{"name":"X","genres":["jazz"]}]}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	client := spotifyclient.New("cid", "csec", "http://localhost/cb", srv.URL)
	pub := &fakePublisher{}
	adapter := spotifyscrape.NewAdapter(q, cipher, client, pub, "http://localhost/q")

	require.NoError(t, adapter.ScrapeOne(ctx, userRow.ID))
	require.Equal(t, 1, tokenCalls)
	require.Equal(t, 1, apiCalls)

	// Refreshed AT was persisted (encrypted).
	row, err := q.GetUserSpotifyTokens(ctx, userRow.ID)
	require.NoError(t, err)
	decoded, err := cipher.Decrypt(row.AccessTokenEnc)
	require.NoError(t, err)
	require.Equal(t, "AT-new", string(decoded))
}
```

- [ ] **Step 2: Run test to confirm it fails**

```bash
go test ./internal/scraper/spotify -v
```

Expected: FAIL — `undefined: NewAdapter`.

- [ ] **Step 3: Implement**

`internal/scraper/spotify/adapter.go`:

```go
// Package spotify (under internal/scraper) bridges the user_spotify_tokens
// table → the Spotify Web API → the interests-queue. Stateless apart from
// what's stored in the DB.
package spotify

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wmyers/heres-whats-happening/internal/crypto"
	"github.com/wmyers/heres-whats-happening/internal/events"
	"github.com/wmyers/heres-whats-happening/internal/spotify"
	"github.com/wmyers/heres-whats-happening/internal/store"
)

// Publisher matches scraper.Publisher (Send only).
type Publisher interface {
	Send(ctx context.Context, queueURL string, body []byte) error
}

// Adapter scrapes one or all connected users' Spotify data.
type Adapter struct {
	q         *store.Queries
	cipher    *crypto.Cipher
	client    *spotify.Client
	pub       Publisher
	queueURL  string
}

func NewAdapter(q *store.Queries, c *crypto.Cipher, client *spotify.Client, pub Publisher, queueURL string) *Adapter {
	return &Adapter{q: q, cipher: c, client: client, pub: pub, queueURL: queueURL}
}

// ScrapeOne fetches one user's top artists/genres and publishes an
// InterestMessage. Refreshes the access token if expired.
func (a *Adapter) ScrapeOne(ctx context.Context, userID pgtype.UUID) error {
	row, err := a.q.GetUserSpotifyTokens(ctx, userID)
	if err != nil {
		return fmt.Errorf("load tokens: %w", err)
	}

	accessToken, err := a.cipher.Decrypt(row.AccessTokenEnc)
	if err != nil {
		return fmt.Errorf("decrypt access: %w", err)
	}

	// Refresh if expired (or about to expire within 30s).
	if row.ExpiresAt.Time.Before(time.Now().Add(30 * time.Second)) {
		refreshToken, err := a.cipher.Decrypt(row.RefreshTokenEnc)
		if err != nil {
			return fmt.Errorf("decrypt refresh: %w", err)
		}
		tok, err := a.client.RefreshToken(ctx, string(refreshToken))
		if err != nil {
			return fmt.Errorf("refresh: %w", err)
		}
		newAT, err := a.cipher.Encrypt([]byte(tok.AccessToken))
		if err != nil {
			return fmt.Errorf("encrypt access: %w", err)
		}
		// Spotify may or may not return a new refresh token. Reuse the old one
		// if it didn't.
		newRT := row.RefreshTokenEnc
		if tok.RefreshToken != "" {
			newRT, err = a.cipher.Encrypt([]byte(tok.RefreshToken))
			if err != nil {
				return fmt.Errorf("encrypt refresh: %w", err)
			}
		}
		if err := a.q.UpsertUserSpotifyTokens(ctx, store.UpsertUserSpotifyTokensParams{
			UserID:          userID,
			AccessTokenEnc:  newAT,
			RefreshTokenEnc: newRT,
			ExpiresAt:       pgtype.Timestamptz{Time: time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second), Valid: true},
			Scope:           tok.Scope,
		}); err != nil {
			return fmt.Errorf("persist refreshed tokens: %w", err)
		}
		accessToken = []byte(tok.AccessToken)
	}

	// Fetch top artists.
	artists, err := a.client.GetTopArtists(ctx, string(accessToken), 50)
	if err != nil {
		return fmt.Errorf("get top artists: %w", err)
	}

	// Build InterestMessage. Genre rank = frequency across the top artists.
	msg := events.InterestMessage{
		UserID:    userIDString(userID),
		FetchedAt: time.Now().UTC(),
	}
	msg.SpotifyTopArtists = make([]events.SpotifyTopItem, 0, len(artists))
	genreCount := map[string]int{}
	for i, ar := range artists {
		msg.SpotifyTopArtists = append(msg.SpotifyTopArtists, events.SpotifyTopItem{
			Name: ar.Name,
			Rank: i + 1,
		})
		for _, g := range ar.Genres {
			genreCount[g]++
		}
	}

	type gc struct {
		name  string
		count int
	}
	gs := make([]gc, 0, len(genreCount))
	for name, count := range genreCount {
		gs = append(gs, gc{name, count})
	}
	sort.SliceStable(gs, func(i, j int) bool {
		if gs[i].count != gs[j].count {
			return gs[i].count > gs[j].count
		}
		return gs[i].name < gs[j].name
	})
	msg.SpotifyTopGenres = make([]events.SpotifyTopItem, 0, len(gs))
	for i, g := range gs {
		msg.SpotifyTopGenres = append(msg.SpotifyTopGenres, events.SpotifyTopItem{
			Name: g.name,
			Rank: i + 1,
		})
	}

	body, err := json.Marshal(&msg)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	if err := a.pub.Send(ctx, a.queueURL, body); err != nil {
		return fmt.Errorf("publish: %w", err)
	}
	if err := a.q.UpdateUserSpotifyTokensLastSynced(ctx, userID); err != nil {
		return fmt.Errorf("update last_synced: %w", err)
	}
	return nil
}

// ScrapeAll iterates all users with Spotify tokens and scrapes each. Errors
// per-user are logged via the returned slice (caller decides whether to fail
// the whole run).
func (a *Adapter) ScrapeAll(ctx context.Context) []error {
	rows, err := a.q.ListUserSpotifyTokens(ctx)
	if err != nil {
		return []error{fmt.Errorf("list users: %w", err)}
	}
	var errs []error
	for _, r := range rows {
		if err := a.ScrapeOne(ctx, r.UserID); err != nil {
			errs = append(errs, fmt.Errorf("user %s: %w", userIDString(r.UserID), err))
		}
	}
	return errs
}

// userIDString stringifies a pgtype.UUID. Matches the Plan 1 / Plan 2 pattern.
func userIDString(u pgtype.UUID) string {
	const hex = "0123456789abcdef"
	out := make([]byte, 36)
	j := 0
	for i := 0; i < 16; i++ {
		b := u.Bytes[i]
		out[j] = hex[b>>4]
		out[j+1] = hex[b&0x0F]
		j += 2
		switch i {
		case 3, 5, 7, 9:
			out[j] = '-'
			j++
		}
	}
	return string(out)
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/scraper/spotify -v
```

Expected: both tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/scraper/spotify/adapter.go internal/scraper/spotify/adapter_test.go
git commit -m "feat(scraper/spotify): ScrapeOne + ScrapeAll with token refresh"
```

---

### Task 11: Refactor `ingest.Consumer` to a generic MessageHandler interface

**Files:**
- Modify: `internal/ingest/consumer.go`
- Modify: `internal/ingest/events.go` — rename `Handler` to `EventHandler` and add `Handle(ctx, []byte) error`
- Modify: `internal/ingest/events_test.go` — update references
- Modify: `internal/ingest/consumer_test.go` — update references
- Modify: `internal/http/server.go` — update field type
- Modify: `cmd/app/main.go` — update construction

- [ ] **Step 1: Update `internal/ingest/consumer.go`**

Replace the file with the generalized version:

```go
package ingest

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"

	"github.com/wmyers/heres-whats-happening/internal/queue"
)

// QueueClient is the subset of *queue.Client the consumer needs.
type QueueClient interface {
	Receive(ctx context.Context, queueURL string, max int32, wait time.Duration) ([]queue.Message, error)
	Delete(ctx context.Context, queueURL, receiptHandle string) error
}

// MessageHandler is implemented by per-queue payload handlers.
// Body is the raw SQS message body; the handler is responsible for
// unmarshaling and applying it. Returning a non-nil error leaves the
// message on the queue for SQS-driven retry.
type MessageHandler interface {
	Handle(ctx context.Context, body []byte) error
}

// Consumer runs N worker goroutines long-polling one queue and dispatching
// each received message to the configured Handler.
type Consumer struct {
	q        QueueClient
	queueURL string
	h        MessageHandler
	workers  int
	name     string
}

func NewConsumer(q QueueClient, queueURL string, h MessageHandler, workers int, name string) *Consumer {
	if workers < 1 {
		workers = 1
	}
	if name == "" {
		name = "ingest"
	}
	return &Consumer{q: q, queueURL: queueURL, h: h, workers: workers, name: name}
}

func (c *Consumer) Run(ctx context.Context) error {
	var wg sync.WaitGroup
	for i := 0; i < c.workers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			c.workerLoop(ctx, id)
		}(i)
	}
	wg.Wait()
	return nil
}

func (c *Consumer) workerLoop(ctx context.Context, id int) {
	for {
		if ctx.Err() != nil {
			return
		}
		msgs, err := c.q.Receive(ctx, c.queueURL, 10, 20*time.Second)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			log.Printf("%s worker %d: receive: %v", c.name, id, err)
			time.Sleep(1 * time.Second)
			continue
		}
		for _, m := range msgs {
			c.handleOne(ctx, m, id)
		}
	}
}

func (c *Consumer) handleOne(ctx context.Context, m queue.Message, workerID int) {
	if err := c.h.Handle(ctx, m.Body); err != nil {
		log.Printf("%s worker %d: handle: %v", c.name, workerID, err)
		return
	}
	if err := c.q.Delete(ctx, c.queueURL, m.ReceiptHandle); err != nil {
		log.Printf("%s worker %d: delete: %v", c.name, workerID, err)
	}
}
```

- [ ] **Step 2: Rename `Handler` to `EventHandler` and add MessageHandler-compatible signature in `internal/ingest/events.go`**

Replace the type definition and add the new `Handle` method. Adjust signatures:

```go
// EventHandler applies an events.Message to the database.
type EventHandler struct {
	q      *store.Queries
	cityID pgtype.UUID
}

func NewEventHandler(q *store.Queries, cityID pgtype.UUID) *EventHandler {
	return &EventHandler{q: q, cityID: cityID}
}

// Handle decodes an SQS message body as an events.Message and applies it.
func (h *EventHandler) Handle(ctx context.Context, body []byte) error {
	var m events.Message
	if err := json.Unmarshal(body, &m); err != nil {
		// Malformed message — return nil so consumer deletes it (don't retry forever).
		log.Printf("ingest: bad event message: %v", err)
		return nil
	}
	return h.handleMessage(ctx, m)
}

func (h *EventHandler) handleMessage(ctx context.Context, m events.Message) error {
	// (the body of the previous Handle method goes here verbatim)
}
```

Add `"encoding/json"` and `"log"` to imports if not present. Rename the previous body (`q.GetEventSourceByName`, `UpsertVenue`, `UpsertEvent`, etc.) into the private `handleMessage`.

**Old `Handle(ctx, events.Message) error` is removed** — both call sites are inside this file. The public surface is now:
- `NewEventHandler(q, cityID) *EventHandler`
- `(*EventHandler).Handle(ctx, []byte) error`

- [ ] **Step 3: Update `internal/ingest/events_test.go`**

Find `ingest.NewHandler(q, cityID)` and replace with `ingest.NewEventHandler(q, cityID)`. Find `h.Handle(ctx, msg)` (where msg is `events.Message`) and inline the marshal:

```go
body, _ := json.Marshal(msg)
require.NoError(t, h.Handle(ctx, body))
```

Add `"encoding/json"` import if not present.

- [ ] **Step 4: Update `internal/ingest/consumer_test.go`**

- `ingest.NewHandler(q, cityID)` → `ingest.NewEventHandler(q, cityID)`
- `ingest.NewConsumer(qClient, queueURL, h, 1)` → `ingest.NewConsumer(qClient, queueURL, h, 1, "events")`

- [ ] **Step 5: Update `internal/http/server.go`**

The `Server.IngestConsumer *ingest.Consumer` field type is unchanged. Nothing to do.

- [ ] **Step 6: Update `cmd/app/main.go`**

In `serve()`, find the consumer construction and update:

```go
		h := ingest.NewEventHandler(q, city.ID)
		consumer = ingest.NewConsumer(qClient, cfg.EventsQueueURL, h, cfg.IngestWorkers, "events")
```

- [ ] **Step 7: Run the full suite**

```bash
go build ./...
make test
```

Expected: all PASS (5 ingest tests + everything else).

- [ ] **Step 8: Commit**

```bash
git add internal/ingest/ internal/http/server.go cmd/app/main.go
git commit -m "refactor(ingest): generic MessageHandler interface + rename Handler→EventHandler"
```

---

### Task 12: InterestHandler

**Files:**
- Create: `internal/ingest/interests.go`
- Create: `internal/ingest/interests_test.go`

- [ ] **Step 1: Write failing test**

`internal/ingest/interests_test.go`:

```go
package ingest_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/events"
	"github.com/wmyers/heres-whats-happening/internal/ingest"
	"github.com/wmyers/heres-whats-happening/internal/store"
	"github.com/wmyers/heres-whats-happening/internal/testdb"
)

func TestInterestHandler_WritesSpotifyArtistsAndGenres(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()

	city, err := q.GetDefaultCity(ctx)
	require.NoError(t, err)
	userRow, err := q.CreateUser(ctx, store.CreateUserParams{
		Email:        "interest-handler@example.com",
		PasswordHash: "stub",
		CityID:       city.ID,
	})
	require.NoError(t, err)

	msg := events.InterestMessage{
		UserID: pgtypeUUIDToString(t, userRow.ID),
		SpotifyTopArtists: []events.SpotifyTopItem{
			{Name: "Phoebe Bridgers", Rank: 1},
			{Name: "MUNA", Rank: 2},
		},
		SpotifyTopGenres: []events.SpotifyTopItem{
			{Name: "indie rock", Rank: 1},
			{Name: "indie pop", Rank: 2},
		},
		FetchedAt: time.Now(),
	}
	body, _ := json.Marshal(&msg)
	h := ingest.NewInterestHandler(q)
	require.NoError(t, h.Handle(ctx, body))

	rows, err := q.ListInterestsByUserAndKind(ctx, store.ListInterestsByUserAndKindParams{
		UserID: userRow.ID,
		Kind:   "spotify_top_artist",
	})
	require.NoError(t, err)
	require.Len(t, rows, 2)

	genres, err := q.ListInterestsByUserAndKind(ctx, store.ListInterestsByUserAndKindParams{
		UserID: userRow.ID,
		Kind:   "spotify_top_genre",
	})
	require.NoError(t, err)
	require.Len(t, genres, 2)
}

func TestInterestHandler_ReplaceSemantics(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()

	city, err := q.GetDefaultCity(ctx)
	require.NoError(t, err)
	userRow, err := q.CreateUser(ctx, store.CreateUserParams{
		Email:        "replace@example.com",
		PasswordHash: "stub",
		CityID:       city.ID,
	})
	require.NoError(t, err)

	h := ingest.NewInterestHandler(q)
	first := events.InterestMessage{
		UserID:            pgtypeUUIDToString(t, userRow.ID),
		SpotifyTopArtists: []events.SpotifyTopItem{{Name: "A", Rank: 1}, {Name: "B", Rank: 2}},
		FetchedAt:         time.Now(),
	}
	body1, _ := json.Marshal(&first)
	require.NoError(t, h.Handle(ctx, body1))

	second := events.InterestMessage{
		UserID:            pgtypeUUIDToString(t, userRow.ID),
		SpotifyTopArtists: []events.SpotifyTopItem{{Name: "C", Rank: 1}}, // entirely new
		FetchedAt:         time.Now(),
	}
	body2, _ := json.Marshal(&second)
	require.NoError(t, h.Handle(ctx, body2))

	rows, err := q.ListInterestsByUserAndKind(ctx, store.ListInterestsByUserAndKindParams{
		UserID: userRow.ID,
		Kind:   "spotify_top_artist",
	})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "C", rows[0].Value)
}
```

Note: the test uses `q.ListInterestsByUserAndKind` which doesn't exist yet. Add it now:

Append to `sql/queries/user_interests.sql`:

```sql
-- name: ListInterestsByUserAndKind :many
SELECT id, kind, value, normalized_value, weight
FROM user_interests
WHERE user_id = $1 AND kind = $2
ORDER BY weight DESC, normalized_value ASC;
```

Run `sqlc generate`. Then add a small helper at the bottom of the test file:

```go
import "github.com/google/uuid"

func pgtypeUUIDToString(t *testing.T, u pgtype.UUID) string {
	t.Helper()
	return uuid.UUID(u.Bytes).String()
}
```

(Adjust imports as needed; add `pgtype` and `uuid` imports.)

- [ ] **Step 2: Run test to confirm it fails**

```bash
sqlc generate
go test ./internal/ingest -v -run Interest
```

Expected: FAIL — `undefined: ingest.NewInterestHandler`.

- [ ] **Step 3: Implement**

`internal/ingest/interests.go`:

```go
package ingest

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wmyers/heres-whats-happening/internal/events"
	"github.com/wmyers/heres-whats-happening/internal/store"
)

// InterestHandler applies an InterestMessage to user_interests, replacing
// the user's Spotify-derived rows atomically per message.
//
// Weight scaling: rank 1 → 1.0; rank N → max(0.1, 1.0 - (N-1)*0.02). This
// gives top-1 full weight and ramps down gently so rank-50 still contributes.
type InterestHandler struct {
	q *store.Queries
}

func NewInterestHandler(q *store.Queries) *InterestHandler {
	return &InterestHandler{q: q}
}

func (h *InterestHandler) Handle(ctx context.Context, body []byte) error {
	var m events.InterestMessage
	if err := json.Unmarshal(body, &m); err != nil {
		log.Printf("ingest: bad interest message: %v", err)
		return nil // delete malformed
	}
	uid, err := uuid.Parse(m.UserID)
	if err != nil {
		log.Printf("ingest: bad user_id %q: %v", m.UserID, err)
		return nil
	}
	pgUID := pgtype.UUID{Bytes: uid, Valid: true}

	// Replace artists.
	if err := h.q.ReplaceSpotifyArtistInterests(ctx, pgUID); err != nil {
		return fmt.Errorf("delete artists: %w", err)
	}
	for _, item := range m.SpotifyTopArtists {
		w := rankWeight(item.Rank)
		if err := h.q.InsertSpotifyInterest(ctx, store.InsertSpotifyInterestParams{
			UserID:          pgUID,
			Kind:            "spotify_top_artist",
			Value:           item.Name,
			NormalizedValue: events.NormalizeString(item.Name),
			Weight:          w,
		}); err != nil {
			return fmt.Errorf("insert artist %q: %w", item.Name, err)
		}
	}

	// Replace genres.
	if err := h.q.ReplaceSpotifyGenreInterests(ctx, pgUID); err != nil {
		return fmt.Errorf("delete genres: %w", err)
	}
	for _, item := range m.SpotifyTopGenres {
		w := rankWeight(item.Rank)
		if err := h.q.InsertSpotifyInterest(ctx, store.InsertSpotifyInterestParams{
			UserID:          pgUID,
			Kind:            "spotify_top_genre",
			Value:           item.Name,
			NormalizedValue: events.NormalizeString(item.Name),
			Weight:          w,
		}); err != nil {
			return fmt.Errorf("insert genre %q: %w", item.Name, err)
		}
	}

	return nil
}

func rankWeight(rank int) float64 {
	w := 1.0 - float64(rank-1)*0.02
	if w < 0.1 {
		return 0.1
	}
	return w
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/ingest -v
```

Expected: all ingest tests PASS (event tests from Plan 2 + 2 new interest tests).

- [ ] **Step 5: Commit**

```bash
git add internal/ingest/interests.go internal/ingest/interests_test.go sql/queries/user_interests.sql internal/store/
git commit -m "feat(ingest): InterestHandler replaces Spotify-derived user_interests"
```

---

### Task 13: Spotify Connect handler

**Files:**
- Create: `internal/http/handlers/spotify.go`
- Create: `internal/http/handlers/spotify_test.go`

- [ ] **Step 1: Write failing test**

`internal/http/handlers/spotify_test.go`:

```go
package handlers_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/auth"
	"github.com/wmyers/heres-whats-happening/internal/http/handlers"
	"github.com/wmyers/heres-whats-happening/internal/http/middleware"
	"github.com/wmyers/heres-whats-happening/internal/spotify"
)

func TestSpotifyConnect_RedirectsWithPKCE(t *testing.T) {
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	client := spotify.New("cid", "csec", "http://localhost:8080/integrations/spotify/callback", "")
	access, err := signer.SignAccess(uuid.New())
	require.NoError(t, err)

	mw := middleware.RequireAuth(signer)
	h := mw(handlers.SpotifyConnect(client, []byte("test-key-test-key-test-key-32xx")))

	req := httptest.NewRequest(http.MethodGet, "/integrations/spotify/connect", nil)
	req.Header.Set("Authorization", "Bearer "+access)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusFound, rec.Code)
	loc := rec.Result().Header.Get("Location")
	require.True(t, strings.HasPrefix(loc, "https://accounts.spotify.com/authorize?"))
	require.Contains(t, loc, "code_challenge_method=S256")
	require.Contains(t, loc, "state=")
	require.Contains(t, loc, "code_challenge=")

	// Cookie set
	var found *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == "spotify_oauth" {
			found = c
		}
	}
	require.NotNil(t, found)
	require.True(t, found.HttpOnly)
}
```

Add the imports `"time"`, `"github.com/google/uuid"` at the top.

- [ ] **Step 2: Run test to confirm it fails**

```bash
go test ./internal/http/handlers -v -run SpotifyConnect
```

Expected: FAIL — `undefined: handlers.SpotifyConnect`.

- [ ] **Step 3: Implement**

`internal/http/handlers/spotify.go`:

```go
package handlers

import (
	"net/http"
	"time"

	"github.com/wmyers/heres-whats-happening/internal/http/httperr"
	"github.com/wmyers/heres-whats-happening/internal/spotify"
)

const oauthCookieName = "spotify_oauth"
const oauthCookieTTL = 10 * time.Minute

// SpotifyConnect builds the Spotify authorize URL with a fresh PKCE verifier
// and state, stores both in a signed cookie, and redirects the user there.
func SpotifyConnect(client *spotify.Client, hmacKey []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		verifier, err := spotify.NewVerifier()
		if err != nil {
			httperr.Write(w, http.StatusInternalServerError, "pkce_failed", "could not generate PKCE verifier")
			return
		}
		state, err := spotify.NewVerifier() // reuses random-string helper
		if err != nil {
			httperr.Write(w, http.StatusInternalServerError, "state_failed", "could not generate state")
			return
		}
		cookieValue, err := spotify.SealOAuthState(hmacKey, state, verifier, oauthCookieTTL)
		if err != nil {
			httperr.Write(w, http.StatusInternalServerError, "seal_failed", "could not seal oauth cookie")
			return
		}
		http.SetCookie(w, &http.Cookie{
			Name:     oauthCookieName,
			Value:    cookieValue,
			Path:     "/integrations/spotify",
			HttpOnly: true,
			Secure:   true,
			SameSite: http.SameSiteLaxMode,
			MaxAge:   int(oauthCookieTTL / time.Second),
		})
		challenge := spotify.Challenge(verifier)
		http.Redirect(w, r, client.AuthorizeURL(state, challenge), http.StatusFound)
	}
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/http/handlers -v -run SpotifyConnect
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/http/handlers/spotify.go internal/http/handlers/spotify_test.go
git commit -m "feat(http): GET /integrations/spotify/connect — PKCE + state cookie + 302"
```

---

### Task 14: Spotify Callback handler (with immediate sync)

**Files:**
- Modify: `internal/http/handlers/spotify.go` — append `SpotifyCallback`
- Modify: `internal/http/handlers/spotify_test.go` — append callback tests

- [ ] **Step 1: Append failing test**

```go
func TestSpotifyCallback_HappyPath(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	city, err := q.GetDefaultCity(context.Background())
	require.NoError(t, err)
	userRow, err := q.CreateUser(context.Background(), store.CreateUserParams{
		Email:        "spotify-cb@example.com",
		PasswordHash: "stub",
		CityID:       city.ID,
	})
	require.NoError(t, err)
	access, _ := signer.SignAccess(uuid.UUID(userRow.ID.Bytes))

	// Mock Spotify token + top-artists endpoints
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/token":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"AT","refresh_token":"RT","expires_in":3600,"scope":"user-top-read","token_type":"Bearer"}`))
		case "/v1/me/top/artists":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"items":[{"name":"X","genres":["jazz"]}]}`))
		}
	}))
	defer srv.Close()

	client := spotify.New("cid", "csec", "http://localhost:8080/integrations/spotify/callback", srv.URL)
	hmacKey := []byte("test-key-test-key-test-key-32xx")
	encKey := make([]byte, 32)
	for i := range encKey {
		encKey[i] = byte(i)
	}
	cipher, _ := crypto.NewCipher(encKey)
	pub := &fakePub{}

	// Seal a valid state cookie (10 min TTL)
	cookieValue, err := spotify.SealOAuthState(hmacKey, "STATE-XYZ", "VERIFIER-XYZ", time.Minute)
	require.NoError(t, err)

	h := middleware.RequireAuth(signer)(
		handlers.SpotifyCallback(q, client, cipher, hmacKey, pub, "http://q/interests-queue"))

	req := httptest.NewRequest(http.MethodGet,
		"/integrations/spotify/callback?code=THE-CODE&state=STATE-XYZ", nil)
	req.Header.Set("Authorization", "Bearer "+access)
	req.AddCookie(&http.Cookie{Name: "spotify_oauth", Value: cookieValue})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	// Tokens persisted (encrypted).
	tokRow, err := q.GetUserSpotifyTokens(context.Background(), userRow.ID)
	require.NoError(t, err)
	decoded, err := cipher.Decrypt(tokRow.AccessTokenEnc)
	require.NoError(t, err)
	require.Equal(t, "AT", string(decoded))

	// One InterestMessage published.
	require.Len(t, pub.sent, 1)
}

func TestSpotifyCallback_StateMismatch(t *testing.T) {
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	access, _ := signer.SignAccess(uuid.New())

	hmacKey := []byte("test-key-test-key-test-key-32xx")
	cookieValue, _ := spotify.SealOAuthState(hmacKey, "EXPECTED", "verifier", time.Minute)

	h := middleware.RequireAuth(signer)(handlers.SpotifyCallback(nil, nil, nil, hmacKey, nil, ""))

	req := httptest.NewRequest(http.MethodGet,
		"/integrations/spotify/callback?code=X&state=WRONG", nil)
	req.Header.Set("Authorization", "Bearer "+access)
	req.AddCookie(&http.Cookie{Name: "spotify_oauth", Value: cookieValue})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

type fakePub struct{ sent [][]byte }

func (p *fakePub) Send(ctx context.Context, qURL string, body []byte) error {
	p.sent = append(p.sent, body)
	return nil
}
```

Add imports `"context"`, `"github.com/wmyers/heres-whats-happening/internal/crypto"`, `"github.com/wmyers/heres-whats-happening/internal/store"`, `"github.com/wmyers/heres-whats-happening/internal/testdb"`.

- [ ] **Step 2: Run test to confirm it fails**

```bash
go test ./internal/http/handlers -v -run SpotifyCallback
```

Expected: FAIL — `undefined: handlers.SpotifyCallback`.

- [ ] **Step 3: Implement**

Append to `internal/http/handlers/spotify.go`:

```go
import (
	// ... existing imports plus:
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wmyers/heres-whats-happening/internal/crypto"
	"github.com/wmyers/heres-whats-happening/internal/events"
	"github.com/wmyers/heres-whats-happening/internal/http/middleware"
	"github.com/wmyers/heres-whats-happening/internal/scraper/spotify"  // alias if conflicting
	"github.com/wmyers/heres-whats-happening/internal/store"
)

// CallbackPublisher matches scraper.Publisher: Send(ctx, queueURL, body).
type CallbackPublisher interface {
	Send(ctx context.Context, queueURL string, body []byte) error
}

// SpotifyCallback handles the OAuth redirect-back from Spotify. It validates
// the state cookie, exchanges the code for tokens, persists them (encrypted),
// and triggers an immediate one-user scrape.
func SpotifyCallback(
	q *store.Queries,
	client *spotifyclient.Client,
	cipher *crypto.Cipher,
	hmacKey []byte,
	pub CallbackPublisher,
	queueURL string,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid, ok := middleware.UserIDFromContext(r.Context())
		if !ok {
			httperr.Write(w, http.StatusUnauthorized, "no_user", "user not in context")
			return
		}
		// Read+verify cookie
		c, err := r.Cookie(oauthCookieName)
		if err != nil || c.Value == "" {
			httperr.Write(w, http.StatusBadRequest, "no_state", "missing oauth state cookie")
			return
		}
		expectedState, verifier, err := spotifyclient.OpenOAuthState(hmacKey, c.Value)
		if err != nil {
			httperr.Write(w, http.StatusBadRequest, "bad_state", "oauth state is not valid")
			return
		}
		// Compare state param
		if r.URL.Query().Get("state") != expectedState {
			httperr.Write(w, http.StatusBadRequest, "state_mismatch", "oauth state does not match")
			return
		}
		code := r.URL.Query().Get("code")
		if code == "" {
			httperr.Write(w, http.StatusBadRequest, "no_code", "missing oauth code")
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()

		tok, err := client.ExchangeCode(ctx, code, verifier)
		if err != nil {
			httperr.Write(w, http.StatusBadGateway, "exchange_failed", "could not exchange code")
			return
		}
		atEnc, err := cipher.Encrypt([]byte(tok.AccessToken))
		if err != nil {
			httperr.Write(w, http.StatusInternalServerError, "encrypt_failed", "could not encrypt access token")
			return
		}
		rtEnc, err := cipher.Encrypt([]byte(tok.RefreshToken))
		if err != nil {
			httperr.Write(w, http.StatusInternalServerError, "encrypt_failed", "could not encrypt refresh token")
			return
		}
		pgUID := pgtype.UUID{Bytes: uid, Valid: true}
		if err := q.UpsertUserSpotifyTokens(ctx, store.UpsertUserSpotifyTokensParams{
			UserID:          pgUID,
			AccessTokenEnc:  atEnc,
			RefreshTokenEnc: rtEnc,
			ExpiresAt:       pgtype.Timestamptz{Time: time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second), Valid: true},
			Scope:           tok.Scope,
		}); err != nil {
			httperr.Write(w, http.StatusInternalServerError, "db_error", "could not persist tokens")
			return
		}

		// Clear cookie
		http.SetCookie(w, &http.Cookie{
			Name:     oauthCookieName,
			Value:    "",
			Path:     "/integrations/spotify",
			HttpOnly: true,
			Secure:   true,
			SameSite: http.SameSiteLaxMode,
			MaxAge:   -1,
		})

		// Immediate sync — build an Adapter and ScrapeOne. Best-effort: if it
		// fails, the OAuth connect still succeeds; the daily scraper will retry.
		adapter := spotifyscrape.NewAdapter(q, cipher, client, pub, queueURL)
		if err := adapter.ScrapeOne(ctx, pgUID); err != nil {
			// Log only; don't fail the OAuth flow.
			// (CallbackPublisher errors typically mean SQS is down — the user
			// is still connected.)
			_ = err
		}

		writeJSON(w, http.StatusOK, map[string]string{"status": "connected"})
		_ = json.RawMessage("") // suppress unused-import warning if json not otherwise used
	}
}
```

Imports need cleanup — `json` and any unused names. The actual final import block depends on what's already present in the file. Make sure these aliases are set so the test references compile:

```go
import (
	spotifyclient "github.com/wmyers/heres-whats-happening/internal/spotify"
	spotifyscrape "github.com/wmyers/heres-whats-happening/internal/scraper/spotify"
)
```

And update the previous `SpotifyConnect` function to use `spotifyclient.Client` if you renamed the import; otherwise the existing imports continue to work.

- [ ] **Step 4: Run tests**

```bash
go test ./internal/http/handlers -v -run Spotify
```

Expected: both Spotify tests PASS plus all prior handler tests.

- [ ] **Step 5: Commit**

```bash
git add internal/http/handlers/spotify.go internal/http/handlers/spotify_test.go
git commit -m "feat(http): GET /integrations/spotify/callback with immediate sync"
```

---

### Task 15: Spotify Disconnect handler

**Files:**
- Modify: `internal/http/handlers/spotify.go` — append `SpotifyDisconnect`
- Modify: `internal/http/handlers/spotify_test.go` — append disconnect test

- [ ] **Step 1: Append failing test**

```go
func TestSpotifyDisconnect_RemovesTokensAndInterests(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	city, _ := q.GetDefaultCity(ctx)
	userRow, _ := q.CreateUser(ctx, store.CreateUserParams{
		Email: "disconnect@example.com", PasswordHash: "stub", CityID: city.ID,
	})

	// Seed a token row
	_ = q.UpsertUserSpotifyTokens(ctx, store.UpsertUserSpotifyTokensParams{
		UserID:          userRow.ID,
		AccessTokenEnc:  []byte{1, 2, 3},
		RefreshTokenEnc: []byte{4, 5, 6},
		ExpiresAt:       pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
		Scope:           "user-top-read",
	})
	// Seed a Spotify-derived interest
	_ = q.InsertSpotifyInterest(ctx, store.InsertSpotifyInterestParams{
		UserID: userRow.ID, Kind: "spotify_top_artist",
		Value: "Phoebe Bridgers", NormalizedValue: "phoebe bridgers", Weight: 1.0,
	})

	access, _ := signer.SignAccess(uuid.UUID(userRow.ID.Bytes))
	h := middleware.RequireAuth(signer)(handlers.SpotifyDisconnect(q))

	req := httptest.NewRequest(http.MethodDelete, "/integrations/spotify", nil)
	req.Header.Set("Authorization", "Bearer "+access)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusNoContent, rec.Code)

	// Tokens gone
	_, err := q.GetUserSpotifyTokens(ctx, userRow.ID)
	require.Error(t, err)
	// Spotify-derived interests gone
	rows, err := q.ListInterestsByUserAndKind(ctx, store.ListInterestsByUserAndKindParams{
		UserID: userRow.ID, Kind: "spotify_top_artist",
	})
	require.NoError(t, err)
	require.Empty(t, rows)
}
```

- [ ] **Step 2: Run test to confirm it fails**

```bash
go test ./internal/http/handlers -v -run Disconnect
```

Expected: FAIL — `undefined: handlers.SpotifyDisconnect`.

- [ ] **Step 3: Implement**

Append to `internal/http/handlers/spotify.go`:

```go
// SpotifyDisconnect removes a user's Spotify tokens and all
// Spotify-derived interest rows. Manual tags are not touched.
func SpotifyDisconnect(q *store.Queries) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid, ok := middleware.UserIDFromContext(r.Context())
		if !ok {
			httperr.Write(w, http.StatusUnauthorized, "no_user", "user not in context")
			return
		}
		pgUID := pgtype.UUID{Bytes: uid, Valid: true}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		if err := q.DeleteSpotifyDerivedInterests(ctx, pgUID); err != nil {
			httperr.Write(w, http.StatusInternalServerError, "db_error", "could not delete interests")
			return
		}
		if err := q.DeleteUserSpotifyTokens(ctx, pgUID); err != nil {
			httperr.Write(w, http.StatusInternalServerError, "db_error", "could not delete tokens")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/http/handlers -v -run Spotify
```

Expected: all 3 Spotify tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/http/handlers/spotify.go internal/http/handlers/spotify_test.go
git commit -m "feat(http): DELETE /integrations/spotify"
```

---

### Task 16: Wire Spotify routes in `server.go`

**Files:**
- Modify: `internal/http/server.go`

- [ ] **Step 1: Extend `Server` struct**

Add fields the Spotify handlers need:

```go
type Server struct {
	// ... existing fields ...

	SpotifyClient      *spotify.Client
	SpotifyCipher      *crypto.Cipher
	OAuthHMACKey       []byte
	InterestsQueueURL  string
	QueuePublisher     handlers.CallbackPublisher // *queue.Client satisfies this
}
```

Add imports at top:

```go
	"github.com/wmyers/heres-whats-happening/internal/crypto"
	"github.com/wmyers/heres-whats-happening/internal/spotify"
```

- [ ] **Step 2: Register routes inside the authenticated group**

In `Router()`, inside the `r.Group(func(r chi.Router) { r.Use(middleware.RequireAuth(...)) ... })` block, append:

```go
		r.Get("/integrations/spotify/connect",  handlers.SpotifyConnect(s.SpotifyClient, s.OAuthHMACKey))
		r.Get("/integrations/spotify/callback", handlers.SpotifyCallback(
			s.Queries, s.SpotifyClient, s.SpotifyCipher, s.OAuthHMACKey,
			s.QueuePublisher, s.InterestsQueueURL))
		r.Delete("/integrations/spotify",        handlers.SpotifyDisconnect(s.Queries))
```

- [ ] **Step 3: Update `cmd/app/main.go`** to construct + pass the new fields:

After building the `queue.Client` (already done in Plan 2), and before constructing the Server, add:

```go
	spClient := spotify.New(cfg.SpotifyClientID, cfg.SpotifyClientSecret, cfg.SpotifyRedirectURI, "")
	var cipher *crypto.Cipher
	if len(cfg.SpotifyTokenEncKey) > 0 {
		cipher, err = crypto.NewCipher(cfg.SpotifyTokenEncKey)
		if err != nil {
			return fmt.Errorf("crypto: %w", err)
		}
	}
```

Add `spotify` and `crypto` imports.

Then in the `Server{...}` literal, add:

```go
		SpotifyClient:     spClient,
		SpotifyCipher:     cipher,
		OAuthHMACKey:      []byte(cfg.JWTSigningKey),
		InterestsQueueURL: cfg.InterestsQueueURL,
		QueuePublisher:    qClient, // *queue.Client; matches CallbackPublisher
```

`qClient` already exists from Plan 2's serve() — it's the `*queue.Client` built when `EVENTS_QUEUE_URL` is set. If `EVENTS_QUEUE_URL` is empty (Plan 1-only mode), `qClient` is nil — the Spotify callback handler will fail gracefully if invoked.

- [ ] **Step 4: Verify build + tests**

```bash
go build ./...
make test
```

- [ ] **Step 5: Smoke test the connect URL**

```bash
make queue-up && make run &
SERVE_PID=$!
sleep 1
# (Use a real browser for this — the connect endpoint requires auth)
# For now just confirm the server boots without panicking.
kill $SERVE_PID
```

- [ ] **Step 6: Commit**

```bash
git add internal/http/server.go cmd/app/main.go
git commit -m "feat: wire Spotify routes in HTTP server"
```

---

### Task 17: `app scrape spotify` subcommand

**Files:**
- Modify: `cmd/app/main.go`

- [ ] **Step 1: Extend the `scrape` dispatcher**

In `cmd/app/main.go`, the existing `scrape` function dispatches on `args[0]`. Currently it only handles `events`. Add a `spotify` case:

```go
func scrape(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf(`expected "app scrape events|spotify [...]"`)
	}
	switch args[0] {
	case "events":
		return scrapeEvents(args[1:])
	case "spotify":
		return scrapeSpotify(args[1:])
	default:
		return fmt.Errorf("unknown scrape target: %s", args[0])
	}
}
```

Move the existing inline events logic into `scrapeEvents(args []string) error`. Add `scrapeSpotify`:

```go
func scrapeSpotify(args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := db.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("db: %w", err)
	}
	defer pool.Close()
	q := store.New(pool)

	qClient, err := queue.NewClient(ctx, cfg.AWSRegion, cfg.SQSEndpoint)
	if err != nil {
		return fmt.Errorf("queue: %w", err)
	}
	spClient := spotify.New(cfg.SpotifyClientID, cfg.SpotifyClientSecret, cfg.SpotifyRedirectURI, "")
	cipher, err := crypto.NewCipher(cfg.SpotifyTokenEncKey)
	if err != nil {
		return fmt.Errorf("crypto: %w", err)
	}

	adapter := spotifyscrape.NewAdapter(q, cipher, spClient, qClient, cfg.InterestsQueueURL)
	errs := adapter.ScrapeAll(ctx)
	for _, e := range errs {
		fmt.Fprintf(os.Stderr, "scrape spotify error: %v\n", e)
	}
	if len(errs) > 0 {
		return fmt.Errorf("%d users failed", len(errs))
	}
	return nil
}
```

Add imports for `spotify` (`internal/spotify`), `spotifyscrape` (`internal/scraper/spotify`, aliased), `crypto`.

Update `usage()` to add `scrape spotify`:

```go
func usage() {
	fmt.Fprintf(os.Stderr, `usage: app <subcommand>

subcommands:
  serve                       run the HTTP API server
  scrape events --source=NAME run a one-shot event scraper
  scrape spotify              scrape all connected users' Spotify data
`)
}
```

- [ ] **Step 2: Verify build**

```bash
go build ./cmd/app
./app
./app scrape spotify
```

Expected: `usage` includes the new line; `./app scrape spotify` may fail with "queue client" or similar without proper env, but should not panic.

- [ ] **Step 3: Commit**

```bash
git add cmd/app/main.go
git commit -m "feat(cmd): scrape spotify subcommand"
```

---

### Task 18: Wire interest consumer into `app serve`

**Files:**
- Modify: `internal/http/server.go` — add a second consumer field
- Modify: `cmd/app/main.go` — construct it

- [ ] **Step 1: Extend `Server` struct**

```go
type Server struct {
	// ... existing fields ...

	IngestConsumer    *ingest.Consumer  // events queue
	InterestConsumer  *ingest.Consumer  // interests queue
}
```

In `Run`, start the InterestConsumer alongside the existing IngestConsumer:

```go
	if s.IngestConsumer != nil {
		go func() { errCh <- s.IngestConsumer.Run(ctx) }()
	}
	if s.InterestConsumer != nil {
		go func() { errCh <- s.InterestConsumer.Run(ctx) }()
	}
```

Also bump `errCh` buffer to 3.

- [ ] **Step 2: Construct in `serve()` in `cmd/app/main.go`**

After the existing IngestConsumer construction, add:

```go
	var interestConsumer *ingest.Consumer
	if cfg.InterestsQueueURL != "" && cipher != nil {
		ih := ingest.NewInterestHandler(q)
		interestConsumer = ingest.NewConsumer(qClient, cfg.InterestsQueueURL, ih, cfg.IngestWorkers, "interests")
	}
```

And pass `InterestConsumer: interestConsumer` into the Server literal.

- [ ] **Step 3: Verify build + tests**

```bash
go build ./...
make test
```

- [ ] **Step 4: Commit**

```bash
git add internal/http/server.go cmd/app/main.go
git commit -m "feat: wire interest consumer into app serve"
```

---

### Task 19: README updates

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Append Plan 3 quickstart**

````markdown

## Plan 3 quickstart — Spotify integration

```bash
# Prereqs: register a Spotify app at https://developer.spotify.com/dashboard
# Redirect URI: http://localhost:8080/integrations/spotify/callback
# Copy Client ID + Secret into .env
export SPOTIFY_CLIENT_ID=<your-id>
export SPOTIFY_CLIENT_SECRET=<your-secret>
export SPOTIFY_REDIRECT_URI=http://localhost:8080/integrations/spotify/callback

# Generate an at-rest encryption key for Spotify tokens
openssl rand -base64 32   # paste into .env as SPOTIFY_TOKEN_ENC_KEY

make db-up && make queue-up
make migrate && make migrate-test
make run    # starts api + events consumer + interests consumer
```

### Connect a user

1. Sign up + log in via the auth flow (Plan 1 quickstart).
2. Visit `http://localhost:8080/integrations/spotify/connect` in a browser
   with your access token in the `Authorization` header (use a REST client
   like Postman, or wrap in a small HTML form).
3. Spotify will redirect back to `/integrations/spotify/callback` — the
   server stores the encrypted tokens and immediately publishes one
   InterestMessage to the interests-queue. The consumer drains it.
4. Verify:

```bash
docker exec hwh_postgres psql -U app -d appdb -c \
  "SELECT kind, value, weight FROM user_interests \
   WHERE kind LIKE 'spotify%' ORDER BY weight DESC LIMIT 10;"
```

### Periodic scrape

```bash
./app scrape spotify   # iterates all connected users, publishes fresh InterestMessages
```
````

- [ ] **Step 2: Commit**

```bash
git add README.md
git commit -m "docs: Plan 3 Spotify integration quickstart"
```

---

## Self-Review

**Spec coverage check (Plan 3 scope only):**

| Spec requirement | Implemented in |
|---|---|
| `user_spotify_tokens` table | Task 1 |
| Encrypted at rest (AES-GCM) | Task 3 + Task 14 (callback) |
| sqlc queries for tokens | Task 2 |
| `interests-queue` + DLQ | Task 5 |
| InterestMessage canonical schema | Task 6 |
| OAuth Connect with PKCE + state | Tasks 7, 9, 13 |
| OAuth Callback with state verification | Task 14 |
| OAuth Disconnect | Task 15 |
| Spotify Web API client (token exchange + refresh + top artists) | Task 8 |
| Spotify scraper (`ScrapeOne` + `ScrapeAll`) | Task 10 |
| `app scrape spotify` subcommand | Task 17 |
| Token refresh logic on expired access tokens | Task 10 |
| Interest ingest consumer | Tasks 11, 12 |
| Replace semantics (artists/genres) per scrape | Task 12 |
| Consumer wired into `app serve` | Task 18 |
| On-connect immediate sync | Task 14 (calls ScrapeOne inline) |
| Spotify-derived interests removed on disconnect | Task 15 |

**Deferred to later plans (per spec, not Plan 3 scope):**

- TEI / embeddings / match-job (Plan 4)
- Calendar API + iCal feed (Plan 5)
- Frontend (Plan 6)
- Terraform / production deployment (Plans 7, 8)
- Last.fm / past-attendance importers (deferred indefinitely)

**Placeholder scan:** no "TBD" / "add error handling" / "handle edge cases" steps. Every code-touching step has full code blocks.

**Type consistency:**

- `events.InterestMessage` and `events.SpotifyTopItem` defined in Task 6, used in Tasks 10 (scraper), 12 (handler).
- `spotify.Client.AuthorizeURL/ExchangeCode/RefreshToken/GetTopArtists` defined in Task 8, used in Tasks 10, 13, 14, 17.
- `spotify.NewVerifier/Challenge` in Task 7, used in Tasks 13 (Connect handler).
- `spotify.SealOAuthState/OpenOAuthState` in Task 9, used in Tasks 13, 14.
- `crypto.Cipher` (NewCipher/Encrypt/Decrypt) defined in Task 3, used in Tasks 10, 14, 15-config (cipher constructor), 17, 18.
- `scraperspotify.NewAdapter/ScrapeOne/ScrapeAll` in Task 10, used in Tasks 14 (callback immediate sync), 17 (CLI).
- `ingest.MessageHandler` interface in Task 11, satisfied by both `*EventHandler` (Task 11) and `*InterestHandler` (Task 12).
- `ingest.NewConsumer(q, url, h, workers, name)` — extra `name` parameter added in Task 11; both Plan 2's call sites and Plan 3's new call sites must pass the name.

**Plan-internal consistency notes:**

- The Spotify Connect handler reuses `spotify.NewVerifier()` to generate the `state` value — they're both random base64url strings. The function is named "Verifier" because of its PKCE origin, but the bytes are appropriate for any random-token use.
- The OAuth state cookie's HMAC key is the same as the JWT signing key (Task 13/16). This is a deliberate v1 simplification — one key to rotate.
- The Spotify Disconnect handler deletes interests *before* deleting tokens (Task 15). Either order is correct; this order ensures the tokens persist if interest-delete fails, so the user is never left in a "no tokens but stale interests" state.
- Task 10's `userIDString` helper is a tight pgtype.UUID-to-canonical-string converter that doesn't depend on `github.com/google/uuid`. This is intentional — the scraper package shouldn't pull in the google/uuid dep just for this.
- Task 11 refactors `ingest.Consumer` to add a `name` parameter for logging. Plan 2's existing call sites (`cmd/app/main.go`, `internal/ingest/consumer_test.go`) are updated to pass `"events"`.
- Task 14's callback handler best-effort calls `ScrapeOne` synchronously; if SQS is down, the user still appears connected, but no message is published. The next `app scrape spotify` run picks them up.

---

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-05-19-plan-03-spotify-integration.md`. Two execution options:

1. **Subagent-Driven (recommended)** — I dispatch a fresh subagent per task, review between tasks, fast iteration.
2. **Inline Execution** — Execute tasks in this session using executing-plans, batch execution with checkpoints.

Which approach?
