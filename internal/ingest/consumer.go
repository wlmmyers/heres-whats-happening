package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"sync"
	"time"

	"github.com/wmyers/heres-whats-happening/internal/events"
	"github.com/wmyers/heres-whats-happening/internal/queue"
)

// QueueClient is the subset of *queue.Client the consumer needs. Mockable.
type QueueClient interface {
	Receive(ctx context.Context, queueURL string, max int32, wait time.Duration) ([]queue.Message, error)
	Delete(ctx context.Context, queueURL, receiptHandle string) error
}

// Consumer runs N worker goroutines, each long-polling SQS and dispatching
// to a Handler. Messages that handler() succeeds on are deleted; failures are
// left to be retried by SQS visibility timeout.
type Consumer struct {
	q        QueueClient
	queueURL string
	h        *Handler
	workers  int
}

func NewConsumer(q QueueClient, queueURL string, h *Handler, workers int) *Consumer {
	if workers < 1 {
		workers = 1
	}
	return &Consumer{q: q, queueURL: queueURL, h: h, workers: workers}
}

func (c *Consumer) Run(ctx context.Context) error {
	var wg sync.WaitGroup
	for i := 0; i < c.workers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			c.workerLoop(ctx, id)
		}(i)
	}
	wg.Wait()
	return nil
}

func (c *Consumer) workerLoop(ctx context.Context, id int) {
	for {
		if ctx.Err() != nil {
			return
		}
		msgs, err := c.q.Receive(ctx, c.queueURL, 10, 20*time.Second)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			log.Printf("ingest worker %d: receive: %v", id, err)
			time.Sleep(1 * time.Second)
			continue
		}
		for _, m := range msgs {
			c.handleOne(ctx, m, id)
		}
	}
}

func (c *Consumer) handleOne(ctx context.Context, m queue.Message, workerID int) {
	var em events.Message
	if err := json.Unmarshal(m.Body, &em); err != nil {
		// Malformed message — log and delete so we don't retry forever.
		log.Printf("ingest worker %d: bad message body: %v", workerID, err)
		_ = c.q.Delete(ctx, c.queueURL, m.ReceiptHandle)
		return
	}
	if err := c.h.Handle(ctx, em); err != nil {
		log.Printf("ingest worker %d: handle %s/%s: %v", workerID, em.SourceID, em.SourceEventID, err)
		// Leave on queue — SQS will redeliver after visibility timeout.
		return
	}
	if err := c.q.Delete(ctx, c.queueURL, m.ReceiptHandle); err != nil {
		log.Printf("ingest worker %d: delete %s: %v", workerID, em.SourceEventID, err)
	}
}
