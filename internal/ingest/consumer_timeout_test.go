package ingest_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/ingest"
	"github.com/wmyers/heres-whats-happening/internal/queue"
)

// stubQueue yields exactly one message, then parks until the consumer shuts
// down, so a test observes a single Handle call without spinning.
type stubQueue struct {
	mu      sync.Mutex
	sent    bool
	deletes int
}

func (q *stubQueue) Receive(ctx context.Context, _ string, _ int32, _ time.Duration) ([]queue.Message, error) {
	q.mu.Lock()
	first := !q.sent
	q.sent = true
	q.mu.Unlock()
	if first {
		return []queue.Message{{Body: []byte(`{}`), ReceiptHandle: "rh-1"}}, nil
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

func (q *stubQueue) Delete(context.Context, string, string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.deletes++
	return nil
}

// recordingHandler captures the context it was handed and optionally blocks
// until that context is done, so a test can see whether anything cancels it.
type recordingHandler struct {
	block bool

	mu          sync.Mutex
	called      chan struct{}
	unblocked   chan struct{}
	deadline    time.Time
	hadDeadline bool
	ctxErr      error
}

func newRecordingHandler(block bool) *recordingHandler {
	return &recordingHandler{block: block, called: make(chan struct{}), unblocked: make(chan struct{})}
}

func (h *recordingHandler) Handle(ctx context.Context, _ []byte) error {
	d, ok := ctx.Deadline()
	h.mu.Lock()
	h.deadline, h.hadDeadline = d, ok
	h.mu.Unlock()
	close(h.called)
	if h.block {
		<-ctx.Done()
		h.mu.Lock()
		h.ctxErr = ctx.Err()
		h.mu.Unlock()
		close(h.unblocked)
	}
	return nil
}

func runConsumer(t *testing.T, c *ingest.Consumer, h *recordingHandler) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = c.Run(ctx) }()
	select {
	case <-h.called:
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("handler was never called")
	}
	// A blocking handler must be released by its OWN deadline. Cancelling the
	// parent here would release it too, and the test would pass whether or not
	// handleOne sets a deadline at all.
	if h.block {
		select {
		case <-h.unblocked:
		case <-time.After(5 * time.Second):
			cancel()
			t.Fatal("handler was never released by its deadline")
		}
	}
	cancel()
	<-done
}

// Without a deadline, a message that cannot get a connection waits forever and
// pins one of the pool's ten slots. Eight such workers starve the HTTP handlers
// sharing the pool, and the failure surfaces as unexplained API latency rather
// than as an error anyone can act on.
func TestConsumer_GivesEachMessageADeadline(t *testing.T) {
	h := newRecordingHandler(false)
	c := ingest.NewConsumer(&stubQueue{}, "q", h, 1, "test")
	runConsumer(t, c, h)

	h.mu.Lock()
	defer h.mu.Unlock()
	require.True(t, h.hadDeadline, "the handler context must carry a deadline")
}

// SQS hands the message to another worker once the visibility timeout lapses.
// Both queues this consumer serves are set to 30s (terraform/prod/sqs.tf:28,
// enrichment.tf:11), so a handler still running past that is a second copy of
// work already redelivered — two workers interleaving replaceInterests'
// DELETE/INSERT for one user.
func TestConsumer_MessageDeadlineStaysUnderTheSQSVisibilityTimeout(t *testing.T) {
	h := newRecordingHandler(false)
	c := ingest.NewConsumer(&stubQueue{}, "q", h, 1, "test")
	start := time.Now()
	runConsumer(t, c, h)

	h.mu.Lock()
	defer h.mu.Unlock()
	require.True(t, h.hadDeadline, "no deadline at all would pass the bound below vacuously")
	require.Less(t, h.deadline.Sub(start), 30*time.Second,
		"a handler must not outlive its message's SQS invisibility")
	require.Greater(t, h.deadline.Sub(start), time.Second, "the bound must leave room for real work")
}

func TestConsumer_CancelsAHandlerThatOverrunsItsDeadline(t *testing.T) {
	h := newRecordingHandler(true)
	c := ingest.NewConsumer(&stubQueue{}, "q", h, 1, "test")
	c.HandleTimeout = 50 * time.Millisecond
	runConsumer(t, c, h)

	h.mu.Lock()
	defer h.mu.Unlock()
	require.ErrorIs(t, h.ctxErr, context.DeadlineExceeded,
		"an overrunning handler must be cancelled by its own deadline, not left running")
}
