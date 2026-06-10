# On-demand User Embedding Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Embed a user's `interest_embedding` promptly after they connect Spotify or change manual interests, instead of waiting for the once-daily `match` batch.

**Architecture:** Both interest-change paths converge on the interest consumer doing a targeted single-user embed. The Spotify scraper tags its snapshot message with `Op: "replace_interests_and_embed"`; the `InterestHandler` replaces rows then embeds. The manual-interest HTTP handlers write their row, then publish an `Op: "only_embed"` message to the same interests queue; the consumer embeds without touching rows. Only the consumer process gets a TEI client; the API server stays TEI-free.

**Tech Stack:** Go, pgx/pgvector, AWS SQS, sqlc-generated `store` queries, testify, a Postgres-backed `testdb` harness.

**Spec:** `docs/superpowers/specs/2026-06-09-on-demand-user-embedding-design.md`

---

## File Structure

- `internal/events/interest.go` — add `Op` field + the two op constants to `InterestMessage`.
- `internal/matcher/user_embedder.go` — extract a shared `embedUsers` helper; add `EmbedUser` for a single user.
- `internal/scraper/spotify/adapter.go` — set `Op: OpReplaceInterestsAndEmbed` on the snapshot message.
- `internal/ingest/interests.go` — give `InterestHandler` a `matcher.Embedder`; branch `Handle` on `Op`; embed after replace.
- `internal/http/handlers/interests.go` — `CreateInterest`/`DeleteInterest` gain `(pub, queueURL)` and publish an `only_embed` message.
- `internal/http/server.go` — pass `QueuePublisher` + `InterestsQueueURL` into the two handlers.
- `cmd/app/main.go` — build a TEI client in `serve()` and pass it to `NewInterestHandler`.
- Tests: `internal/matcher/user_embedder_test.go`, `internal/ingest/interests_test.go`, `internal/http/handlers/interests_test.go`.

---

## Task 1: Add `Op` discriminator to `InterestMessage`

**Files:**
- Modify: `internal/events/interest.go`

- [ ] **Step 1: Add the `Op` field and constants**

In `internal/events/interest.go`, add the field to the struct (after `UserID`):

```go
type InterestMessage struct {
	UserID string `json:"user_id"`
	// Op selects how the consumer processes this message. Empty is treated as
	// OpReplaceInterestsAndEmbed for backward compatibility with messages in
	// flight at deploy time.
	Op                     string           `json:"op,omitempty"`
	SpotifyTopArtists      []SpotifyTopItem `json:"spotify_top_artists,omitempty"`
	SpotifyTopTrackArtists []SpotifyTopItem `json:"spotify_top_track_artists,omitempty"`
	// SpotifySavedSongArtists are the artists behind the user's saved tracks
	// ("/me/tracks"), ranked by how recently each was saved (added_at), deduped
	// by name, and capped at 200.
	SpotifySavedSongArtists []SpotifyTopItem `json:"spotify_saved_song_artists,omitempty"`
	SpotifyTopGenres        []SpotifyTopItem `json:"spotify_top_genres,omitempty"`
	FetchedAt               time.Time        `json:"fetched_at"`
}
```

Add the constants below the struct (above `SpotifyTopItem`):

```go
// InterestMessage.Op values.
const (
	// OpReplaceInterestsAndEmbed replaces the user's Spotify-derived interest
	// rows from this message, then re-embeds the user. Published by the Spotify
	// scraper. Empty Op is treated identically.
	OpReplaceInterestsAndEmbed = "replace_interests_and_embed"
	// OpOnlyEmbed skips all row replacement and only re-embeds the user.
	// Published by the manual-interest API handlers, which write rows themselves.
	OpOnlyEmbed = "only_embed"
)
```

- [ ] **Step 2: Build the package**

Run: `go build ./internal/events/`
Expected: builds cleanly (no test for a plain struct field).

- [ ] **Step 3: Commit**

```bash
git add internal/events/interest.go
git commit -m "Add Op discriminator to InterestMessage"
```

---

## Task 2: Add `EmbedUser` to `UserEmbedder`

**Files:**
- Modify: `internal/matcher/user_embedder.go`
- Test: `internal/matcher/user_embedder_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/matcher/user_embedder_test.go`:

```go
func TestEmbedUser_EmbedsSingleUserWithSpotifyOnly(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()

	city, _ := q.GetDefaultCity(ctx)
	userRow, err := q.CreateUser(ctx, store.CreateUserParams{
		Email:        "embed-one@example.com",
		PasswordHash: "stub",
		CityID:       city.ID,
	})
	require.NoError(t, err)
	// Spotify interest only — no manual tag. Must still embed.
	require.NoError(t, q.InsertSpotifyInterest(ctx, store.InsertSpotifyInterestParams{
		UserID:          userRow.ID,
		Kind:            "spotify_top_artist",
		Value:           "Phoebe Bridgers",
		NormalizedValue: "phoebe bridgers",
		Weight:          1.0,
	}))

	fakeVec := make([]float32, 384)
	for i := range fakeVec {
		fakeVec[i] = 0.2
	}
	emb := &fakeEmbedder{vec: fakeVec}
	step := matcher.NewUserEmbedder(q, emb)

	require.NoError(t, step.EmbedUser(ctx, userRow.ID))
	require.Len(t, emb.calls, 1)
	require.Contains(t, emb.calls[0][0], "Phoebe Bridgers")
}

func TestEmbedUser_SkipsUserWithNoInterests(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()

	city, _ := q.GetDefaultCity(ctx)
	userRow, err := q.CreateUser(ctx, store.CreateUserParams{
		Email:        "embed-none@example.com",
		PasswordHash: "stub",
		CityID:       city.ID,
	})
	require.NoError(t, err)

	emb := &fakeEmbedder{vec: make([]float32, 384)}
	step := matcher.NewUserEmbedder(q, emb)

	require.NoError(t, step.EmbedUser(ctx, userRow.ID))
	require.Len(t, emb.calls, 0) // empty text → no embed call
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/matcher/ -run TestEmbedUser -v`
Expected: FAIL — `step.EmbedUser undefined`.

- [ ] **Step 3: Refactor `Run` and add `EmbedUser`**

In `internal/matcher/user_embedder.go`, replace the `Run` method (lines 24–114) with `Run` + `EmbedUser` + a shared `embedUsers` helper. The body of `embedUsers` is the existing logic verbatim from the `if len(userIDs) == 0` guard onward:

```go
func (u *UserEmbedder) Run(ctx context.Context) error {
	userIDs, err := u.q.SelectUsersNeedingEmbedding(ctx)
	if err != nil {
		return fmt.Errorf("select users: %w", err)
	}
	return u.embedUsers(ctx, userIDs)
}

// EmbedUser embeds a single user immediately, regardless of staleness — the
// caller (the interest consumer) already knows the user's interests changed. A
// user with no interest text (no interests of any kind) is skipped, leaving any
// existing embedding unchanged.
func (u *UserEmbedder) EmbedUser(ctx context.Context, userID pgtype.UUID) error {
	return u.embedUsers(ctx, []pgtype.UUID{userID})
}

func (u *UserEmbedder) embedUsers(ctx context.Context, userIDs []pgtype.UUID) error {
	if len(userIDs) == 0 {
		return nil
	}

	interests, err := u.q.ListUserInterestsBatch(ctx, userIDs)
	if err != nil {
		return fmt.Errorf("list interests: %w", err)
	}

	type bucket struct {
		artists      []string
		trackArtists []string
		savedArtists []string
		genres       []string
		tags         []string
	}
	byUser := make(map[pgtype.UUID]*bucket, len(userIDs))
	for _, id := range userIDs {
		byUser[id] = &bucket{}
	}
	for _, ui := range interests {
		b := byUser[ui.UserID]
		switch ui.Kind {
		case "spotify_top_artist":
			b.artists = append(b.artists, ui.Value)
		case "spotify_top_track_artist":
			b.trackArtists = append(b.trackArtists, ui.Value)
		case "spotify_saved_song_artist":
			b.savedArtists = append(b.savedArtists, ui.Value)
		case "spotify_top_genre":
			b.genres = append(b.genres, ui.Value)
		case "manual_tag":
			b.tags = append(b.tags, ui.Value)
		}
	}

	texts := make([]string, len(userIDs))
	for i, id := range userIDs {
		b := byUser[id]
		artists := foldDeduped(b.artists, b.trackArtists, events.NormalizeString)
		artists = foldDeduped(artists, b.savedArtists, events.NormalizeString)
		texts[i] = BuildUserText(UserText{
			TopArtists: artists,
			TopGenres:  b.genres,
			ManualTags: b.tags,
		})
	}

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

Note: the large doc comment that previously sat inside `Run` (lines 80–83 about empty-text users) now lives in `embedUsers` — keep it if you like; it is not reproduced here for brevity but the logic is unchanged.

- [ ] **Step 4: Run the new tests and the existing ones**

Run: `go test ./internal/matcher/ -run 'TestEmbedUser|TestEmbedUsers' -v`
Expected: PASS — both new tests and the three pre-existing `TestEmbedUsers_*` tests pass (the refactor preserves `Run` behavior).

- [ ] **Step 5: Commit**

```bash
git add internal/matcher/user_embedder.go internal/matcher/user_embedder_test.go
git commit -m "Add UserEmbedder.EmbedUser for single-user embedding"
```

---

## Task 3: Tag the Spotify snapshot message with its Op

**Files:**
- Modify: `internal/scraper/spotify/adapter.go`

- [ ] **Step 1: Set `Op` on the message literal**

In `internal/scraper/spotify/adapter.go`, find the `msg := events.InterestMessage{` literal (around line 108) and add the `Op` field:

```go
	msg := events.InterestMessage{
		UserID:    userIDString(userID),
		Op:        events.OpReplaceInterestsAndEmbed,
		FetchedAt: time.Now().UTC(),
	}
```

- [ ] **Step 2: Build**

Run: `go build ./internal/scraper/...`
Expected: builds cleanly.

- [ ] **Step 3: Commit**

```bash
git add internal/scraper/spotify/adapter.go
git commit -m "Tag Spotify snapshot message with replace_interests_and_embed op"
```

---

## Task 4: Embed in the consumer; branch `Handle` on Op

**Files:**
- Modify: `internal/ingest/interests.go`
- Test: `internal/ingest/interests_test.go`

- [ ] **Step 1: Write the failing tests**

In `internal/ingest/interests_test.go`, add a local fake embedder and two tests. Put the fake type near the top of the file (after the imports):

```go
type fakeEmbedder struct {
	calls [][]string
	vec   []float32
}

func (f *fakeEmbedder) Embed(_ context.Context, inputs []string) ([][]float32, error) {
	f.calls = append(f.calls, inputs)
	out := make([][]float32, len(inputs))
	for i := range out {
		out[i] = f.vec
	}
	return out, nil
}

func newFakeEmbedder() *fakeEmbedder {
	v := make([]float32, 384)
	for i := range v {
		v[i] = 0.1
	}
	return &fakeEmbedder{vec: v}
}
```

Then add the tests:

```go
func TestInterestHandler_OnlyEmbed_DoesNotTouchRows(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()

	city, err := q.GetDefaultCity(ctx)
	require.NoError(t, err)
	userRow, err := q.CreateUser(ctx, store.CreateUserParams{
		Email:        "only-embed@example.com",
		PasswordHash: "stub",
		CityID:       city.ID,
	})
	require.NoError(t, err)
	// A manual tag the handler must NOT delete.
	_, err = q.CreateManualInterest(ctx, store.CreateManualInterestParams{
		UserID:          userRow.ID,
		Value:           "Indie Rock",
		NormalizedValue: "indie rock",
	})
	require.NoError(t, err)

	emb := newFakeEmbedder()
	body, _ := json.Marshal(&events.InterestMessage{
		UserID: pgtypeUUIDToString(t, userRow.ID),
		Op:     events.OpOnlyEmbed,
	})
	h := ingest.NewInterestHandler(q, emb)
	require.NoError(t, h.Handle(ctx, body))

	// Embedded once.
	require.Len(t, emb.calls, 1)
	require.Contains(t, emb.calls[0][0], "Indie Rock")
	// Manual tag still present.
	tags, err := q.ListInterestsByUserAndKind(ctx, store.ListInterestsByUserAndKindParams{
		UserID: userRow.ID,
		Kind:   "manual_tag",
	})
	require.NoError(t, err)
	require.Len(t, tags, 1)
}

func TestInterestHandler_Replace_AlsoEmbeds(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()

	city, err := q.GetDefaultCity(ctx)
	require.NoError(t, err)
	userRow, err := q.CreateUser(ctx, store.CreateUserParams{
		Email:        "replace-embed@example.com",
		PasswordHash: "stub",
		CityID:       city.ID,
	})
	require.NoError(t, err)

	emb := newFakeEmbedder()
	body, _ := json.Marshal(&events.InterestMessage{
		UserID: pgtypeUUIDToString(t, userRow.ID),
		Op:     events.OpReplaceInterestsAndEmbed,
		SpotifyTopArtists: []events.SpotifyTopItem{
			{Name: "Phoebe Bridgers", Rank: 1},
		},
	})
	h := ingest.NewInterestHandler(q, emb)
	require.NoError(t, h.Handle(ctx, body))

	rows, err := q.ListInterestsByUserAndKind(ctx, store.ListInterestsByUserAndKindParams{
		UserID: userRow.ID,
		Kind:   "spotify_top_artist",
	})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Len(t, emb.calls, 1)
	require.Contains(t, emb.calls[0][0], "Phoebe Bridgers")
}

func TestInterestHandler_NilEmbedder_StillReplaces(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()

	city, err := q.GetDefaultCity(ctx)
	require.NoError(t, err)
	userRow, err := q.CreateUser(ctx, store.CreateUserParams{
		Email:        "nil-emb@example.com",
		PasswordHash: "stub",
		CityID:       city.ID,
	})
	require.NoError(t, err)

	body, _ := json.Marshal(&events.InterestMessage{
		UserID: pgtypeUUIDToString(t, userRow.ID),
		Op:     events.OpReplaceInterestsAndEmbed,
		SpotifyTopArtists: []events.SpotifyTopItem{
			{Name: "MUNA", Rank: 1},
		},
	})
	h := ingest.NewInterestHandler(q, nil)
	require.NoError(t, h.Handle(ctx, body))

	rows, err := q.ListInterestsByUserAndKind(ctx, store.ListInterestsByUserAndKindParams{
		UserID: userRow.ID,
		Kind:   "spotify_top_artist",
	})
	require.NoError(t, err)
	require.Len(t, rows, 1)
}
```

Also update the two existing calls in this file (lines 56 and 95) from `ingest.NewInterestHandler(q)` to `ingest.NewInterestHandler(q, nil)`.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/ingest/ -run TestInterestHandler -v`
Expected: FAIL — `not enough arguments in call to ingest.NewInterestHandler`.

- [ ] **Step 3: Add the embedder and branch `Handle`**

In `internal/ingest/interests.go`:

Add imports: `"github.com/wmyers/heres-whats-happening/internal/matcher"`.

Change the struct and constructor:

```go
type InterestHandler struct {
	q   *store.Queries
	emb matcher.Embedder // may be nil (no TEI configured) → embed step skipped
}

func NewInterestHandler(q *store.Queries, emb matcher.Embedder) *InterestHandler {
	return &InterestHandler{q: q, emb: emb}
}
```

Replace `Handle` so it parses the message, then branches. Move the existing replace logic (the four "Replace …" blocks, lines 44–112) into a new `replaceInterests` method unchanged, and add `embedUser`:

```go
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

	// Empty Op is treated as replace-and-embed for backward compatibility.
	if m.Op != events.OpOnlyEmbed {
		if err := h.replaceInterests(ctx, pgUID, m); err != nil {
			return err
		}
	}
	return h.embedUser(ctx, pgUID)
}

// embedUser re-embeds the user via the matcher's single-user path. If no
// embedder is configured (no TEI endpoint), embedding is skipped — the daily
// match batch is the backstop.
func (h *InterestHandler) embedUser(ctx context.Context, pgUID pgtype.UUID) error {
	if h.emb == nil {
		return nil
	}
	if err := matcher.NewUserEmbedder(h.q, h.emb).EmbedUser(ctx, pgUID); err != nil {
		return fmt.Errorf("embed user: %w", err)
	}
	return nil
}

func (h *InterestHandler) replaceInterests(ctx context.Context, pgUID pgtype.UUID, m events.InterestMessage) error {
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

	// Replace track artists.
	if err := h.q.ReplaceSpotifyTrackArtistInterests(ctx, pgUID); err != nil {
		return fmt.Errorf("delete track artists: %w", err)
	}
	for _, item := range m.SpotifyTopTrackArtists {
		w := rankWeight(item.Rank)
		if err := h.q.InsertSpotifyInterest(ctx, store.InsertSpotifyInterestParams{
			UserID:          pgUID,
			Kind:            "spotify_top_track_artist",
			Value:           item.Name,
			NormalizedValue: events.NormalizeString(item.Name),
			Weight:          w,
		}); err != nil {
			return fmt.Errorf("insert track artist %q: %w", item.Name, err)
		}
	}

	// Replace saved-song artists.
	if err := h.q.ReplaceSpotifySavedSongArtistInterests(ctx, pgUID); err != nil {
		return fmt.Errorf("delete saved song artists: %w", err)
	}
	for _, item := range m.SpotifySavedSongArtists {
		w := rankWeight(item.Rank)
		if err := h.q.InsertSpotifyInterest(ctx, store.InsertSpotifyInterestParams{
			UserID:          pgUID,
			Kind:            "spotify_saved_song_artist",
			Value:           item.Name,
			NormalizedValue: events.NormalizeString(item.Name),
			Weight:          w,
		}); err != nil {
			return fmt.Errorf("insert saved song artist %q: %w", item.Name, err)
		}
	}

	// Replace genres.
	if err := h.q.ReplaceSpotifyGenreInterests(ctx, pgUID); err != nil {
		return fmt.Errorf("delete genres: %w", err)
	}
	for _, item := range m.SpotifyTopGenres {
		w := rankGenreWeight(item.Rank)
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
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/ingest/ -run TestInterestHandler -v`
Expected: PASS — all new tests plus the two pre-existing `TestInterestHandler_*` tests pass.

- [ ] **Step 5: Commit**

```bash
git add internal/ingest/interests.go internal/ingest/interests_test.go
git commit -m "Embed user in interest consumer; branch Handle on Op"
```

---

## Task 5: Manual handlers publish an `only_embed` message

**Files:**
- Modify: `internal/http/handlers/interests.go`
- Modify: `internal/http/server.go:76-77`
- Test: `internal/http/handlers/interests_test.go`

- [ ] **Step 1: Write the failing test**

In `internal/http/handlers/interests_test.go`, add a fake publisher near the top (after imports) and a test:

```go
type fakePublisher struct {
	bodies [][]byte
}

func (f *fakePublisher) Send(_ context.Context, _ string, body []byte) error {
	f.bodies = append(f.bodies, body)
	return nil
}

func TestPostInterests_PublishesEmbedMessage(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	cityID := defaultCityID(t, q)
	access := signupAndAccess(t, q, signer, cityID, "pub1@example.com")

	pub := &fakePublisher{}
	body, _ := json.Marshal(map[string]string{"value": "Indie Rock"})
	req := httptest.NewRequest(http.MethodPost, "/me/interests", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+access)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	mw := middleware.RequireAuth(signer)
	mw(handlers.CreateInterest(q, pub, "interests-queue-url")).ServeHTTP(rec, req)

	require.Equal(t, http.StatusCreated, rec.Code)
	require.Len(t, pub.bodies, 1)
	var msg events.InterestMessage
	require.NoError(t, json.Unmarshal(pub.bodies[0], &msg))
	require.Equal(t, events.OpOnlyEmbed, msg.Op)
	require.NotEmpty(t, msg.UserID)
}

func TestPostInterests_NilPublisher_StillSucceeds(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	cityID := defaultCityID(t, q)
	access := signupAndAccess(t, q, signer, cityID, "pub2@example.com")

	body, _ := json.Marshal(map[string]string{"value": "Jazz"})
	req := httptest.NewRequest(http.MethodPost, "/me/interests", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+access)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	mw := middleware.RequireAuth(signer)
	mw(handlers.CreateInterest(q, nil, "")).ServeHTTP(rec, req)

	require.Equal(t, http.StatusCreated, rec.Code)
}
```

Add `"github.com/wmyers/heres-whats-happening/internal/events"` to the test file imports. Then update the five existing call sites in this file to the new signature:
- `handlers.CreateInterest(q)` → `handlers.CreateInterest(q, nil, "")` (lines ~50, 78, 99, 133)
- `handlers.DeleteInterest(q)` → `handlers.DeleteInterest(q, nil, "")` (line ~142)

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/http/handlers/ -run TestPostInterests -v`
Expected: FAIL — `not enough arguments in call to handlers.CreateInterest`.

- [ ] **Step 3: Update handler signatures and publish**

In `internal/http/handlers/interests.go`:

Add imports: `"log"` and `"github.com/wmyers/heres-whats-happening/internal/events"`.

Add a shared publish helper:

```go
// publishEmbed asks the interest consumer to re-embed the user. Best-effort:
// a nil publisher / empty queue URL (local dev without SQS) is a no-op, and a
// send failure is logged, not returned — the daily match batch is the backstop.
func publishEmbed(ctx context.Context, pub CallbackPublisher, queueURL string, uid uuid.UUID) {
	if pub == nil || queueURL == "" {
		return
	}
	body, err := json.Marshal(events.InterestMessage{
		UserID: uid.String(),
		Op:     events.OpOnlyEmbed,
	})
	if err != nil {
		log.Printf("interests: marshal embed message: %v", err)
		return
	}
	if err := pub.Send(ctx, queueURL, body); err != nil {
		log.Printf("interests: publish embed message: %v", err)
	}
}
```

Change `CreateInterest` signature and publish after a successful write (after `writeJSON`, before returning, while `ctx` is still alive):

```go
func CreateInterest(q *store.Queries, pub CallbackPublisher, queueURL string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// ... unchanged: auth, decode, validate, CreateManualInterest, error handling ...
		writeJSON(w, http.StatusCreated, interestOut{
			ID:              uuid.UUID(row.ID.Bytes).String(),
			Value:           row.Value,
			NormalizedValue: row.NormalizedValue,
			Weight:          row.Weight,
			CreatedAt:       row.CreatedAt.Time.UTC().Format(time.RFC3339),
		})
		publishEmbed(ctx, pub, queueURL, uid)
	}
}
```

Change `DeleteInterest` signature and publish after a successful delete:

```go
func DeleteInterest(q *store.Queries, pub CallbackPublisher, queueURL string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// ... unchanged: auth, parse id, DeleteInterestByIDForUser, error handling ...
		w.WriteHeader(http.StatusNoContent)
		publishEmbed(ctx, pub, queueURL, uid)
	}
}
```

(`uid` and `ctx` are already in scope in both handlers.)

- [ ] **Step 4: Update the router**

In `internal/http/server.go`, lines 76–77:

```go
			r.Post("/me/interests", handlers.CreateInterest(s.Queries, s.QueuePublisher, s.InterestsQueueURL))
			r.Delete("/me/interests/{id}", handlers.DeleteInterest(s.Queries, s.QueuePublisher, s.InterestsQueueURL))
```

- [ ] **Step 5: Run tests**

Run: `go test ./internal/http/handlers/ -run 'TestPostInterests|TestDelete' -v`
Expected: PASS — new publish tests plus existing interest handler tests pass.

- [ ] **Step 6: Commit**

```bash
git add internal/http/handlers/interests.go internal/http/handlers/interests_test.go internal/http/server.go
git commit -m "Publish only_embed message from manual interest handlers"
```

---

## Task 6: Wire a TEI client into the interest consumer

**Files:**
- Modify: `cmd/app/main.go` (`serve()`, around lines 142–146)

- [ ] **Step 1: Build the embedder and pass it in**

In `cmd/app/main.go` `serve()`, where the interest consumer is constructed:

```go
	var interestConsumer *ingest.Consumer
	if cfg.InterestsQueueURL != "" && cipher != nil {
		var interestEmbedder matcher.Embedder
		if cfg.TEIEndpoint != "" {
			interestEmbedder = tei.New(cfg.TEIEndpoint)
		}
		ih := ingest.NewInterestHandler(q, interestEmbedder)
		interestConsumer = ingest.NewConsumer(qClient, cfg.InterestsQueueURL, ih, cfg.IngestWorkers, "interests")
	}
```

`matcher` and `tei` are already imported in `main.go` (used by `runMatch`). If goimports reports them unused elsewhere, leave them — they are used here now.

- [ ] **Step 2: Build the whole binary**

Run: `go build ./...`
Expected: builds cleanly.

- [ ] **Step 3: Run the full test suite**

Run: `go test ./...`
Expected: PASS across all packages.

- [ ] **Step 4: Commit**

```bash
git add cmd/app/main.go
git commit -m "Wire TEI client into interest consumer for on-demand embedding"
```

---

## Verification

- [ ] `go build ./...` succeeds.
- [ ] `go test ./...` passes.
- [ ] `go vet ./...` is clean.
- [ ] Manual trace: a Spotify-only user (no manual tags) connecting Spotify results in `users.interest_embedding` being set — covered by `TestInterestHandler_Replace_AlsoEmbeds` (Spotify rows + embed) and `TestEmbedUser_EmbedsSingleUserWithSpotifyOnly` (Spotify-only embed).

## Notes / known limitations (from the spec)

- Deleting a user's last interest leaves the prior embedding in place (empty text → skip). Matches current batch behavior; intentionally not addressed here.
- In `serve()` the HTTP server and interest consumer share a process, so the manual path publishes to a queue it also drains. The queue is retained for durability, retry, and parity with the Spotify path.
