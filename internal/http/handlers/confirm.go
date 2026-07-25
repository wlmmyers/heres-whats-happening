package handlers

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wmyers/heres-whats-happening/internal/auth"
	"github.com/wmyers/heres-whats-happening/internal/config"
	"github.com/wmyers/heres-whats-happening/internal/email"
	"github.com/wmyers/heres-whats-happening/internal/store"
)

// confirmationTTL is how long an emailed confirmation link stays valid.
const confirmationTTL = 24 * time.Hour

// ConfirmationDeps is what the auth handlers need to mint, send, and honor
// confirmation links. Grouped into a struct because Signup, ConfirmEmail, and
// ResendConfirmation all need overlapping subsets of it.
type ConfirmationDeps struct {
	Mode   config.ConfirmationMode
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
