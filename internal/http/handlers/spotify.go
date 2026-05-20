package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wmyers/heres-whats-happening/internal/crypto"
	"github.com/wmyers/heres-whats-happening/internal/http/httperr"
	"github.com/wmyers/heres-whats-happening/internal/http/middleware"
	spotifyscrape "github.com/wmyers/heres-whats-happening/internal/scraper/spotify"
	"github.com/wmyers/heres-whats-happening/internal/spotify"
	"github.com/wmyers/heres-whats-happening/internal/store"
)

const oauthCookieName = "spotify_oauth"
const oauthCookieTTL = 10 * time.Minute

// CallbackPublisher matches scraper.Publisher: Send(ctx, queueURL, body).
type CallbackPublisher interface {
	Send(ctx context.Context, queueURL string, body []byte) error
}

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

// SpotifyCallback handles the OAuth redirect-back from Spotify. It validates
// the state cookie, exchanges the code for tokens, persists them (encrypted),
// and triggers an immediate one-user scrape.
func SpotifyCallback(
	q *store.Queries,
	client *spotify.Client,
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
		c, err := r.Cookie(oauthCookieName)
		if err != nil || c.Value == "" {
			httperr.Write(w, http.StatusBadRequest, "no_state", "missing oauth state cookie")
			return
		}
		expectedState, verifier, err := spotify.OpenOAuthState(hmacKey, c.Value)
		if err != nil {
			httperr.Write(w, http.StatusBadRequest, "bad_state", "oauth state is not valid")
			return
		}
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

		// Immediate sync — best-effort. If it fails, the daily scraper picks up.
		adapter := spotifyscrape.NewAdapter(q, cipher, client, pub, queueURL)
		_ = adapter.ScrapeOne(ctx, pgUID)

		writeJSON(w, http.StatusOK, map[string]string{"status": "connected"})
	}
}

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
