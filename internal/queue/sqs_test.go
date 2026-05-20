package queue

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

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

// uniqueQueue creates a fresh per-test queue so tests don't conflict with each
// other or with shared queues used by other packages.
func uniqueQueue(t *testing.T, c *Client) string {
	t.Helper()
	name := fmt.Sprintf("test-queue-%d", time.Now().UnixNano())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	url, err := c.CreateTestQueue(ctx, name)
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel2()
		_ = c.DeleteTestQueue(ctx2, url)
	})
	return url
}

func TestClient_SendReceiveDelete_RoundTrip(t *testing.T) {
	c := newTestClient(t)
	queueURL := uniqueQueue(t, c)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	body := []byte(`{"ping":"pong"}`)
	require.NoError(t, c.Send(ctx, queueURL, body))

	msgs, err := c.Receive(ctx, queueURL, 10, 2*time.Second)
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	require.Equal(t, body, msgs[0].Body)

	require.NoError(t, c.Delete(ctx, queueURL, msgs[0].ReceiptHandle))

	// After delete, no message left.
	more, err := c.Receive(ctx, queueURL, 10, 1*time.Second)
	require.NoError(t, err)
	require.Empty(t, more)
}
