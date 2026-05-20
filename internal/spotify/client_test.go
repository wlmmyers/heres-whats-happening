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
