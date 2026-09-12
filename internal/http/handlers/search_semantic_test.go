package handlers_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/http/handlers"
	"github.com/wmyers/heres-whats-happening/internal/store"
	"github.com/wmyers/heres-whats-happening/internal/testdb"
)

type stubEmbedder struct {
	calls int
	err   error
	vec   []float32
}

func (s *stubEmbedder) Embed(_ context.Context, inputs []string) ([][]float32, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	out := make([][]float32, len(inputs))
	for i := range out {
		out[i] = s.vec
	}
	return out, nil
}

func semanticRouter(deps handlers.SearchDeps) http.Handler {
	r := chi.NewRouter()
	r.Get("/search/{cityId}/events", handlers.SearchEvents(deps))
	return r
}

func zeroVec() []float32 { return make([]float32, 384) }

// Flag off is the default: TEI must not be touched at all.
func TestSearch_FlagOff_DoesNotCallEmbedder(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()
	cityID := seedSearchHandlerFixture(t, q, ctx)

	emb := &stubEmbedder{vec: zeroVec()}
	rec := doSearch(t, semanticRouter(handlers.SearchDeps{
		Queries: q, Embedder: emb, SemanticEnabled: false,
	}), cityID, "midnight")

	require.Equal(t, http.StatusOK, rec.Code)
	require.Zero(t, emb.calls)
}

func TestSearch_FlagOn_CallsEmbedder(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()
	cityID := seedSearchHandlerFixture(t, q, ctx)

	emb := &stubEmbedder{vec: zeroVec()}
	rec := doSearch(t, semanticRouter(handlers.SearchDeps{
		Queries: q, Embedder: emb, SemanticEnabled: true,
	}), cityID, "midnight")

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, 1, emb.calls)
}

// The property that makes the flag safe to flip against a Spot service.
func TestSearch_FlagOn_TEIFailureDegradesToLexical(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()
	cityID := seedSearchHandlerFixture(t, q, ctx)

	emb := &stubEmbedder{err: errors.New("connection refused")}
	rec := doSearch(t, semanticRouter(handlers.SearchDeps{
		Queries: q, Embedder: emb, SemanticEnabled: true,
	}), cityID, "midnight")

	require.Equal(t, http.StatusOK, rec.Code, "a TEI outage must never fail the request")
	require.Contains(t, rec.Body.String(), "Midnight Orchard")
}

// An embedder that returns no vectors is a contract violation, not an outage --
// it must degrade the same way rather than panic on an empty slice.
func TestSearch_FlagOn_EmptyEmbeddingDegradesToLexical(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()
	cityID := seedSearchHandlerFixture(t, q, ctx)

	emb := &stubEmbedder{vec: nil}
	rec := doSearch(t, semanticRouter(handlers.SearchDeps{
		Queries: q, Embedder: emb, SemanticEnabled: true,
	}), cityID, "midnight")

	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "Midnight Orchard")
}
