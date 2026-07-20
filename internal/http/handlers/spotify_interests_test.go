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
	cityID := defaultCityID(t, q)
	email := "spotify-groups@x.com"
	token := signupAndAccess(t, q, signer, cityID, email)
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
	cityID := defaultCityID(t, q)
	email := "spotify-excl@x.com"
	token := signupAndAccess(t, q, signer, cityID, email)
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
	cityID := defaultCityID(t, q)
	email := "spotify-empty@x.com"
	token := signupAndAccess(t, q, signer, cityID, email)
	uid := userIDByEmail(t, q, email)

	insertInterest(t, pool, uid, "spotify_top_artist", "Alvvays", 0.8)

	_, groups := getSpotifyInterests(t, q, signer, token)
	require.Len(t, groups, 1)
}

func TestGetSpotifyInterests_NoDataReturnsEmptyGroups(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	cityID := defaultCityID(t, q)
	token := signupAndAccess(t, q, signer, cityID, "spotify-none@x.com")

	code, groups := getSpotifyInterests(t, q, signer, token)
	require.Equal(t, http.StatusOK, code)
	require.Empty(t, groups)

	// Verify raw response body contains empty array [], not null, to guard against regression
	req := httptest.NewRequest(http.MethodGet, "/me/spotify-interests", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	middleware.RequireAuth(signer)(handlers.SpotifyInterests(q)).ServeHTTP(rec, req)
	require.Contains(t, rec.Body.String(), `"groups":[]`)
}

func TestGetSpotifyInterests_ReturnsOnlyOwn(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	cityID := defaultCityID(t, q)

	otherEmail := "spotify-other@x.com"
	signupAndAccess(t, q, signer, cityID, otherEmail)
	otherUID := userIDByEmail(t, q, otherEmail)
	insertInterest(t, pool, otherUID, "spotify_top_artist", "Someone Else", 0.9)

	mineToken := signupAndAccess(t, q, signer, cityID, "spotify-mine@x.com")
	_, groups := getSpotifyInterests(t, q, signer, mineToken)
	require.Empty(t, groups)
}

func TestGetSpotifyInterests_OrdersByWeightDesc(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	cityID := defaultCityID(t, q)
	email := "spotify-order@x.com"
	token := signupAndAccess(t, q, signer, cityID, email)
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
