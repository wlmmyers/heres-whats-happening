package tei

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
