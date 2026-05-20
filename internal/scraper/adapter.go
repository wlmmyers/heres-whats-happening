// Package scraper defines the contract event-source adapters must satisfy
// and the runner that drives them.
package scraper

import (
	"context"

	"github.com/wmyers/heres-whats-happening/internal/events"
)

// Adapter pulls events from a single source and returns them in canonical form.
// Implementations are stateless. They never touch the database or the queue.
type Adapter interface {
	// Name is the event_sources.name value this adapter corresponds to (e.g., "ticketmaster").
	Name() string

	// Fetch returns the adapter's current view of the source's events.
	// Idempotency: callers may invoke Fetch repeatedly; results should be the
	// adapter's best snapshot at call time.
	Fetch(ctx context.Context) ([]events.Message, error)
}
