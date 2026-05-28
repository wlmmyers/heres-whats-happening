# Stateful Spotify Settings Section Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the Spotify section of the Settings page reflect whether the user is currently connected — showing only the Connect button when not connected and only the Disconnect button when connected.

**Architecture:** Add a new authenticated `GET /integrations/spotify/status` endpoint that reports `{connected: bool}` based on whether a `user_spotify_tokens` row exists. The SPA fetches it with react-query on the Settings page and renders the appropriate button. The disconnect mutation invalidates the query so the UI swaps to the Connect button automatically.

**Tech Stack:** Go (chi, pgx, sqlc) backend, React + TypeScript + @tanstack/react-query + Vitest + Testing Library frontend.

---

## File Structure

**Backend**
- Modify `internal/http/handlers/spotify.go` — add `SpotifyStatus(q *store.Queries) http.HandlerFunc`.
- Modify `internal/http/handlers/spotify_test.go` — add two tests for the new endpoint.
- Modify `internal/http/server.go` — register the route.

**Frontend**
- Modify `web/src/api/spotify.ts` — export `getSpotifyStatus()`.
- Modify `web/src/pages/SettingsPage.tsx` — add the `useQuery`, conditional render, invalidate-on-disconnect.
- Modify `web/src/pages/SettingsPage.test.tsx` — mock `getSpotifyStatus`, default to `connected: false`, add the two new branch tests.

---

### Task 1: Backend handler — failing tests first

**Files:**
- Modify (Test): `internal/http/handlers/spotify_test.go`

- [ ] **Step 1: Append the two new tests at the bottom of `internal/http/handlers/spotify_test.go`**

These mirror the patterns used by `TestSpotifyDisconnect_RemovesTokensAndInterests`: they create a real user via `testdb`, optionally seed a token row, then call the handler directly through `middleware.RequireAuth` with a signed bearer.

Add to the bottom of `internal/http/handlers/spotify_test.go`:

```go
func TestSpotifyStatus_Connected(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	city, _ := q.GetDefaultCity(ctx)
	userRow, _ := q.CreateUser(ctx, store.CreateUserParams{
		Email: "spotify-status-connected@example.com", PasswordHash: "stub", CityID: city.ID,
	})

	// Seed a token row so the user is "connected".
	_ = q.UpsertUserSpotifyTokens(ctx, store.UpsertUserSpotifyTokensParams{
		UserID:          userRow.ID,
		AccessTokenEnc:  []byte{1, 2, 3},
		RefreshTokenEnc: []byte{4, 5, 6},
		ExpiresAt:       pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
		Scope:           "user-top-read",
	})

	access, _ := signer.SignAccess(uuid.UUID(userRow.ID.Bytes))
	h := middleware.RequireAuth(signer)(handlers.SpotifyStatus(q))

	req := httptest.NewRequest(http.MethodGet, "/integrations/spotify/status", nil)
	req.Header.Set("Authorization", "Bearer "+access)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var body struct {
		Connected bool `json:"connected"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.True(t, body.Connected)
}

func TestSpotifyStatus_NotConnected(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	city, _ := q.GetDefaultCity(ctx)
	userRow, _ := q.CreateUser(ctx, store.CreateUserParams{
		Email: "spotify-status-disconnected@example.com", PasswordHash: "stub", CityID: city.ID,
	})

	access, _ := signer.SignAccess(uuid.UUID(userRow.ID.Bytes))
	h := middleware.RequireAuth(signer)(handlers.SpotifyStatus(q))

	req := httptest.NewRequest(http.MethodGet, "/integrations/spotify/status", nil)
	req.Header.Set("Authorization", "Bearer "+access)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var body struct {
		Connected bool `json:"connected"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.False(t, body.Connected)
}
```

- [ ] **Step 2: Run the new tests and confirm they fail to compile**

Run: `go test ./internal/http/handlers/ -run TestSpotifyStatus -count=1`
Expected: a compilation error mentioning `handlers.SpotifyStatus` (undefined). This proves the test is wired correctly and the handler doesn't exist yet.

---

### Task 2: Backend handler — implementation

**Files:**
- Modify: `internal/http/handlers/spotify.go`
- Modify: `internal/http/server.go`

- [ ] **Step 1: Add the handler at the bottom of `internal/http/handlers/spotify.go`**

Add this function (and update imports — `errors` and `github.com/jackc/pgx/v5` will be new):

```go
// SpotifyStatus reports whether the authenticated user has Spotify
// connected, i.e. whether a row exists in user_spotify_tokens.
func SpotifyStatus(q *store.Queries) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid, ok := middleware.UserIDFromContext(r.Context())
		if !ok {
			httperr.Write(w, http.StatusUnauthorized, "no_user", "user not in context")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		_, err := q.GetUserSpotifyTokens(ctx, pgtype.UUID{Bytes: uid, Valid: true})
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			httperr.Write(w, http.StatusInternalServerError, "db_error", "could not load tokens")
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"connected": err == nil})
	}
}
```

Update the import block at the top of the file to add `"errors"` and `"github.com/jackc/pgx/v5"`. The full import block should be:

```go
import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wmyers/heres-whats-happening/internal/crypto"
	"github.com/wmyers/heres-whats-happening/internal/http/httperr"
	"github.com/wmyers/heres-whats-happening/internal/http/middleware"
	spotifyscrape "github.com/wmyers/heres-whats-happening/internal/scraper/spotify"
	"github.com/wmyers/heres-whats-happening/internal/spotify"
	"github.com/wmyers/heres-whats-happening/internal/store"
)
```

- [ ] **Step 2: Register the route in `internal/http/server.go`**

In the authenticated group (currently around line 77), add a new line *immediately after* the existing `r.Get("/integrations/spotify/connect", ...)` line:

```go
r.Get("/integrations/spotify/status", handlers.SpotifyStatus(s.Queries))
```

So the relevant block becomes:

```go
r.Get("/integrations/spotify/connect", handlers.SpotifyConnect(s.SpotifyClient, s.OAuthHMACKey))
r.Get("/integrations/spotify/status", handlers.SpotifyStatus(s.Queries))
r.Post("/integrations/spotify/exchange", handlers.SpotifyExchange(
    s.Queries, s.SpotifyClient, s.SpotifyCipher, s.OAuthHMACKey,
    s.QueuePublisher, s.InterestsQueueURL))
r.Delete("/integrations/spotify", handlers.SpotifyDisconnect(s.Queries))
```

- [ ] **Step 3: Run the new tests and confirm they pass**

Run: `go test ./internal/http/handlers/ -run TestSpotifyStatus -count=1 -v`
Expected: both `TestSpotifyStatus_Connected` and `TestSpotifyStatus_NotConnected` pass.

- [ ] **Step 4: Run the full handler test suite to catch regressions**

Run: `go test ./internal/http/handlers/ -count=1`
Expected: all tests pass.

- [ ] **Step 5: Commit**

```bash
git add internal/http/handlers/spotify.go internal/http/handlers/spotify_test.go internal/http/server.go
git commit -m "feat(spotify): add GET /integrations/spotify/status endpoint"
```

---

### Task 3: Frontend API client

**Files:**
- Modify: `web/src/api/spotify.ts`

- [ ] **Step 1: Add the new exported function in `web/src/api/spotify.ts`**

Append below the existing `disconnectSpotify` function:

```ts
export async function getSpotifyStatus(): Promise<{ connected: boolean }> {
  return apiFetch<{ connected: boolean }>('/integrations/spotify/status');
}
```

- [ ] **Step 2: Type-check the web project**

Run: `cd web && npx tsc --noEmit`
Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add web/src/api/spotify.ts
git commit -m "feat(web): add getSpotifyStatus API client"
```

---

### Task 4: Settings page test scaffolding — mock `getSpotifyStatus`

**Files:**
- Modify (Test): `web/src/pages/SettingsPage.test.tsx`

We add the mock and a default `beforeEach` value first, so the existing tests keep working when we change the component in Task 5. Existing tests run against `connected: false` (the previous behavior — Connect button visible).

- [ ] **Step 1: Update the `vi.mock('../api/spotify', ...)` block**

Change:

```ts
vi.mock('../api/spotify', () => ({
  startSpotifyConnect: vi.fn(),
  disconnectSpotify: vi.fn(),
}));
```

to:

```ts
vi.mock('../api/spotify', () => ({
  startSpotifyConnect: vi.fn(),
  disconnectSpotify: vi.fn(),
  getSpotifyStatus: vi.fn(),
}));
```

- [ ] **Step 2: In the `beforeEach` block, default `getSpotifyStatus` to "not connected"**

Change:

```ts
beforeEach(() => {
  vi.resetAllMocks();
  (interestsApi.listInterests as ReturnType<typeof vi.fn>).mockResolvedValue([]);
});
```

to:

```ts
beforeEach(() => {
  vi.resetAllMocks();
  (interestsApi.listInterests as ReturnType<typeof vi.fn>).mockResolvedValue([]);
  (spotifyApi.getSpotifyStatus as ReturnType<typeof vi.fn>).mockResolvedValue({ connected: false });
});
```

- [ ] **Step 3: Run the existing tests to make sure nothing regressed**

Run: `cd web && npx vitest run src/pages/SettingsPage.test.tsx`
Expected: all three existing tests still pass.

---

### Task 5: Settings page — add the two new failing tests

**Files:**
- Modify (Test): `web/src/pages/SettingsPage.test.tsx`

- [ ] **Step 1: Append the two new tests inside the existing `describe('SettingsPage', ...)` block**

Add these to the end of the `describe('SettingsPage', () => { ... })` block:

```ts
  it('shows only the Disconnect button when Spotify is connected', async () => {
    (spotifyApi.getSpotifyStatus as ReturnType<typeof vi.fn>).mockResolvedValue({ connected: true });
    renderPage();
    expect(await screen.findByRole('button', { name: /disconnect/i })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /connect spotify/i })).not.toBeInTheDocument();
  });

  it('shows only the Connect button when Spotify is not connected', async () => {
    (spotifyApi.getSpotifyStatus as ReturnType<typeof vi.fn>).mockResolvedValue({ connected: false });
    renderPage();
    expect(await screen.findByRole('button', { name: /connect spotify/i })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /disconnect/i })).not.toBeInTheDocument();
  });
```

- [ ] **Step 2: Run the tests and confirm the two new ones FAIL**

Run: `cd web && npx vitest run src/pages/SettingsPage.test.tsx`
Expected: the two new tests fail because the current `SettingsPage.tsx` always renders BOTH buttons. The existing three tests still pass.

---

### Task 6: Settings page — implementation

**Files:**
- Modify: `web/src/pages/SettingsPage.tsx`

- [ ] **Step 1: Import `getSpotifyStatus`**

In the imports at the top of `web/src/pages/SettingsPage.tsx`, change:

```ts
import { startSpotifyConnect, disconnectSpotify } from '../api/spotify';
```

to:

```ts
import { startSpotifyConnect, disconnectSpotify, getSpotifyStatus } from '../api/spotify';
```

- [ ] **Step 2: Add the status query and update the disconnect mutation to invalidate it**

Inside the `SettingsPage` component, replace:

```ts
  const connectSpotifyMut = useMutation({
    mutationFn: startSpotifyConnect,
    onSuccess: (authorizeURL) => {
      window.location.assign(authorizeURL);
    },
  });
  const disconnectSpotifyMut = useMutation({
    mutationFn: disconnectSpotify,
  });
```

with:

```ts
  const { data: spotifyStatus, isLoading: spotifyStatusLoading } = useQuery({
    queryKey: ['spotify-status'],
    queryFn: getSpotifyStatus,
  });
  const connectSpotifyMut = useMutation({
    mutationFn: startSpotifyConnect,
    onSuccess: (authorizeURL) => {
      window.location.assign(authorizeURL);
    },
  });
  const disconnectSpotifyMut = useMutation({
    mutationFn: disconnectSpotify,
    onSuccess: () => qc.invalidateQueries({ queryKey: ['spotify-status'] }),
  });
```

- [ ] **Step 3: Replace the Spotify section's button row with the conditional version**

Replace the existing `<div className="flex gap-2">…</div>` inside the Spotify section (the one containing both buttons) with:

```tsx
        {!spotifyStatusLoading && (
          <div className="flex gap-2 items-center">
            {spotifyStatus?.connected ? (
              <>
                <span className="text-sm text-gray-700">Connected.</span>
                <button
                  type="button"
                  onClick={() => disconnectSpotifyMut.mutate()}
                  disabled={disconnectSpotifyMut.isPending}
                  className="border rounded px-4 py-2 hover:bg-gray-50 disabled:opacity-60"
                >
                  Disconnect
                </button>
              </>
            ) : (
              <button
                type="button"
                onClick={() => connectSpotifyMut.mutate()}
                disabled={connectSpotifyMut.isPending}
                className="bg-green-600 hover:bg-green-700 disabled:opacity-60 text-white rounded px-4 py-2"
              >
                Connect Spotify
              </button>
            )}
          </div>
        )}
```

- [ ] **Step 4: Run the SettingsPage tests and confirm everything passes**

Run: `cd web && npx vitest run src/pages/SettingsPage.test.tsx`
Expected: all five tests pass (three existing + two new).

- [ ] **Step 5: Type-check the web project**

Run: `cd web && npx tsc --noEmit`
Expected: no errors.

- [ ] **Step 6: Run the full web test suite to check for regressions**

Run: `cd web && npx vitest run`
Expected: all tests pass.

- [ ] **Step 7: Commit**

```bash
git add web/src/pages/SettingsPage.tsx web/src/pages/SettingsPage.test.tsx
git commit -m "feat(web): show Connect/Disconnect based on Spotify status"
```

---

## Final verification

- [ ] **Step 1: Run all Go tests**

Run: `go test ./...`
Expected: all packages pass.

- [ ] **Step 2: Run all web tests**

Run: `cd web && npx vitest run`
Expected: all tests pass.

- [ ] **Step 3: Confirm git status is clean**

Run: `git status`
Expected: working tree clean (or only contains untracked files unrelated to this change).
