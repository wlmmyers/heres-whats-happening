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

// defaultHandleTimeout bounds one message's processing.
//
// It sits under the 30s visibility timeout both consumed queues are configured
// with (terraform/prod/sqs.tf, terraform/prod/enrichment.tf), leaving headroom
// for the delete that follows. Past that timeout SQS has already redelivered
// the message, so a still-running handler is a duplicate of work another worker
// now owns — and it is pinning one of the pool's connections while it does it.
const defaultHandleTimeout = 25 * time.Second

// Consumer runs N worker goroutines long-polling one queue and dispatching
// each received message to the configured Handler.
type Consumer struct {
	// HandleTimeout bounds one message's processing. Set before Run.
	HandleTimeout time.Duration

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
	return &Consumer{q: q, queueURL: queueURL, h: h, workers: workers, name: name, HandleTimeout: defaultHandleTimeout}
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

// handleOne applies one message under its own deadline. The bound lives here
// rather than in each handler so it covers every handler, including ones added
// later — the same reasoning the router uses for its rate-limit groups. Without
// it a handler inherits the process-lifetime context and can block on the
// connection pool indefinitely.
func (c *Consumer) handleOne(ctx context.Context, m queue.Message, workerID int) {
	ctx, cancel := context.WithTimeout(ctx, c.HandleTimeout)
	defer cancel()

	if err := c.h.Handle(ctx, m.Body); err != nil {
		log.Printf("%s worker %d: handle: %v", c.name, workerID, err)
		return
	}
	if err := c.q.Delete(ctx, c.queueURL, m.ReceiptHandle); err != nil {
		log.Printf("%s worker %d: delete: %v", c.name, workerID, err)
	}
}
