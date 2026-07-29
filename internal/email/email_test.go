package email_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/email"
)

func TestConfirmationMessage_CarriesBothPartsAndTheLink(t *testing.T) {
	link := "https://api.example.com/auth/confirm?token=abc123"
	msg := email.ConfirmationMessage("user@example.com", link)

	require.Equal(t, "user@example.com", msg.To)
	require.NotEmpty(t, msg.Subject)
	// Both parts, so the mail is not spam-scored as HTML-only.
	require.NotEmpty(t, msg.HTML)
	require.NotEmpty(t, msg.Text)
	require.Contains(t, msg.HTML, link)
	require.Contains(t, msg.Text, link)
}

func TestConfirmationMessage_EscapesTheLinkInHTML(t *testing.T) {
	msg := email.ConfirmationMessage("user@example.com",
		"https://api.example.com/auth/confirm?token=a&b=\"x\"")
	require.NotContains(t, msg.HTML, `token=a&b="x"`, "raw ampersand/quote must be escaped")
	require.Contains(t, msg.HTML, "&amp;")
}

func TestFake_CapturesMessages(t *testing.T) {
	f := &email.Fake{}
	require.NoError(t, f.Send(context.Background(), email.Message{To: "a@example.com", Subject: "one"}))
	require.NoError(t, f.Send(context.Background(), email.Message{To: "b@example.com", Subject: "two"}))

	require.Len(t, f.Messages(), 2)
	require.Equal(t, "a@example.com", f.Messages()[0].To)
	require.Equal(t, "two", f.Last().Subject)
}

func TestFake_ReturnsConfiguredError(t *testing.T) {
	want := errors.New("ses is down")
	f := &email.Fake{Err: want}
	err := f.Send(context.Background(), email.Message{To: "a@example.com"})
	require.ErrorIs(t, err, want)
	require.Empty(t, f.Messages(), "a failed send must not be recorded as sent")
}

func TestLogSender_WritesTheLinkAndSucceeds(t *testing.T) {
	var buf strings.Builder
	s := email.NewLogSenderTo(&buf)
	require.NoError(t, s.Send(context.Background(), email.Message{
		To: "user@example.com", Subject: "Confirm", Text: "click https://x/confirm?token=t",
	}))
	out := buf.String()
	require.Contains(t, out, "user@example.com")
	require.Contains(t, out, "https://x/confirm?token=t")
}
