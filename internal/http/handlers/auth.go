package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

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
	ID    string `json:"id"`
	Email string `json:"email"`
}

// Signup creates a new user, sets the refresh cookie, and returns an access token.
// cityID is the default city assignment for v1.
func Signup(q *store.Queries, signer *auth.JWTSigner, refreshTTL time.Duration, cityID string) http.HandlerFunc {
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
			httperr.Write(w, http.StatusInternalServerError, "hash_failed", "could not hash password")
			return
		}

		cityUUID, err := uuid.Parse(cityID)
		if err != nil {
			httperr.Write(w, http.StatusInternalServerError, "bad_city_id", "city id is invalid")
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		row, err := q.CreateUser(ctx, store.CreateUserParams{
			Email:        req.Email,
			PasswordHash: hash,
			CityID:       pgtype.UUID{Bytes: cityUUID, Valid: true},
		})
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				httperr.Write(w, http.StatusConflict, "email_taken", "an account with that email already exists")
				return
			}
			httperr.Write(w, http.StatusInternalServerError, "db_error", "could not create user")
			return
		}

		userUUID := uuid.UUID(row.ID.Bytes)
		access, err := signer.SignAccess(userUUID)
		if err != nil {
			httperr.Write(w, http.StatusInternalServerError, "sign_failed", "could not sign access token")
			return
		}

		refreshTok, err := auth.GenerateRefresh()
		if err != nil {
			httperr.Write(w, http.StatusInternalServerError, "refresh_failed", "could not mint refresh token")
			return
		}
		if _, err := q.CreateRefreshToken(ctx, store.CreateRefreshTokenParams{
			UserID:    row.ID,
			TokenHash: auth.HashRefresh(refreshTok),
			ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(refreshTTL), Valid: true},
		}); err != nil {
			httperr.Write(w, http.StatusInternalServerError, "db_error", "could not persist refresh token")
			return
		}
		setRefreshCookie(w, refreshTok, refreshTTL)

		writeJSON(w, http.StatusCreated, signupResponse{
			AccessToken: access,
			User:        userOut{ID: row.ID.String(), Email: row.Email},
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
		access, err := signer.SignAccess(userUUID)
		if err != nil {
			httperr.Write(w, http.StatusInternalServerError, "sign_failed", "could not sign access token")
			return
		}
		refreshTok, err := auth.GenerateRefresh()
		if err != nil {
			httperr.Write(w, http.StatusInternalServerError, "refresh_failed", "could not mint refresh token")
			return
		}
		if _, err := q.CreateRefreshToken(ctx, store.CreateRefreshTokenParams{
			UserID:    row.ID,
			TokenHash: auth.HashRefresh(refreshTok),
			ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(refreshTTL), Valid: true},
		}); err != nil {
			httperr.Write(w, http.StatusInternalServerError, "db_error", "could not persist refresh token")
			return
		}
		setRefreshCookie(w, refreshTok, refreshTTL)

		writeJSON(w, http.StatusOK, loginResponse{AccessToken: access})
	}
}

func setRefreshCookie(w http.ResponseWriter, token string, ttl time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name:     "refresh_token",
		Value:    token,
		Path:     "/auth",
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
