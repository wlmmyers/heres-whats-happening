// Package ratelimit provides request rate limiting keyed on an opaque string
// (in practice, a client IP address).
package ratelimit

import (
	"context"
	"time"
)

// Decision is the outcome of a limit check. RetryAfter is meaningful only when
// Allowed is false.
type Decision struct {
	Allowed    bool
	RetryAfter time.Duration
}

// Limiter checks and records usage against a limit.
//
// Allow and Record are separate so a caller can gate on every request but count
// only some of them — signup counts only successful account creations, so its
// middleware calls Allow before the handler and Record only on a 201.
type Limiter interface {
	// Allow reports whether a request for key may proceed. Implementations that
	// return a non-nil error must fail open (Allowed true) so an infrastructure
	// problem cannot take the endpoint down.
	Allow(ctx context.Context, key string) (Decision, error)

	// Record registers one use against key.
	Record(ctx context.Context, key string) error
}
