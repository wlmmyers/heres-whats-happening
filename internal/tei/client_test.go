package tei

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestEmbed_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/embed", r.URL.Path)
		var req struct {
			Inputs []string `json:"inputs"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		require.Len(t, req.Inputs, 2)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[[0.1, 0.2, 0.3], [0.4, 0.5, 0.6]]`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	vecs, err := c.Embed(context.Background(), []string{"a", "b"})
	require.NoError(t, err)
	require.Len(t, vecs, 2)
	require.Equal(t, []float32{0.1, 0.2, 0.3}, vecs[0])
	require.Equal(t, []float32{0.4, 0.5, 0.6}, vecs[1])
}

func TestEmbed_EmptyInput_NoCall(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++ }))
	defer srv.Close()
	c := New(srv.URL)
	vecs, err := c.Embed(context.Background(), nil)
	require.NoError(t, err)
	require.Empty(t, vecs)
	require.Equal(t, 0, calls)
}

func TestEmbed_ChunksLargeBatch(t *testing.T) {
	var batchSizes []int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Inputs []string `json:"inputs"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		batchSizes = append(batchSizes, len(req.Inputs))
		require.LessOrEqual(t, len(req.Inputs), 32, "TEI rejects batches larger than 32")

		out := make([][]float32, len(req.Inputs))
		for i, s := range req.Inputs {
			// Echo the input length as the vector — gives us a way to verify ordering.
			out[i] = []float32{float32(len(s))}
		}
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(out))
	}))
	defer srv.Close()

	inputs := make([]string, 70)
	for i := range inputs {
		// Distinct length per input so we can verify ordering across chunks.
		inputs[i] = strings.Repeat("x", i+1)
	}

	c := New(srv.URL)
	vecs, err := c.Embed(context.Background(), inputs)
	require.NoError(t, err)
	require.Len(t, vecs, 70)
	require.Equal(t, []int{32, 32, 6}, batchSizes)
	for i, v := range vecs {
		require.Equal(t, []float32{float32(i + 1)}, v, "vector %d out of order", i)
	}
}

func TestEmbed_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"oops"}`))
	}))
	defer srv.Close()
	c := New(srv.URL)
	c.baseDelay = 0 // 500 is retryable now; don't actually sleep between attempts
	_, err := c.Embed(context.Background(), []string{"x"})
	require.Error(t, err)
}

// A Spot reclaim makes TEI briefly unreachable (5xx / connection refused). The
// client should ride through a short blip by retrying, not fail the whole embed.
func TestEmbed_RetriesTransient5xx(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[[0.1, 0.2]]`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	c.baseDelay = 0 // no real waiting in tests
	vecs, err := c.Embed(context.Background(), []string{"x"})
	require.NoError(t, err)
	require.Len(t, vecs, 1)
	require.Equal(t, int32(3), calls.Load(), "should retry twice then succeed")
}

// A 4xx is a client error (bad input) — retrying can't fix it, so don't waste
// attempts on it.
func TestEmbed_DoesNotRetry4xx(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"bad input"}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	c.baseDelay = 0
	_, err := c.Embed(context.Background(), []string{"x"})
	require.Error(t, err)
	require.Equal(t, int32(1), calls.Load(), "4xx must not be retried")
}

func TestEmbed_RetriesExhausted_ReturnsError(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	c := New(srv.URL)
	c.baseDelay = 0
	c.maxAttempts = 3
	_, err := c.Embed(context.Background(), []string{"x"})
	require.Error(t, err)
	require.Equal(t, int32(3), calls.Load(), "should try exactly maxAttempts times")
}

// The backoff wait must observe context cancellation instead of blocking for the
// full delay — so a caller whose deadline passes (or the process shutting down)
// bails promptly rather than hanging through the retry schedule.
func TestEmbed_ContextCanceled_StopsRetryingPromptly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	c := New(srv.URL)
	c.baseDelay = 10 * time.Second // a real sleep would dominate; ctx must short-circuit it
	c.maxAttempts = 5
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	_, err := c.Embed(ctx, []string{"x"})
	require.Error(t, err)
	require.Less(t, time.Since(start), 2*time.Second, "canceled ctx must abort backoff, not sleep")
}
