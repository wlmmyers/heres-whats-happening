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

	ctx := context.Background()
	city, err := q.GetDefaultCity(ctx)
	require.NoError(t, err)
	userRow, err := q.CreateUser(ctx, store.CreateUserParams{
		Email:        "spotify-scrape@example.com",
		PasswordHash: "stub",
		CityID:       city.ID,
	})
	require.NoError(t, err)

	key := makeTestKey(t)
	cipher, err := crypto.NewCipher(key)
	require.NoError(t, err)
	at, _ := cipher.Encrypt([]byte("AT-original"))
	rt, _ := cipher.Encrypt([]byte("RT-original"))
	require.NoError(t, q.UpsertUserSpotifyTokens(ctx, store.UpsertUserSpotifyTokensParams{
		UserID:          userRow.ID,
		AccessTokenEnc:  at,
		RefreshTokenEnc: rt,
		ExpiresAt:       pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
		Scope:           "user-top-read user-read-recently-played",
	}))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "Bearer AT-original", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/me/top/artists":
			_, _ = w.Write([]byte(`{
			  "items": [
			    {"name": "Phoebe Bridgers", "genres": ["indie pop", "indie rock"]},
			    {"name": "MUNA", "genres": ["indie pop"]}
			  ]
			}`))
		case "/v1/me/top/tracks":
			// Boygenius appears twice (one track is a collab) → deduped to one
			// entry; MUNA also surfaces here and as a top artist (the two lists
			// are independent, so it legitimately appears in both).
			_, _ = w.Write([]byte(`{
			  "items": [
			    {"name": "Not Strong Enough", "artists": [{"name": "boygenius"}]},
			    {"name": "$20", "artists": [{"name": "boygenius"}, {"name": "MUNA"}]}
			  ]
			}`))
		case "/v1/me/tracks":
			// Two saved tracks; the newer (Lucy Dacus) ranks first.
			_, _ = w.Write([]byte(`{
			  "next": null,
			  "items": [
			    {"added_at": "2024-01-01T00:00:00Z", "track": {"artists": [{"name": "Julien Baker"}]}},
			    {"added_at": "2024-06-01T00:00:00Z", "track": {"artists": [{"name": "Lucy Dacus"}]}}
			  ]
			}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
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
	// Track artists are their own ranked list, deduped by name (boygenius once).
	require.Len(t, msg.SpotifyTopTrackArtists, 2)
	require.Equal(t, "boygenius", msg.SpotifyTopTrackArtists[0].Name)
	require.Equal(t, 1, msg.SpotifyTopTrackArtists[0].Rank)
	require.Equal(t, "MUNA", msg.SpotifyTopTrackArtists[1].Name)
	require.Equal(t, 2, msg.SpotifyTopTrackArtists[1].Rank)
	// Genres ranked by frequency: indie pop appears in 2 artists → rank 1; indie rock in 1 → rank 2.
	require.Equal(t, "indie pop", msg.SpotifyTopGenres[0].Name)
	require.Equal(t, "indie rock", msg.SpotifyTopGenres[1].Name)
	// Saved-song artists ranked by recency (most recently saved first).
	require.Len(t, msg.SpotifySavedSongArtists, 2)
	require.Equal(t, "Lucy Dacus", msg.SpotifySavedSongArtists[0].Name)
	require.Equal(t, 1, msg.SpotifySavedSongArtists[0].Rank)
	require.Equal(t, "Julien Baker", msg.SpotifySavedSongArtists[1].Name)
	require.Equal(t, 2, msg.SpotifySavedSongArtists[1].Rank)
}

func TestScrapeOne_SavedTracksForbidden_NonFatal(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()
	city, err := q.GetDefaultCity(ctx)
	require.NoError(t, err)
	userRow, err := q.CreateUser(ctx, store.CreateUserParams{
		Email:        "saved-forbidden@example.com",
		PasswordHash: "stub",
		CityID:       city.ID,
	})
	require.NoError(t, err)

	key := makeTestKey(t)
	cipher, err := crypto.NewCipher(key)
	require.NoError(t, err)
	at, _ := cipher.Encrypt([]byte("AT-original"))
	rt, _ := cipher.Encrypt([]byte("RT-original"))
	// Scope granted before user-library-read existed: /me/tracks will 403.
	require.NoError(t, q.UpsertUserSpotifyTokens(ctx, store.UpsertUserSpotifyTokensParams{
		UserID:          userRow.ID,
		AccessTokenEnc:  at,
		RefreshTokenEnc: rt,
		ExpiresAt:       pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
		Scope:           "user-top-read user-read-recently-played",
	}))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/me/top/artists":
			_, _ = w.Write([]byte(`{"items":[{"name":"Phoebe Bridgers","genres":["indie pop"]}]}`))
		case "/v1/me/top/tracks":
			_, _ = w.Write([]byte(`{"items":[{"name":"T","artists":[{"name":"boygenius"}]}]}`))
		case "/v1/me/tracks":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":{"status":403,"message":"Insufficient client scope"}}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	client := spotifyclient.New("cid", "csec", "http://localhost/cb", srv.URL)
	pub := &fakePublisher{}
	adapter := spotifyscrape.NewAdapter(q, cipher, client, pub, "http://localhost/q")

	// A 403 on saved tracks must not abort the scrape: top artists/tracks
	// still publish, saved songs come through empty.
	require.NoError(t, adapter.ScrapeOne(ctx, userRow.ID))
	require.Len(t, pub.sent, 1)

	var msg events.InterestMessage
	require.NoError(t, json.Unmarshal(pub.sent[0], &msg))
	require.Len(t, msg.SpotifyTopArtists, 1)
	require.Equal(t, "Phoebe Bridgers", msg.SpotifyTopArtists[0].Name)
	require.Len(t, msg.SpotifyTopTrackArtists, 1)
	require.Empty(t, msg.SpotifySavedSongArtists)
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
		case "/v1/me/top/tracks":
			apiCalls++
			require.Equal(t, "Bearer AT-new", r.Header.Get("Authorization"))
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"items":[{"name":"T","artists":[{"name":"Y"}]}]}`))
		case "/v1/me/tracks":
			apiCalls++
			require.Equal(t, "Bearer AT-new", r.Header.Get("Authorization"))
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"next":null,"items":[{"added_at":"2024-01-01T00:00:00Z","track":{"artists":[{"name":"Z"}]}}]}`))
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
	require.Equal(t, 3, apiCalls) // /top/artists + /top/tracks + /me/tracks

	// Refreshed AT was persisted (encrypted).
	row, err := q.GetUserSpotifyTokens(ctx, userRow.ID)
	require.NoError(t, err)
	decoded, err := cipher.Decrypt(row.AccessTokenEnc)
	require.NoError(t, err)
	require.Equal(t, "AT-new", string(decoded))
}

func TestScrapeOne_SavedSongArtistsRankedByRecency(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()
	city, err := q.GetDefaultCity(ctx)
	require.NoError(t, err)
	userRow, err := q.CreateUser(ctx, store.CreateUserParams{
		Email:        "saved-songs@example.com",
		PasswordHash: "stub",
		CityID:       city.ID,
	})
	require.NoError(t, err)

	key := makeTestKey(t)
	cipher, err := crypto.NewCipher(key)
	require.NoError(t, err)
	at, _ := cipher.Encrypt([]byte("AT-original"))
	rt, _ := cipher.Encrypt([]byte("RT-original"))
	require.NoError(t, q.UpsertUserSpotifyTokens(ctx, store.UpsertUserSpotifyTokensParams{
		UserID:          userRow.ID,
		AccessTokenEnc:  at,
		RefreshTokenEnc: rt,
		ExpiresAt:       pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
		Scope:           "user-top-read",
	}))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/me/top/artists", "/v1/me/top/tracks":
			_, _ = w.Write([]byte(`{"items":[]}`))
		case "/v1/me/tracks":
			// Saved tracks returned out of recency order, with a duplicate artist
			// at an older timestamp. After sorting by added_at descending and
			// deduping, the duplicate's older entry is skipped.
			_, _ = w.Write([]byte(`{
			  "next": null,
			  "items": [
			    {"added_at": "2024-01-01T00:00:00Z", "track": {"artists": [{"name": "Old Band"}]}},
			    {"added_at": "2024-05-01T00:00:00Z", "track": {"artists": [{"name": "New Band"}]}},
			    {"added_at": "2024-03-01T00:00:00Z", "track": {"artists": [{"name": "Mid Band"}]}},
			    {"added_at": "2023-01-01T00:00:00Z", "track": {"artists": [{"name": "New Band"}]}}
			  ]
			}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	client := spotifyclient.New("cid", "csec", "http://localhost/cb", srv.URL)
	pub := &fakePublisher{}
	adapter := spotifyscrape.NewAdapter(q, cipher, client, pub, "http://localhost/q")

	require.NoError(t, adapter.ScrapeOne(ctx, userRow.ID))
	require.Len(t, pub.sent, 1)

	var msg events.InterestMessage
	require.NoError(t, json.Unmarshal(pub.sent[0], &msg))
	require.Len(t, msg.SpotifySavedSongArtists, 3)
	require.Equal(t, "New Band", msg.SpotifySavedSongArtists[0].Name)
	require.Equal(t, 1, msg.SpotifySavedSongArtists[0].Rank)
	require.Equal(t, "Mid Band", msg.SpotifySavedSongArtists[1].Name)
	require.Equal(t, 2, msg.SpotifySavedSongArtists[1].Rank)
	require.Equal(t, "Old Band", msg.SpotifySavedSongArtists[2].Name)
	require.Equal(t, 3, msg.SpotifySavedSongArtists[2].Rank)
}
