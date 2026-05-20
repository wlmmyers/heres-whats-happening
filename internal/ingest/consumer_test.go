package ingest_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/ingest"
	"github.com/wmyers/heres-whats-happening/internal/queue"
	"github.com/wmyers/heres-whats-happening/internal/store"
	"github.com/wmyers/heres-whats-happening/internal/testdb"
)

func TestConsumer_E2E_ElasticMQToPostgres(t *testing.T) {
	// ElasticMQ requires static credentials; set dummy values for local dev.
	t.Setenv("AWS_ACCESS_KEY_ID", "local")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "local")

	pool := testdb.MustOpen(t)
	q := store.New(pool)
	cityID := defaultCityID(t, q)

	qClient, err := queue.NewClient(context.Background(), "us-east-1", "http://localhost:9324")
	require.NoError(t, err)

	// Create an ephemeral queue per test — isolates from other packages and from
	// the shared events-queue. Cleaned up by t.Cleanup.
	queueName := fmt.Sprintf("ingest-test-%d", time.Now().UnixNano())
	createCtx, createCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer createCancel()
	queueURL, err := qClient.CreateTestQueue(createCtx, queueName)
	require.NoError(t, err)
	t.Cleanup(func() {
		delCtx, delCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer delCancel()
		_ = qClient.DeleteTestQueue(delCtx, queueURL)
	})

	// Publish a message
	body, _ := json.Marshal(sampleMessage())
	require.NoError(t, qClient.Send(context.Background(), queueURL, body))

	// Run consumer for a short window
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	h := ingest.NewHandler(q, cityID)
	c := ingest.NewConsumer(qClient, queueURL, h, 1)
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()

	// Wait until the event is in the DB or the context times out.
	require.Eventually(t, func() bool {
		src, err := q.GetEventSourceByName(context.Background(), "ticketmaster")
		if err != nil {
			return false
		}
		_, err = q.GetEventBySourceKey(context.Background(), store.GetEventBySourceKeyParams{
			SourceID:      src.ID,
			SourceEventID: "tm-aaa",
		})
		return err == nil
	}, 5*time.Second, 100*time.Millisecond)

	cancel()
	<-done
}
