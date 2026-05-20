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
		state, err := spotify.NewVerifier() // reuse random-string helper
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
