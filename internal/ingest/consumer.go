package ingest

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"

	"github.com/wmyers/heres-whats-happening/internal/queue"
)

// QueueClient is the subset of *queue.Client the consumer needs.
type QueueClient interface {
	Receive(ctx context.Context, queueURL string, max int32, wait time.Duration) ([]queue.Message, error)
	Delete(ctx context.Context, queueURL, receiptHandle string) error
}

// MessageHandler is implemented by per-queue payload handlers.
// Body is the raw SQS message body; the handler is responsible for
// unmarshaling and applying it. Returning a non-nil error leaves the
// message on the queue for SQS-driven retry.
type MessageHandler interface {
	Handle(ctx context.Context, body []byte) error
}

// Consumer runs N worker goroutines long-polling one queue and dispatching
// each received message to the configured Handler.
type Consumer struct {
	q        QueueClient
	queueURL string
	h        MessageHandler
	workers  int
	name     string
}

func NewConsumer(q QueueClient, queueURL string, h MessageHandler, workers int, name string) *Consumer {
	if workers < 1 {
		workers = 1
	}
	if name == "" {
		name = "ingest"
	}
	return &Consumer{q: q, queueURL: queueURL, h: h, workers: workers, name: name}
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
			log.Printf("%s worker %d: receive: %v", c.name, id, err)
			time.Sleep(1 * time.Second)
			continue
		}
		for _, m := range msgs {
			c.handleOne(ctx, m, id)
		}
	}
}

func (c *Consumer) handleOne(ctx context.Context, m queue.Message, workerID int) {
	if err := c.h.Handle(ctx, m.Body); err != nil {
		log.Printf("%s worker %d: handle: %v", c.name, workerID, err)
		return
	}
	if err := c.q.Delete(ctx, c.queueURL, m.ReceiptHandle); err != nil {
		log.Printf("%s worker %d: delete: %v", c.name, workerID, err)
	}
}
