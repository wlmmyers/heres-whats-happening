package handlers_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/auth"
	"github.com/wmyers/heres-whats-happening/internal/crypto"
	"github.com/wmyers/heres-whats-happening/internal/http/handlers"
	"github.com/wmyers/heres-whats-happening/internal/http/middleware"
	"github.com/wmyers/heres-whats-happening/internal/spotify"
	"github.com/wmyers/heres-whats-happening/internal/store"
	"github.com/wmyers/heres-whats-happening/internal/testdb"
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

type fakePub struct{ sent [][]byte }

func (p *fakePub) Send(ctx context.Context, qURL string, body []byte) error {
	p.sent = append(p.sent, body)
	return nil
}
