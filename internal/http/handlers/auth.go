package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wmyers/heres-whats-happening/internal/auth"
	"github.com/wmyers/heres-whats-happening/internal/http/httperr"
	"github.com/wmyers/heres-whats-happening/internal/pwhash"
	"github.com/wmyers/heres-whats-happening/internal/store"
)

type signupRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type signupResponse struct {
	AccessToken string  `json:"access_token"`
	User        userOut `json:"user"`
}

type userOut struct {
	ID             string   `json:"id"`
	Email          string   `json:"email"`
	CityID         string   `json:"city_id"`
	Confirmed      bool     `json:"confirmed"`
	ScoreThreshold *float64 `json:"score_threshold,omitempty"`
}

// Signup creates a new user, sets the refresh cookie, and returns an access token.
// cityID is the default city assignment for v1.
//
// The new user is always unconfirmed and always gets confirmation mail: the
// account is gated off everything but /me, resend, and delete until the link is
// clicked.
func Signup(q *store.Queries, signer *auth.JWTSigner, refreshTTL time.Duration, cityID string, conf ConfirmationDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req signupRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httperr.Write(w, http.StatusBadRequest, "bad_json", "request body is not valid JSON")
			return
		}
		req.Email = strings.ToLower(strings.TrimSpace(req.Email))
		if !looksLikeEmail(req.Email) {
			httperr.Write(w, http.StatusBadRequest, "invalid_email", "email is not valid")
			return
		}
		if len(req.Password) < 8 {
			httperr.Write(w, http.StatusBadRequest, "weak_password", "password must be at least 8 characters")
			return
		}

		hash, err := pwhash.Hash(req.Password)
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, "hash_failed", "could not hash password", err)
			return
		}

		cityUUID, err := uuid.Parse(cityID)
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, "bad_city_id", "city id is invalid", err)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		row, err := q.CreateUser(ctx, store.CreateUserParams{
			Email:        req.Email,
			PasswordHash: hash,
			CityID:       pgtype.UUID{Bytes: cityUUID, Valid: true},
			// Passed explicitly rather than leaning on the column default, so
			// this handler — not the schema — decides.
			Confirmed: false,
		})
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				httperr.Write(w, http.StatusConflict, "email_taken", "an account with that email already exists")
				return
			}
			httperr.WriteErr(w, r, http.StatusInternalServerError, "db_error", "could not create user", err)
			return
		}

		userUUID := uuid.UUID(row.ID.Bytes)
		access, err := signer.SignAccess(userUUID, row.Confirmed)
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, "sign_failed", "could not sign access token", err)
			return
		}

		refreshTok, err := auth.GenerateRefresh()
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, "refresh_failed", "could not mint refresh token", err)
			return
		}
		if _, err := q.CreateRefreshToken(ctx, store.CreateRefreshTokenParams{
			UserID:    row.ID,
			TokenHash: auth.HashRefresh(refreshTok),
			ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(refreshTTL), Valid: true},
		}); err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, "db_error", "could not persist refresh token", err)
			return
		}
		setRefreshCookie(w, refreshTok, refreshTTL)

		// A send failure is logged but must not fail the request: the user still
		// reaches /confirm-email, where resend is one click away.
		if err := sendConfirmation(ctx, q, conf, row.Email, row.ID); err != nil {
			log.Printf("[%s] signup: confirmation mail for %s: %v",
				chimw.GetReqID(r.Context()), row.Email, err)
		}

		writeJSON(w, http.StatusCreated, signupResponse{
			AccessToken: access,
			User: userOut{
				ID:        row.ID.String(),
				Email:     row.Email,
				CityID:    row.CityID.String(),
				Confirmed: row.Confirmed,
			},
		})
	}
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type loginResponse struct {
	AccessToken string `json:"access_token"`
}

func Login(q *store.Queries, signer *auth.JWTSigner, refreshTTL time.Duration) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req loginRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httperr.Write(w, http.StatusBadRequest, "bad_json", "request body is not valid JSON")
			return
		}
		req.Email = strings.ToLower(strings.TrimSpace(req.Email))

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		row, err := q.GetUserByEmail(ctx, req.Email)
		if err != nil {
			httperr.Write(w, http.StatusUnauthorized, "invalid_credentials", "email or password is wrong")
			return
		}
		ok, err := pwhash.Verify(req.Password, row.PasswordHash)
		if err != nil || !ok {
			httperr.Write(w, http.StatusUnauthorized, "invalid_credentials", "email or password is wrong")
			return
		}

		userUUID := uuid.UUID(row.ID.Bytes)
		access, err := signer.SignAccess(userUUID, row.Confirmed)
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, "sign_failed", "could not sign access token", err)
			return
		}
		refreshTok, err := auth.GenerateRefresh()
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, "refresh_failed", "could not mint refresh token", err)
			return
		}
		if _, err := q.CreateRefreshToken(ctx, store.CreateRefreshTokenParams{
			UserID:    row.ID,
			TokenHash: auth.HashRefresh(refreshTok),
			ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(refreshTTL), Valid: true},
		}); err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, "db_error", "could not persist refresh token", err)
			return
		}
		setRefreshCookie(w, refreshTok, refreshTTL)

		writeJSON(w, http.StatusOK, loginResponse{AccessToken: access})
	}
}

type refreshResponse struct {
	AccessToken string `json:"access_token"`
}

func Refresh(q *store.Queries, signer *auth.JWTSigner) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie("refresh_token")
		if err != nil || c.Value == "" {
			httperr.Write(w, http.StatusUnauthorized, "no_refresh", "refresh token cookie is missing")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		row, err := q.GetActiveRefreshTokenByHash(ctx, auth.HashRefresh(c.Value))
		if err != nil {
			httperr.Write(w, http.StatusUnauthorized, "invalid_refresh", "refresh token is not valid")
			return
		}
		access, err := signer.SignAccess(uuid.UUID(row.UserID.Bytes), row.Confirmed)
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, "sign_failed", "could not sign access token", err)
			return
		}
		writeJSON(w, http.StatusOK, refreshResponse{AccessToken: access})
	}
}

func Logout(q *store.Queries) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie("refresh_token")
		if err == nil && c.Value != "" {
			ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
			defer cancel()
			_ = q.RevokeRefreshTokenByHash(ctx, auth.HashRefresh(c.Value))
		}
		http.SetCookie(w, &http.Cookie{
			Name:     "refresh_token",
			Value:    "",
			Path:     "/",
			HttpOnly: true,
			Secure:   true,
			SameSite: http.SameSiteLaxMode,
			MaxAge:   -1,
		})
		w.WriteHeader(http.StatusNoContent)
	}
}

func setRefreshCookie(w http.ResponseWriter, token string, ttl time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name:     "refresh_token",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(ttl),
	})
}

func looksLikeEmail(s string) bool {
	at := strings.Index(s, "@")
	return at > 0 && at < len(s)-1 && strings.Contains(s[at+1:], ".")
}
