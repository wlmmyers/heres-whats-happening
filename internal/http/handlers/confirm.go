package handlers

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"time"

	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wmyers/heres-whats-happening/internal/auth"
	"github.com/wmyers/heres-whats-happening/internal/email"
	"github.com/wmyers/heres-whats-happening/internal/http/httperr"
	"github.com/wmyers/heres-whats-happening/internal/http/middleware"
	"github.com/wmyers/heres-whats-happening/internal/store"
)

// confirmationTTL is how long an emailed confirmation link stays valid.
const confirmationTTL = 24 * time.Hour

// ConfirmationDeps is what the auth handlers need to mint, send, and honor
// confirmation links. Grouped into a struct because Signup, ConfirmEmail, and
// ResendConfirmation all need overlapping subsets of it.
type ConfirmationDeps struct {
	Sender email.Sender
	// APIBaseURL builds the link that goes in the mail — it points at this API,
	// because the mail client navigates straight to GET /auth/confirm.
	APIBaseURL string
	// AppBaseURL is where that handler redirects the browser afterwards.
	AppBaseURL string
}

// sendConfirmation mints a fresh confirmation token for userID — replacing any
// previous one, so a resend invalidates the older link — and emails it.
func sendConfirmation(ctx context.Context, q *store.Queries, conf ConfirmationDeps, toEmail string, userID pgtype.UUID) error {
	// A nil Sender is a wiring bug — config.Load requires EMAIL_SENDER, so only
	// a Server built without it (tests, or a future caller that skips
	// config.Load) can get here. Return it as an error so the caller's logging
	// path reports it, rather than panicking mid-request.
	if conf.Sender == nil {
		return fmt.Errorf("confirmation: no email sender configured")
	}
	raw, err := auth.GenerateRefresh()
	if err != nil {
		return fmt.Errorf("generate confirmation token: %w", err)
	}
	if err := q.UpsertEmailConfirmation(ctx, store.UpsertEmailConfirmationParams{
		UserID:    userID,
		TokenHash: auth.HashRefresh(raw),
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(confirmationTTL), Valid: true},
	}); err != nil {
		return fmt.Errorf("persist confirmation token: %w", err)
	}
	link := conf.APIBaseURL + "/auth/confirm?token=" + url.QueryEscape(raw)
	if err := conf.Sender.Send(ctx, email.ConfirmationMessage(toEmail, link)); err != nil {
		return fmt.Errorf("send confirmation mail: %w", err)
	}
	return nil
}

// ConfirmEmail validates the emailed token, flips users.confirmed, and 302s
// back into the SPA. It ALWAYS redirects and never returns JSON — this is a
// browser navigation from a mail client, not an API call.
//
// Only two states produce the error redirect: an unknown token, and an
// unconsumed token past its expiry. Everything else lands on ?welcome=true.
func ConfirmEmail(q *store.Queries, conf ConfirmationDeps) http.HandlerFunc {
	confirmError := conf.AppBaseURL + "/?confirmerror=true"

	return func(w http.ResponseWriter, r *http.Request) {
		token := r.URL.Query().Get("token")
		if token == "" {
			http.Redirect(w, r, confirmError, http.StatusFound)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		row, err := q.GetEmailConfirmationByHash(ctx, auth.HashRefresh(token))
		if err != nil {
			http.Redirect(w, r, confirmError, http.StatusFound)
			return
		}

		// Carry the confirmed address back to the SPA as ?email= so the login
		// form it lands on can prefill it. Escaping matters — a raw '+' in a
		// plus-addressed email would otherwise decode to a space. If the lookup
		// fails, fall back to the bare welcome page rather than failing a
		// confirmation that otherwise succeeded.
		welcome := conf.AppBaseURL + "/?welcome=true"
		if u, uerr := q.GetUserByID(ctx, row.UserID); uerr == nil {
			welcome += "&email=" + url.QueryEscape(u.Email)
		}

		// Already consumed: almost always a mail-security scanner having
		// prefetched the link before the human clicked it. The account is
		// confirmed, so send them to the welcome page — reporting a failure
		// here would tell a successfully confirmed user their link was broken.
		if row.ConsumedAt.Valid {
			http.Redirect(w, r, welcome, http.StatusFound)
			return
		}

		if row.ExpiresAt.Time.Before(time.Now()) {
			http.Redirect(w, r, confirmError, http.StatusFound)
			return
		}

		// Order matters: confirm the user FIRST, then mark the token consumed.
		// The reverse order can strand a user — a consumed token whose owner is
		// still unconfirmed would redirect every future click to ?welcome=true
		// while the gate keeps rejecting them.
		if err := q.MarkUserConfirmed(ctx, row.UserID); err != nil {
			log.Printf("[%s] confirm: mark confirmed: %v", chimw.GetReqID(r.Context()), err)
			http.Redirect(w, r, confirmError, http.StatusFound)
			return
		}
		if err := q.ConsumeEmailConfirmation(ctx, row.UserID); err != nil {
			// Non-fatal: the user is confirmed. An unconsumed row just means a
			// replay re-runs an idempotent update.
			log.Printf("[%s] confirm: mark consumed: %v", chimw.GetReqID(r.Context()), err)
		}

		http.Redirect(w, r, welcome, http.StatusFound)
	}
}

// ResendConfirmation mints a fresh link for the authenticated user and mails
// it, invalidating the previous one. It is a no-op 204 when the user is
// already confirmed, so a stale tab cannot generate pointless mail.
//
// This route is deliberately exempt from the confirmation gate: it is one of
// the few things an unconfirmed user must be able to do.
func ResendConfirmation(q *store.Queries, conf ConfirmationDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid, ok := middleware.UserIDFromContext(r.Context())
		if !ok {
			httperr.Write(w, http.StatusUnauthorized, "no_user", "user not in context")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		pgUID := pgtype.UUID{Bytes: uid, Valid: true}
		row, err := q.GetUserByID(ctx, pgUID)
		if err != nil {
			httperr.Write(w, http.StatusNotFound, "no_user", "user not found")
			return
		}
		if row.Confirmed {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		if err := sendConfirmation(ctx, q, conf, row.Email, pgUID); err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, "send_failed",
				"could not send the confirmation email", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
