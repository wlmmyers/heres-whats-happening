// Package email sends transactional mail. The Sender interface has exactly one
// method so tests can substitute a fake and no test ever touches AWS.
package email

import (
	"context"
	"fmt"
	"html"
)

// Message is one outbound email. HTML and Text are both populated on every
// message we send: an HTML-only mail scores worse with spam filters, and the
// text part is what plain-text clients render.
type Message struct {
	To      string
	Subject string
	HTML    string
	Text    string
}

// Sender delivers a Message.
type Sender interface {
	Send(ctx context.Context, msg Message) error
}

const confirmationSubject = "Confirm your email address"

// ConfirmationMessage builds the signup confirmation mail. link is the full
// API URL that flips the account to confirmed.
//
// Note: this takes the recipient rather than returning a Message with an empty
// To for the caller to fill in — an incomplete Message is too easy to send.
func ConfirmationMessage(to, link string) Message {
	safeLink := html.EscapeString(link)
	return Message{
		To:      to,
		Subject: confirmationSubject,
		Text: fmt.Sprintf(`Welcome to Here's What's Happening.

Confirm your email address by opening this link:

%s

This link expires in 24 hours. If you didn't create an account, you can ignore
this message.
`, link),
		HTML: fmt.Sprintf(`<!doctype html>
<html>
  <body style="font-family: system-ui, -apple-system, sans-serif; line-height: 1.5;">
    <h1 style="font-size: 1.25rem;">Welcome to Here's What's Happening</h1>
    <p>Confirm your email address to start getting your calendar.</p>
    <p>
      <a href="%s" style="display:inline-block;padding:0.75rem 1.25rem;background:#111;color:#fff;text-decoration:none;border-radius:0.375rem;">
        Confirm email address
      </a>
    </p>
    <p style="color:#666;font-size:0.875rem;">
      This link expires in 24 hours. If you didn't create an account, you can
      ignore this message.
    </p>
    <p style="color:#666;font-size:0.75rem;word-break:break-all;">%s</p>
  </body>
</html>
`, safeLink, safeLink),
	}
}
