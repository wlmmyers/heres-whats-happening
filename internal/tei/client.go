// Package tei wraps the Hugging Face text-embeddings-inference HTTP API.
// TEI returns a 2D array of float32 vectors — one per input string.
package tei

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// maxBatchSize matches TEI's default --max-client-batch-size. Requests larger
// than this are split into sub-batches by Embed.
const maxBatchSize = 32

// Retry defaults. TEI runs on Fargate Spot, so a reclaim can make it briefly
// unreachable (connection refused) or unhealthy (5xx) while ECS launches a
// replacement task. Retrying with backoff rides through a short blip. These
// bridge a brief gap only — a long outage still exhausts and returns an error,
// leaving the nightly match job's next-run re-selection as the backstop.
const (
	defaultMaxAttempts = 4
	defaultBaseDelay   = 1 * time.Second
	defaultMaxDelay    = 10 * time.Second
)

type Client struct {
	baseURL string
	http    *http.Client

	// Retry policy for transient failures (transport errors and 5xx). Fields,
	// not constructor args, so tests can shrink the backoff without a wider API.
	maxAttempts int
	baseDelay   time.Duration
	maxDelay    time.Duration
}

func New(baseURL string) *Client {
	return &Client{
		baseURL:     baseURL,
		http:        &http.Client{Timeout: 60 * time.Second},
		maxAttempts: defaultMaxAttempts,
		baseDelay:   defaultBaseDelay,
		maxDelay:    defaultMaxDelay,
	}
}

// Embed sends inputs to TEI's /embed endpoint and returns a vector per input,
// chunking into sub-batches of maxBatchSize to stay under TEI's server limit.
// Empty input → empty output without an HTTP call.
func (c *Client) Embed(ctx context.Context, inputs []string) ([][]float32, error) {
	if len(inputs) == 0 {
		return nil, nil
	}
	out := make([][]float32, 0, len(inputs))
	for start := 0; start < len(inputs); start += maxBatchSize {
		end := start + maxBatchSize
		if end > len(inputs) {
			end = len(inputs)
		}
		chunk, err := c.embedChunk(ctx, inputs[start:end])
		if err != nil {
			return nil, err
		}
		out = append(out, chunk...)
	}
	return out, nil
}

// embedChunk sends one sub-batch, retrying transient failures with exponential
// backoff. Non-transient errors (4xx, malformed request/response) fail fast.
func (c *Client) embedChunk(ctx context.Context, inputs []string) ([][]float32, error) {
	var lastErr error
	for attempt := 1; attempt <= c.maxAttempts; attempt++ {
		vecs, err := c.embedChunkOnce(ctx, inputs)
		if err == nil {
			return vecs, nil
		}
		var re *retryableError
		if !errors.As(err, &re) {
			return nil, err // client error / bad payload — retrying won't help
		}
		lastErr = err
		if attempt == c.maxAttempts {
			break
		}
		if werr := waitBackoff(ctx, c.baseDelay, c.maxDelay, attempt); werr != nil {
			return nil, werr // context canceled/expired during backoff
		}
	}
	return nil, lastErr
}

func (c *Client) embedChunkOnce(ctx context.Context, inputs []string) ([][]float32, error) {
	body, err := json.Marshal(struct {
		Inputs []string `json:"inputs"`
	}{Inputs: inputs})
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/embed", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		// Transport failure (dial/connection refused/timeout) — the reclaim case.
		return nil, &retryableError{fmt.Errorf("http: %w", err)}
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(resp.Body)
		statusErr := fmt.Errorf("tei %d: %s", resp.StatusCode, string(b))
		if resp.StatusCode >= 500 {
			return nil, &retryableError{statusErr} // server-side/transient
		}
		return nil, statusErr // 4xx — client error, do not retry
	}
	var out [][]float32
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return out, nil
}

// retryableError marks a failure worth retrying (transport error or 5xx).
type retryableError struct{ err error }

func (e *retryableError) Error() string { return e.err.Error() }
func (e *retryableError) Unwrap() error { return e.err }

// waitBackoff sleeps for base*2^(attempt-1), capped at max, returning early if
// ctx is done. A non-positive base means no delay (used in tests).
func waitBackoff(ctx context.Context, base, max time.Duration, attempt int) error {
	if base <= 0 {
		return ctx.Err()
	}
	d := base << (attempt - 1)
	if d <= 0 || d > max { // d <= 0 guards against shift overflow
		d = max
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
