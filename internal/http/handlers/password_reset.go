package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wmyers/heres-whats-happening/internal/auth"
	"github.com/wmyers/heres-whats-happening/internal/email"
	"github.com/wmyers/heres-whats-happening/internal/http/httperr"
	"github.com/wmyers/heres-whats-happening/internal/http/middleware"
	"github.com/wmyers/heres-whats-happening/internal/observability"
	"github.com/wmyers/heres-whats-happening/internal/pwhash"
	"github.com/wmyers/heres-whats-happening/internal/ratelimit"
	"github.com/wmyers/heres-whats-happening/internal/store"
)

// passwordResetTTL is how long an emailed reset link stays valid.
const passwordResetTTL = 10 * time.Minute

// PasswordResetDeps is what the two reset handlers need.
type PasswordResetDeps struct {
	Sender email.Sender
	// AppBaseURL builds the link in the mail. Unlike the confirmation link it
	// points at the SPA, not this API: following the link must not consume the
	// token, because mail-security scanners prefetch links. Only the POST from
	// the form the SPA renders does.
	AppBaseURL string
	// EmailLimiter caps reset mails per ADDRESS, on top of the per-IP limit on
	// the route, so nobody can flood one inbox from a pool of IPs.
	EmailLimiter ratelimit.Limiter
	// Async runs the mail work off the request path. nil means a goroutine;
	// tests pass a synchronous runner so they can assert on what was sent.
	Async func(func())
}

func (d PasswordResetDeps) async(f func()) {
	if d.Async != nil {
		d.Async(f)
		return
	}
	go f()
}

type requestPasswordResetRequest struct {
	Email string `json:"email"`
}

// RequestPasswordReset mails a reset link to the address, if it belongs to an
// account. It answers 204 for every well-formed request — unknown address,
// malformed address, over the per-address limit, or a failed send — and does
// the lookup and the send after responding, so neither the status nor the
// response time says whether an account exists.
func RequestPasswordReset(q *store.Queries, deps PasswordResetDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req requestPasswordResetRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httperr.Write(w, http.StatusBadRequest, "bad_json", "request body is not valid JSON")
			return
		}
		address := strings.ToLower(strings.TrimSpace(req.Email))
		reqID := chimw.GetReqID(r.Context())
		ip := middleware.ClientIP(r)
		// Detached from the request: it is cancelled as soon as the 204 goes out.
		ctx := context.WithoutCancel(r.Context())

		deps.async(func() {
			ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			if err := sendPasswordReset(ctx, q, deps, address, ip); err != nil {
				log.Printf("[%s] password reset request: %v", reqID, err)
			}
		})
		w.WriteHeader(http.StatusNoContent)
	}
}

// sendPasswordReset does the work behind RequestPasswordReset. A nil error
// covers every outcome the caller should not hear about, including "no such
// account".
func sendPasswordReset(ctx context.Context, q *store.Queries, deps PasswordResetDeps, address, ip string) error {
	if !looksLikeEmail(address) {
		return nil
	}
	if deps.Sender == nil {
		return fmt.Errorf("no email sender configured")
	}

	// Every address spends from its own budget, including addresses with no
	// account, so the limiter's behavior cannot be used to probe for accounts.
	if deps.EmailLimiter != nil {
		key := "email:" + address
		d, err := deps.EmailLimiter.Allow(ctx, key)
		if err != nil {
			log.Printf("password reset: email limiter check failed: %v", err)
		}
		if !d.Allowed {
			observability.Default.RateLimitRejection(middleware.EndpointPasswordResetEmail, ip)
			return nil
		}
		if err := deps.EmailLimiter.Record(ctx, key); err != nil {
			log.Printf("password reset: email limiter record failed: %v", err)
		}
	}

	user, err := q.GetUserByEmail(ctx, address)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("look up user: %w", err)
	}

	raw, err := auth.GenerateRefresh()
	if err != nil {
		return fmt.Errorf("generate reset token: %w", err)
	}
	if err := q.UpsertPasswordReset(ctx, store.UpsertPasswordResetParams{
		UserID:    user.ID,
		TokenHash: auth.HashRefresh(raw),
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(passwordResetTTL), Valid: true},
	}); err != nil {
		return fmt.Errorf("persist reset token: %w", err)
	}
	link := deps.AppBaseURL + "/reset-password?token=" + url.QueryEscape(raw)
	if err := deps.Sender.Send(ctx, email.PasswordResetMessage(user.Email, link)); err != nil {
		return fmt.Errorf("send reset mail: %w", err)
	}
	return nil
}

type resetPasswordRequest struct {
	Token    string `json:"token"`
	Password string `json:"password"`
}

type resetPasswordResponse struct {
	// Email lets the SPA prefill the sign-in form it sends the user to next.
	Email string `json:"email"`
}

// ResetPassword spends a reset token to set a new password. In the same
// statement it marks the account confirmed and revokes every refresh token,
// signing the user out everywhere. It does not sign them in: the SPA sends
// them to /login.
func ResetPassword(q *store.Queries, deps PasswordResetDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req resetPasswordRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httperr.Write(w, http.StatusBadRequest, "bad_json", "request body is not valid JSON")
			return
		}
		if req.Token == "" {
			httperr.Write(w, http.StatusBadRequest, "invalid_token", "this reset link is invalid or has expired")
			return
		}
		if len(req.Password) < minPasswordLen {
			httperr.Write(w, http.StatusBadRequest, "weak_password", "password must be at least 8 characters")
			return
		}

		hash, err := pwhash.Hash(req.Password)
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, "hash_failed", "could not hash password", err)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		row, err := q.ResetPasswordWithToken(ctx, store.ResetPasswordWithTokenParams{
			TokenHash:    auth.HashRefresh(req.Token),
			PasswordHash: hash,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			httperr.Write(w, http.StatusBadRequest, "invalid_token", "this reset link is invalid or has expired")
			return
		}
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, "db_error", "could not reset password", err)
			return
		}

		// Best effort: the password is already changed, so a failed notice
		// must not turn a successful reset into an error.
		if deps.Sender != nil {
			reqID := chimw.GetReqID(r.Context())
			sendCtx := context.WithoutCancel(r.Context())
			deps.async(func() {
				ctx, cancel := context.WithTimeout(sendCtx, 15*time.Second)
				defer cancel()
				if err := deps.Sender.Send(ctx, email.PasswordChangedMessage(row.Email)); err != nil {
					log.Printf("[%s] password reset: send changed notice: %v", reqID, err)
				}
			})
		}

		writeJSON(w, http.StatusOK, resetPasswordResponse{Email: row.Email})
	}
}
