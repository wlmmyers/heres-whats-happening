package email

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSESSender_BuildsInputWithBothBodyParts(t *testing.T) {
	s := &sesSender{from: "noreply@example.com"}
	in := s.buildInput(Message{
		To:      "user@example.com",
		Subject: "Confirm your email address",
		HTML:    "<p>hi</p>",
		Text:    "hi",
	})

	require.Equal(t, "noreply@example.com", *in.FromEmailAddress)
	require.Equal(t, []string{"user@example.com"}, in.Destination.ToAddresses)
	require.Equal(t, "Confirm your email address", *in.Content.Simple.Subject.Data)
	require.Equal(t, "<p>hi</p>", *in.Content.Simple.Body.Html.Data)
	require.Equal(t, "hi", *in.Content.Simple.Body.Text.Data)
}

func TestNewSESSender_RejectsEmptyFrom(t *testing.T) {
	_, err := NewSESSender(t.Context(), "us-east-1", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "From address")
}
