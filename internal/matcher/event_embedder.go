package matcher

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/pgvector/pgvector-go"

	"github.com/wmyers/heres-whats-happening/internal/store"
)

// Embedder is the minimal interface the matcher needs from the TEI client.
// Mockable in tests.
type Embedder interface {
	Embed(ctx context.Context, inputs []string) ([][]float32, error)
}

// EventEmbedder embeds events whose embedding column is NULL.
type EventEmbedder struct {
	q   *store.Queries
	emb Embedder
}

func NewEventEmbedder(q *store.Queries, emb Embedder) *EventEmbedder {
	return &EventEmbedder{q: q, emb: emb}
}

// Run finds events that need an embedding, batches them through the embedder,
// and writes the vectors back.
func (e *EventEmbedder) Run(ctx context.Context) error {
	rows, err := e.q.SelectEventsNeedingEmbedding(ctx)
	if err != nil {
		return fmt.Errorf("select events: %w", err)
	}
	if len(rows) == 0 {
		return nil
	}

	eventIDs := make([]pgtype.UUID, 0, len(rows))
	for _, r := range rows {
		eventIDs = append(eventIDs, r.ID)
	}
	performers, err := e.q.ListEventPerformersBatch(ctx, eventIDs)
	if err != nil {
		return fmt.Errorf("list performers: %w", err)
	}
	genres, err := e.q.ListEventGenresBatch(ctx, eventIDs)
	if err != nil {
		return fmt.Errorf("list genres: %w", err)
	}

	performerByEvent := make(map[pgtype.UUID][]string)
	for _, p := range performers {
		performerByEvent[p.EventID] = append(performerByEvent[p.EventID], p.PerformerName)
	}
	genreByEvent := make(map[pgtype.UUID][]string)
	for _, g := range genres {
		genreByEvent[g.EventID] = append(genreByEvent[g.EventID], g.GenreSlug)
	}

	texts := make([]string, len(rows))
	for i, r := range rows {
		texts[i] = BuildEventText(EventText{
			Title:       r.Title,
			Performers:  performerByEvent[r.ID],
			Genres:      genreByEvent[r.ID],
			Description: r.Description,
		})
	}

	vectors, err := e.emb.Embed(ctx, texts)
	if err != nil {
		return fmt.Errorf("embed: %w", err)
	}
	if len(vectors) != len(rows) {
		return fmt.Errorf("embedder returned %d vectors for %d events", len(vectors), len(rows))
	}

	for i, r := range rows {
		v := pgvector.NewVector(vectors[i])
		if err := e.q.UpdateEventEmbedding(ctx, store.UpdateEventEmbeddingParams{
			ID:        r.ID,
			Embedding: &v,
		}); err != nil {
			return fmt.Errorf("update event: %w", err)
		}
	}
	return nil
}
