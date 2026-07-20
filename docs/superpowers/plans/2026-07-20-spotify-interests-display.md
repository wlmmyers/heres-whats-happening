# Read-only Spotify Interests Display Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Show users the Spotify-derived interests that drive their event matches, as a read-only section on the Interests page backed by a new `GET /me/spotify-interests` endpoint.

**Architecture:** One new sqlc query fetches all four Spotify-derived interest kinds in a single round trip. A new handler groups rows by kind, attaches human labels, omits empty groups, and emits them in a fixed server-owned order. The frontend renders one read-only chip list per group, collapsing groups longer than 20 items.

**Tech Stack:** Go 1.x, chi router, sqlc + pgx/v5, PostgreSQL; React + TypeScript, TanStack Query, vanilla-extract CSS, Vitest + Testing Library.

**Spec:** `docs/superpowers/specs/2026-07-20-spotify-interests-display-design.md`

## Global Constraints

- **Branch:** all work commits to `feature/ux-improvements`. Do not create new branches.
- **Read-only feature.** GET only. Never add POST/PATCH/DELETE for Spotify interests.
- **Never modify** `ListInterestsByUserAndKind` (singular) or `CreateManualInterest` — the Spotify ingest path and its tests depend on them.
- **The four Spotify kinds, exact strings, in this display order:** `spotify_top_artist`, `spotify_top_track_artist`, `spotify_saved_song_artist`, `spotify_top_genre`.
- **Exact group labels:** "Top artists", "Artists from your top tracks", "Artists from your saved songs", "Top genres".
- **Empty groups are omitted** from the API response — never emit a group with zero interests.
- **Collapse threshold:** 20 items per group.
- Backend tests need the test Postgres, same as existing handler tests. If `go test ./internal/http/handlers/` passes before you start, your environment is fine.
- `sqlc generate` regenerates `internal/store/models.go` as well as the `.sql.go` file. Commit both together.

---

### Task 1: Backend endpoint

**Files:**
- Modify: `sql/queries/user_interests.sql` (append)
- Regenerate: `internal/store/user_interests.sql.go`, `internal/store/models.go`
- Create: `internal/http/handlers/spotify_interests.go`
- Modify: `internal/http/server.go:76` (add route after the manual-interest routes)
- Test: `internal/http/handlers/spotify_interests_test.go`

**Interfaces:**
- Consumes: `interestOut` struct (already in `internal/http/handlers/manual_interests.go:23`), `writeJSON`, `httperr.Write`, `httperr.WriteErr`, `middleware.UserIDFromContext`.
- Produces: `handlers.SpotifyInterests(q *store.Queries) http.HandlerFunc`; JSON shape `{"groups":[{"kind":string,"label":string,"interests":[{id,value,normalized_value,weight,created_at}]}]}` consumed by Task 2.

- [ ] **Step 1: Add the sqlc query**

Append to `sql/queries/user_interests.sql`:

```sql
-- name: ListInterestsByUserAndKinds :many
SELECT id, kind, value, normalized_value, weight, created_at
FROM user_interests
WHERE user_id = $1 AND kind = ANY(sqlc.arg(kinds)::text[])
ORDER BY weight DESC, normalized_value ASC;
```

`sqlc.arg(kinds)` (rather than `$2`) is what makes the generated field `Kinds []string` instead of `Column2`. The rest of the plan depends on that name.

- [ ] **Step 2: Regenerate the store**

Run: `sqlc generate`
Then confirm the generated signature:

Run: `grep -n "ListInterestsByUserAndKinds" internal/store/user_interests.sql.go`
Expected: a `ListInterestsByUserAndKindsParams` struct with `UserID` and `Kinds []string`, a `ListInterestsByUserAndKindsRow`, and a `func (q *Queries) ListInterestsByUserAndKinds(...)`.

- [ ] **Step 3: Write the failing tests**

Create `internal/http/handlers/spotify_interests_test.go`:

```go
package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/auth"
	"github.com/wmyers/heres-whats-happening/internal/http/handlers"
	"github.com/wmyers/heres-whats-happening/internal/http/middleware"
	"github.com/wmyers/heres-whats-happening/internal/store"
	"github.com/wmyers/heres-whats-happening/internal/testdb"
)

// CreateManualInterest hardcodes kind='manual_tag', so there is no production
// query that can seed the Spotify kinds. Tests insert directly.
//
// userID is pgtype.UUID, not uuid.UUID: that is what store rows carry and what
// pgx binds directly, matching how ical_test.go and ingest tests pass user ids.
func insertInterest(t *testing.T, pool *pgxpool.Pool, userID pgtype.UUID, kind, value string, weight float64) {
	t.Helper()
	_, err := pool.Exec(context.Background(),
		`INSERT INTO user_interests (user_id, kind, value, normalized_value, weight)
		 VALUES ($1, $2, $3, $4, $5)`,
		userID, kind, value, strings.ToLower(value), weight)
	require.NoError(t, err)
}

func userIDByEmail(t *testing.T, q *store.Queries, email string) pgtype.UUID {
	t.Helper()
	row, err := q.GetUserByEmail(context.Background(), email)
	require.NoError(t, err)
	return row.ID
}

type spotifyGroupJSON struct {
	Kind      string `json:"kind"`
	Label     string `json:"label"`
	Interests []struct {
		ID     string  `json:"id"`
		Value  string  `json:"value"`
		Weight float64 `json:"weight"`
	} `json:"interests"`
}

func getSpotifyInterests(t *testing.T, q *store.Queries, signer *auth.JWTSigner, token string) (int, []spotifyGroupJSON) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/me/spotify-interests", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	middleware.RequireAuth(signer)(handlers.SpotifyInterests(q)).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		return rec.Code, nil
	}
	var body struct {
		Groups []spotifyGroupJSON `json:"groups"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
	return rec.Code, body.Groups
}

func TestGetSpotifyInterests_GroupsByKind(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	email := "spotify-groups@x"
	token := signupAndAccess(t, q, signer, "", email)
	uid := userIDByEmail(t, q, email)

	insertInterest(t, pool, uid, "spotify_top_genre", "Shoegaze", 0.4)
	insertInterest(t, pool, uid, "spotify_top_artist", "Phoebe Bridgers", 0.9)
	insertInterest(t, pool, uid, "spotify_saved_song_artist", "Big Thief", 0.6)
	insertInterest(t, pool, uid, "spotify_top_track_artist", "Alvvays", 0.8)

	code, groups := getSpotifyInterests(t, q, signer, token)
	require.Equal(t, http.StatusOK, code)
	require.Len(t, groups, 4)

	require.Equal(t, "spotify_top_artist", groups[0].Kind)
	require.Equal(t, "Top artists", groups[0].Label)
	require.Equal(t, "Phoebe Bridgers", groups[0].Interests[0].Value)

	require.Equal(t, "spotify_top_track_artist", groups[1].Kind)
	require.Equal(t, "Artists from your top tracks", groups[1].Label)

	require.Equal(t, "spotify_saved_song_artist", groups[2].Kind)
	require.Equal(t, "Artists from your saved songs", groups[2].Label)

	require.Equal(t, "spotify_top_genre", groups[3].Kind)
	require.Equal(t, "Top genres", groups[3].Label)
}

func TestGetSpotifyInterests_ExcludesManualTags(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	email := "spotify-excl@x"
	token := signupAndAccess(t, q, signer, "", email)
	uid := userIDByEmail(t, q, email)

	insertInterest(t, pool, uid, "manual_tag", "jazz", 1.0)
	insertInterest(t, pool, uid, "spotify_top_artist", "Alvvays", 0.8)

	_, groups := getSpotifyInterests(t, q, signer, token)
	require.Len(t, groups, 1)
	require.Equal(t, "spotify_top_artist", groups[0].Kind)
	for _, g := range groups {
		for _, i := range g.Interests {
			require.NotEqual(t, "jazz", i.Value)
		}
	}
}

func TestGetSpotifyInterests_OmitsEmptyGroups(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	email := "spotify-empty@x"
	token := signupAndAccess(t, q, signer, "", email)
	uid := userIDByEmail(t, q, email)

	insertInterest(t, pool, uid, "spotify_top_artist", "Alvvays", 0.8)

	_, groups := getSpotifyInterests(t, q, signer, token)
	require.Len(t, groups, 1)
}

func TestGetSpotifyInterests_NoDataReturnsEmptyGroups(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	token := signupAndAccess(t, q, signer, "", "spotify-none@x")

	code, groups := getSpotifyInterests(t, q, signer, token)
	require.Equal(t, http.StatusOK, code)
	require.Empty(t, groups)
}

func TestGetSpotifyInterests_ReturnsOnlyOwn(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)

	otherEmail := "spotify-other@x"
	signupAndAccess(t, q, signer, "", otherEmail)
	otherUID := userIDByEmail(t, q, otherEmail)
	insertInterest(t, pool, otherUID, "spotify_top_artist", "Someone Else", 0.9)

	mineToken := signupAndAccess(t, q, signer, "", "spotify-mine@x")
	_, groups := getSpotifyInterests(t, q, signer, mineToken)
	require.Empty(t, groups)
}

func TestGetSpotifyInterests_OrdersByWeightDesc(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	email := "spotify-order@x"
	token := signupAndAccess(t, q, signer, "", email)
	uid := userIDByEmail(t, q, email)

	insertInterest(t, pool, uid, "spotify_top_artist", "Low", 0.1)
	insertInterest(t, pool, uid, "spotify_top_artist", "High", 0.9)
	insertInterest(t, pool, uid, "spotify_top_artist", "Mid", 0.5)

	_, groups := getSpotifyInterests(t, q, signer, token)
	require.Len(t, groups, 1)
	require.Equal(t, []string{"High", "Mid", "Low"},
		[]string{groups[0].Interests[0].Value, groups[0].Interests[1].Value, groups[0].Interests[2].Value})
}

func TestGetSpotifyInterests_Unauthenticated(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)

	req := httptest.NewRequest(http.MethodGet, "/me/spotify-interests", nil)
	rec := httptest.NewRecorder()
	middleware.RequireAuth(signer)(handlers.SpotifyInterests(q)).ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}
```

Note: `signupAndAccess` already exists in `internal/http/handlers/manual_interests_test.go` in the same `handlers_test` package — do not redefine it. Its third argument is `cityID`; existing tests pass a real city ID where it matters. If `""` fails signup validation, copy whatever the neighbouring tests in `manual_interests_test.go` pass and use that instead.

- [ ] **Step 4: Run tests to verify they fail**

Run: `go test ./internal/http/handlers/ -run TestGetSpotifyInterests -v`
Expected: FAIL — compile error, `undefined: handlers.SpotifyInterests`.

- [ ] **Step 5: Write the handler**

Create `internal/http/handlers/spotify_interests.go`:

```go
package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wmyers/heres-whats-happening/internal/http/httperr"
	"github.com/wmyers/heres-whats-happening/internal/http/middleware"
	"github.com/wmyers/heres-whats-happening/internal/store"
)

// spotifyKinds is the set of Spotify-derived interest kinds. The order here is
// the display order of the response groups. Adding a fifth Spotify kind is a
// change to this slice plus spotifyKindLabels — the frontend renders whatever
// groups it is handed.
var spotifyKinds = []string{
	"spotify_top_artist",
	"spotify_top_track_artist",
	"spotify_saved_song_artist",
	"spotify_top_genre",
}

var spotifyKindLabels = map[string]string{
	"spotify_top_artist":        "Top artists",
	"spotify_top_track_artist":  "Artists from your top tracks",
	"spotify_saved_song_artist": "Artists from your saved songs",
	"spotify_top_genre":         "Top genres",
}

type spotifyInterestGroup struct {
	Kind      string        `json:"kind"`
	Label     string        `json:"label"`
	Interests []interestOut `json:"interests"`
}

type spotifyInterestsResponse struct {
	Groups []spotifyInterestGroup `json:"groups"`
}

// SpotifyInterests returns the caller's Spotify-derived interests grouped by
// kind. Read-only: these rows are owned by the Spotify scraper, so there is no
// create or delete counterpart. Groups with no rows are omitted entirely.
func SpotifyInterests(q *store.Queries) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid, ok := middleware.UserIDFromContext(r.Context())
		if !ok {
			httperr.Write(w, http.StatusUnauthorized, "no_user", "user not in context")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		rows, err := q.ListInterestsByUserAndKinds(ctx, store.ListInterestsByUserAndKindsParams{
			UserID: pgtype.UUID{Bytes: uid, Valid: true},
			Kinds:  spotifyKinds,
		})
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, "db_error", "could not list spotify interests", err)
			return
		}

		byKind := make(map[string][]interestOut, len(spotifyKinds))
		for _, row := range rows {
			byKind[row.Kind] = append(byKind[row.Kind], interestOut{
				ID:              uuid.UUID(row.ID.Bytes).String(),
				Value:           row.Value,
				NormalizedValue: row.NormalizedValue,
				Weight:          row.Weight,
				CreatedAt:       row.CreatedAt.Time.UTC().Format(time.RFC3339),
			})
		}

		groups := make([]spotifyInterestGroup, 0, len(spotifyKinds))
		for _, kind := range spotifyKinds {
			items := byKind[kind]
			if len(items) == 0 {
				continue
			}
			groups = append(groups, spotifyInterestGroup{
				Kind:      kind,
				Label:     spotifyKindLabels[kind],
				Interests: items,
			})
		}
		writeJSON(w, http.StatusOK, spotifyInterestsResponse{Groups: groups})
	}
}
```

`groups` is built with `make(..., 0, ...)` so an empty result serializes as `[]`, not `null` — Task 3's frontend relies on that.

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test ./internal/http/handlers/ -run TestGetSpotifyInterests -v`
Expected: PASS, all 7 tests.

If `row.Weight` or `row.CreatedAt` cause a type error, check the generated `ListInterestsByUserAndKindsRow` and mirror exactly what `ListManualInterests` does in `manual_interests.go:51-57`.

- [ ] **Step 7: Register the route**

In `internal/http/server.go`, immediately after the `r.Delete("/me/manual-interests/{id}", ...)` line:

```go
		r.Get("/me/spotify-interests", handlers.SpotifyInterests(s.Queries))
```

- [ ] **Step 8: Verify the whole backend**

Run: `go build ./... && go test ./internal/http/...`
Expected: all packages PASS.

- [ ] **Step 9: Commit**

```bash
git add sql/queries/user_interests.sql internal/store/ internal/http/handlers/spotify_interests.go internal/http/handlers/spotify_interests_test.go internal/http/server.go
git commit -m "Add GET /me/spotify-interests returning interests grouped by kind"
```

---

### Task 2: Frontend API client and read-only chip list

**Files:**
- Create: `web/src/api/spotifyInterests.ts`
- Create: `web/src/components/TagList.tsx`
- Create: `web/src/components/TagList.css.ts`
- Test: `web/src/components/TagList.test.tsx`

**Interfaces:**
- Consumes: `apiFetch` from `web/src/api/client.ts`; `Interest` type from `web/src/api/manualInterests.ts`; the JSON shape from Task 1.
- Produces: `listSpotifyInterests(): Promise<SpotifyInterestGroup[]>`, the `SpotifyInterestGroup` type (`{kind: string; label: string; interests: Interest[]}`), and `<TagList values={string[]} />` — all used by Task 3.

- [ ] **Step 1: Write the failing component test**

Create `web/src/components/TagList.test.tsx`:

```tsx
import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import TagList from './TagList';

describe('TagList', () => {
  it('renders each value as a chip', () => {
    render(<TagList values={['jazz', 'shoegaze']} />);
    expect(screen.getByText('jazz')).toBeInTheDocument();
    expect(screen.getByText('shoegaze')).toBeInTheDocument();
  });

  it('is read-only — renders no remove buttons', () => {
    render(<TagList values={['jazz']} />);
    expect(screen.queryByRole('button')).not.toBeInTheDocument();
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && npx vitest run src/components/TagList.test.tsx`
Expected: FAIL — cannot resolve `./TagList`.

- [ ] **Step 3: Write the styles**

Create `web/src/components/TagList.css.ts`:

```ts
import { style } from '@vanilla-extract/css';
import { color, radius, fontSize } from '../styles/theme';

export const wrapper = style({
  display: 'flex',
  flexWrap: 'wrap',
  gap: '0.5rem',
  alignItems: 'center',
});

export const tag = style({
  display: 'inline-flex',
  alignItems: 'center',
  backgroundColor: color.blue100,
  color: color.blue800,
  borderRadius: radius.full,
  paddingInline: '0.75rem',
  paddingBlock: '0.25rem',
  ...fontSize.sm,
});
```

The chip rule duplicates `TagInput.css.ts`'s `tag`. That is deliberate: `TagInput` is an editing control with an input and per-chip remove buttons, and giving it a `readOnly` mode would make one component serve two purposes. `wrapper` here has no border, since this is a display list rather than an input affordance.

- [ ] **Step 4: Write the component**

Create `web/src/components/TagList.tsx`:

```tsx
import * as s from './TagList.css';

interface Props {
  values: string[];
}

// Read-only counterpart to TagInput: renders chips with no add input and no
// remove affordance. Used for interests the user cannot edit.
export default function TagList({ values }: Props) {
  return (
    <div className={s.wrapper}>
      {values.map((v) => (
        <span key={v} className={s.tag}>
          {v}
        </span>
      ))}
    </div>
  );
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `cd web && npx vitest run src/components/TagList.test.tsx`
Expected: PASS, 2 tests.

- [ ] **Step 6: Write the API client**

Create `web/src/api/spotifyInterests.ts`:

```ts
import { apiFetch } from './client';
import type { Interest } from './manualInterests';

// A group of Spotify-derived interests of one kind. The server owns the kind
// ordering and the human label, so a new kind needs no frontend change.
export interface SpotifyInterestGroup {
  kind: string;
  label: string;
  interests: Interest[];
}

// Read-only. These interests come from the Spotify scraper; there is no create
// or delete counterpart. Groups with no interests are omitted by the server.
export async function listSpotifyInterests(): Promise<SpotifyInterestGroup[]> {
  const out = await apiFetch<{ groups: SpotifyInterestGroup[] }>('/me/spotify-interests');
  return out.groups;
}
```

- [ ] **Step 7: Typecheck**

Run: `cd web && npx tsc --noEmit`
Expected: no output (clean).

If `Interest` is not exported from `manualInterests.ts`, add `export` to its interface declaration there.

- [ ] **Step 8: Commit**

```bash
git add web/src/api/spotifyInterests.ts web/src/components/TagList.tsx web/src/components/TagList.css.ts web/src/components/TagList.test.tsx
git commit -m "Add spotifyInterests API client and read-only TagList component"
```

---

### Task 3: Interests page section

**Files:**
- Modify: `web/src/pages/InterestsPage.tsx`
- Modify: `web/src/pages/InterestsPage.css.ts`
- Test: `web/src/pages/InterestsPage.test.tsx` (extend existing)

**Interfaces:**
- Consumes: `listSpotifyInterests`, `SpotifyInterestGroup`, `TagList` from Task 2; `getSpotifyStatus` from `web/src/api/spotify.ts` (returns `Promise<{connected: boolean}>`).
- Produces: final user-facing feature. Nothing depends on it.

- [ ] **Step 1: Write the failing tests**

In `web/src/pages/InterestsPage.test.tsx`, add two mocks next to the existing `vi.mock('../api/manualInterests', ...)` block:

```tsx
vi.mock('../api/spotifyInterests', () => ({
  listSpotifyInterests: vi.fn(),
}));
vi.mock('../api/spotify', () => ({
  getSpotifyStatus: vi.fn(),
}));

import * as spotifyInterestsApi from '../api/spotifyInterests';
import * as spotifyApi from '../api/spotify';
```

Extend the existing `beforeEach` so the new mocks have defaults — without this the existing two tests will break:

```tsx
beforeEach(() => {
  vi.resetAllMocks();
  (interestsApi.listManualInterests as ReturnType<typeof vi.fn>).mockResolvedValue([]);
  (spotifyInterestsApi.listSpotifyInterests as ReturnType<typeof vi.fn>).mockResolvedValue([]);
  (spotifyApi.getSpotifyStatus as ReturnType<typeof vi.fn>).mockResolvedValue({ connected: false });
});
```

Add this helper above the `describe` block:

```tsx
function spotifyInterest(value: string) {
  return { id: value, value, normalized_value: value, weight: 1, created_at: '' };
}
```

Then add a new `describe` block at the end of the file:

```tsx
describe('InterestsPage — Spotify section', () => {
  it('renders a group per kind with its label and chips', async () => {
    (spotifyInterestsApi.listSpotifyInterests as ReturnType<typeof vi.fn>).mockResolvedValue([
      { kind: 'spotify_top_artist', label: 'Top artists', interests: [spotifyInterest('Alvvays')] },
      { kind: 'spotify_top_genre', label: 'Top genres', interests: [spotifyInterest('Shoegaze')] },
    ]);
    renderPage();

    await waitFor(() => expect(screen.getByText('Top artists')).toBeInTheDocument());
    expect(screen.getByText('Alvvays')).toBeInTheDocument();
    expect(screen.getByText('Top genres')).toBeInTheDocument();
    expect(screen.getByText('Shoegaze')).toBeInTheDocument();
  });

  it('renders Spotify chips read-only', async () => {
    (spotifyInterestsApi.listSpotifyInterests as ReturnType<typeof vi.fn>).mockResolvedValue([
      { kind: 'spotify_top_artist', label: 'Top artists', interests: [spotifyInterest('Alvvays')] },
    ]);
    renderPage();

    await waitFor(() => expect(screen.getByText('Alvvays')).toBeInTheDocument());
    expect(screen.queryByLabelText('Remove Alvvays')).not.toBeInTheDocument();
  });

  it('collapses groups longer than 20 and expands on click', async () => {
    const many = Array.from({ length: 25 }, (_, i) => spotifyInterest(`artist-${i}`));
    (spotifyInterestsApi.listSpotifyInterests as ReturnType<typeof vi.fn>).mockResolvedValue([
      { kind: 'spotify_saved_song_artist', label: 'Artists from your saved songs', interests: many },
    ]);
    renderPage();

    await waitFor(() => expect(screen.getByText('artist-0')).toBeInTheDocument());
    expect(screen.queryByText('artist-24')).not.toBeInTheDocument();

    await userEvent.click(screen.getByRole('button', { name: /show all \(25\)/i }));
    expect(screen.getByText('artist-24')).toBeInTheDocument();

    await userEvent.click(screen.getByRole('button', { name: /show less/i }));
    expect(screen.queryByText('artist-24')).not.toBeInTheDocument();
  });

  it('hides the section entirely when there are no groups and Spotify is not connected', async () => {
    (spotifyInterestsApi.listSpotifyInterests as ReturnType<typeof vi.fn>).mockResolvedValue([]);
    (spotifyApi.getSpotifyStatus as ReturnType<typeof vi.fn>).mockResolvedValue({ connected: false });
    renderPage();

    await waitFor(() => expect(screen.getByRole('button', { name: /continue/i })).toBeEnabled());
    expect(screen.queryByText(/from your spotify/i)).not.toBeInTheDocument();
  });

  it('shows a pending message when connected but nothing scraped yet', async () => {
    (spotifyInterestsApi.listSpotifyInterests as ReturnType<typeof vi.fn>).mockResolvedValue([]);
    (spotifyApi.getSpotifyStatus as ReturnType<typeof vi.fn>).mockResolvedValue({ connected: true });
    renderPage();

    await waitFor(() =>
      expect(screen.getByText(/haven't pulled your listening history yet/i)).toBeInTheDocument(),
    );
  });
});
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd web && npx vitest run src/pages/InterestsPage.test.tsx`
Expected: FAIL — the 5 new tests fail (no "Top artists" text, no Spotify section). The 2 pre-existing tests should still PASS.

- [ ] **Step 3: Add the styles**

Append to `web/src/pages/InterestsPage.css.ts`:

```ts
export const groupHeading = style({
  ...fontSize.sm,
  color: color.gray600,
  marginTop: '1rem',
  marginBottom: '0.5rem',
});

export const sectionHeading = style({
  ...fontSize.sm,
  fontWeight: 600,
});

export const showAllButton = style({
  marginTop: '0.5rem',
  color: color.blue600,
  textDecorationLine: 'underline',
  ...fontSize.sm,
});

export const emptyNote = style({
  color: color.gray600,
  ...fontSize.sm,
});
```

Update that file's import line to pull in what these rules use:

```ts
import { color, fontSize } from '../styles/theme';
```

If `fontSize` or `color.gray600` is not exported from `web/src/styles/theme.ts`, check what `TagInput.css.ts` and the existing `lead` rule use and match those.

- [ ] **Step 4: Rewrite the page**

Replace the contents of `web/src/pages/InterestsPage.tsx`:

```tsx
import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useNavigate } from 'react-router-dom';
import TagInput from '../components/TagInput';
import TagList from '../components/TagList';
import {
  createManualInterest,
  deleteManualInterest,
  listManualInterests,
  type Interest,
} from '../api/manualInterests';
import { listSpotifyInterests, type SpotifyInterestGroup } from '../api/spotifyInterests';
import { getSpotifyStatus } from '../api/spotify';
import * as s from './InterestsPage.css';
import * as c from '../styles/common.css';

const COLLAPSE_AT = 20;

export default function InterestsPage() {
  const navigate = useNavigate();
  const qc = useQueryClient();
  const [expanded, setExpanded] = useState<Set<string>>(new Set());

  const { data: interests = [] } = useQuery<Interest[]>({
    queryKey: ['interests'],
    queryFn: listManualInterests,
  });

  // Loaded independently of status: if groups arrive first we render them
  // immediately rather than blocking on the status request.
  const { data: spotifyGroups = [] } = useQuery<SpotifyInterestGroup[]>({
    queryKey: ['spotifyInterests'],
    queryFn: listSpotifyInterests,
  });
  const { data: spotifyStatus } = useQuery({
    queryKey: ['spotifyStatus'],
    queryFn: getSpotifyStatus,
  });

  const addMut = useMutation({
    mutationFn: (value: string) => createManualInterest(value),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['interests'] }),
  });
  const removeMut = useMutation({
    mutationFn: (value: string) => {
      const target = interests.find((i) => i.value === value);
      if (!target) return Promise.resolve();
      return deleteManualInterest(target.id);
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: ['interests'] }),
  });

  const values = interests.map((i) => i.value);

  function toggle(kind: string) {
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(kind)) next.delete(kind);
      else next.add(kind);
      return next;
    });
  }

  // This page doubles as the signup onboarding step, so a brand-new user with
  // no Spotify connection must not see a dead-end "go connect Spotify" prompt
  // mid-signup. Show the section only when it has something to say.
  const showSpotify = spotifyGroups.length > 0 || spotifyStatus?.connected === true;

  return (
    <div>
      <header>
        <h1 className={c.pageTitle}>Tell us what you're into</h1>
        <p className={s.lead}>Add tags — genres, activities, anything.</p>
      </header>

      <section className={s.section}>
        <TagInput
          values={values}
          onAdd={(v) => addMut.mutate(v)}
          onRemove={(v) => removeMut.mutate(v)}
          placeholder="Add an interest and press Enter"
        />
        {addMut.isError && <div className={s.error}>Couldn't save that tag.</div>}
      </section>

      {showSpotify && (
        <section className={s.section}>
          <h2 className={s.sectionHeading}>From your Spotify</h2>
          {spotifyGroups.length === 0 ? (
            <p className={s.emptyNote}>
              We haven't pulled your listening history yet. Check back soon.
            </p>
          ) : (
            spotifyGroups.map((group) => {
              const isExpanded = expanded.has(group.kind);
              const shown = isExpanded
                ? group.interests
                : group.interests.slice(0, COLLAPSE_AT);
              return (
                <div key={group.kind}>
                  <h3 className={s.groupHeading}>{group.label}</h3>
                  <TagList values={shown.map((i) => i.value)} />
                  {group.interests.length > COLLAPSE_AT && (
                    <button
                      type="button"
                      className={s.showAllButton}
                      onClick={() => toggle(group.kind)}
                    >
                      {isExpanded ? 'Show less' : `Show all (${group.interests.length})`}
                    </button>
                  )}
                </div>
              );
            })
          )}
        </section>
      )}

      <button type="button" onClick={() => navigate('/calendar')} className={s.continueButton}>
        Continue
      </button>
    </div>
  );
}
```

Note the removed `Link` import — the original file imported `Link` from `react-router-dom` alongside `useNavigate` but never used it in the rendered output. The rewrite drops it. If your linter flags an unused `inlineLink` style in `InterestsPage.css.ts`, leave it; it is unrelated to this task.

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd web && npx vitest run src/pages/InterestsPage.test.tsx`
Expected: PASS, all 7 tests (2 pre-existing + 5 new).

- [ ] **Step 6: Typecheck and run the full frontend suite**

Run: `cd web && npx tsc --noEmit && npx vitest run`
Expected: typecheck clean. Suite passes **except** `src/pages/SignupPage.test.tsx > signs up and redirects to onboarding`, which is a known pre-existing failure unrelated to this work. Do not attempt to fix it as part of this task. If any *other* test fails, that is a real regression — fix it before committing.

- [ ] **Step 7: Commit**

```bash
git add web/src/pages/InterestsPage.tsx web/src/pages/InterestsPage.css.ts web/src/pages/InterestsPage.test.tsx
git commit -m "Show read-only Spotify interests on the Interests page"
```

---

## Verification

After all three tasks:

- [ ] `go build ./... && go test ./...` — backend green
- [ ] `cd web && npx tsc --noEmit && npx vitest run` — frontend green except the known `SignupPage` failure
- [ ] `git log --oneline -4` on `feature/ux-improvements` shows the three feature commits on top of the spec commit
