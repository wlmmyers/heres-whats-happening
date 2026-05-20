package handlers_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
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
