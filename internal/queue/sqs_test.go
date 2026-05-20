package queue

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func testQueueURL() string {
	if v := os.Getenv("EVENTS_QUEUE_URL"); v != "" {
		return v
	}
	return "http://localhost:9324/000000000000/events-queue"
}

func newTestClient(t *testing.T) *Client {
	t.Helper()
	if os.Getenv("AWS_ACCESS_KEY_ID") == "" {
		os.Setenv("AWS_ACCESS_KEY_ID", "local")
		os.Setenv("AWS_SECRET_ACCESS_KEY", "local")
	}
	if os.Getenv("AWS_REGION") == "" {
		os.Setenv("AWS_REGION", "us-east-1")
	}
	c, err := NewClient(context.Background(), "us-east-1", "http://localhost:9324")
	require.NoError(t, err)
	return c
}

func TestClient_SendReceiveDelete_RoundTrip(t *testing.T) {
	c := newTestClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Purge any prior messages so the test is deterministic.
	require.NoError(t, c.Purge(ctx, testQueueURL()))

	body := []byte(`{"ping":"pong"}`)
	require.NoError(t, c.Send(ctx, testQueueURL(), body))

	msgs, err := c.Receive(ctx, testQueueURL(), 10, 2*time.Second)
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	require.Equal(t, body, msgs[0].Body)

	require.NoError(t, c.Delete(ctx, testQueueURL(), msgs[0].ReceiptHandle))

	// After delete, no message left.
	more, err := c.Receive(ctx, testQueueURL(), 10, 1*time.Second)
	require.NoError(t, err)
	require.Empty(t, more)
}
