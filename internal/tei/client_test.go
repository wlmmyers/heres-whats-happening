package tei

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
	_, err := c.Embed(context.Background(), []string{"x"})
	require.Error(t, err)
}
