package email

import (
	"context"
	"fmt"
	"io"
	"os"
)

// logSender writes the message to a writer instead of sending it. This is the
// local-dev default: development runs the enforce path end to end and the
// confirmation link is pasted out of the server log.
type logSender struct{ out io.Writer }

// NewLogSender returns a Sender that writes to stdout.
func NewLogSender() Sender { return &logSender{out: os.Stdout} }

// NewLogSenderTo returns a Sender that writes to w. Used by tests.
func NewLogSenderTo(w io.Writer) Sender { return &logSender{out: w} }

func (s *logSender) Send(_ context.Context, msg Message) error {
	_, err := fmt.Fprintf(s.out,
		"\n=== EMAIL (not sent — EMAIL_SENDER=log) ===\nTo:      %s\nSubject: %s\n\n%s\n===========================================\n\n",
		msg.To, msg.Subject, msg.Text)
	return err
}
