package scraper

import (
	"context"
	"encoding/json"
	"fmt"
)

// Publisher is the minimal queue interface the runner needs. Implemented by
// *queue.Client; mockable in tests.
type Publisher interface {
	Send(ctx context.Context, queueURL string, body []byte) error
}

// Runner orchestrates: pull events from one Adapter, publish each as a JSON
// message to a queue.
type Runner struct {
	adapter  Adapter
	pub      Publisher
	queueURL string
}

func NewRunner(adapter Adapter, pub Publisher, queueURL string) *Runner {
	return &Runner{adapter: adapter, pub: pub, queueURL: queueURL}
}

// Run executes one full fetch-and-publish cycle.
func (r *Runner) Run(ctx context.Context) error {
	msgs, err := r.adapter.Fetch(ctx)
	if err != nil {
		return fmt.Errorf("fetch %s: %w", r.adapter.Name(), err)
	}
	for _, m := range msgs {
		body, err := json.Marshal(m)
		if err != nil {
			return fmt.Errorf("marshal message: %w", err)
		}
		if err := r.pub.Send(ctx, r.queueURL, body); err != nil {
			return fmt.Errorf("publish: %w", err)
		}
	}
	return nil
}
