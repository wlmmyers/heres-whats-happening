package email

import (
	"context"
	"sync"
)

// Fake is a Sender that records messages instead of sending them. It lives in
// the package (not a _test.go file) so handler tests in other packages can use
// it. Safe for concurrent use.
type Fake struct {
	// Err, when non-nil, is returned by every Send and the message is not
	// recorded — used to exercise send-failure paths.
	Err error

	mu   sync.Mutex
	sent []Message
}

func (f *Fake) Send(_ context.Context, msg Message) error {
	if f.Err != nil {
		return f.Err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, msg)
	return nil
}

// Messages returns a copy of everything sent so far.
func (f *Fake) Messages() []Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Message, len(f.sent))
	copy(out, f.sent)
	return out
}

// Last returns the most recent message, or the zero Message if none were sent.
func (f *Fake) Last() Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.sent) == 0 {
		return Message{}
	}
	return f.sent[len(f.sent)-1]
}

// Reset drops all recorded messages.
func (f *Fake) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = nil
}
