package handlers

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/pgvector/pgvector-go"

	"github.com/wmyers/heres-whats-happening/internal/http/httperr"
	"github.com/wmyers/heres-whats-happening/internal/store"
)

const (
	// Fixed server-side, like calendarPageSize: there is no client-supplied
	// limit. Typeahead does not paginate.
	searchResultLimit = 10

	// The measured cliff, not a style choice: below 3 runes the trigram index
	// cannot be used and the query degrades to a full scan with per-row
	// similarity() -- 43ms at 10k rows, growing linearly.
	searchMinRunes = 3

	// Truncate rather than reject: pasting a long string is a plausible user
	// action and the first 100 runes carry the signal.
	searchMaxRunes = 100

	// RRF over two top-10 lists is thin -- an event ranked 11th lexically and
	// 1st semantically could not surface at all. Both legs fetch wider and
	// fusion truncates to searchResultLimit.
	searchCandidateLimit = 50
)

type searchVenue struct {
	Name string `json:"name"`
}

// searchResult is deliberately leaner than calendarEvent: a row navigates to
// /events/:id, which fetches the full event itself.
//
// No rank field. It is an internal ordering signal, and publishing it invites
// clients to threshold on it -- see the design's Fusion note for why no score
// cutoff can work. The server owns relevance; the client renders an order.
type searchResult struct {
	ID       string      `json:"id"`
	Title    string      `json:"title"`
	StartsAt string      `json:"starts_at"`
	ImageURL string      `json:"image_url,omitempty"`
	Segment  string      `json:"segment,omitempty"`
	Venue    searchVenue `json:"venue"`
}

type searchResponse struct {
	Results []searchResult `json:"results"`
}

// parseSearchQuery reads and validates q. On bad input it writes the error
// response and returns ok=false.
func parseSearchQuery(w http.ResponseWriter, r *http.Request) (string, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get("q"))
	if raw == "" {
		httperr.Write(w, http.StatusBadRequest, "bad_query", "q is required")
		return "", false
	}
	runes := []rune(raw)
	if len(runes) < searchMinRunes {
		httperr.Write(w, http.StatusBadRequest, "query_too_short",
			"q must be at least 3 characters")
		return "", false
	}
	if len(runes) > searchMaxRunes {
		raw = string(runes[:searchMaxRunes])
	}
	return raw, true
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// SearchEmbedder is the subset of the TEI client search needs. Narrow on
// purpose: it makes the handler testable with a stub and nothing else.
type SearchEmbedder interface {
	Embed(ctx context.Context, inputs []string) ([][]float32, error)
}

type SearchDeps struct {
	Queries  *store.Queries
	Embedder SearchEmbedder
	// SemanticEnabled gates the pgvector leg. When false the Embedder is never
	// called and TEI is not in the request path at all.
	SemanticEnabled bool
}

// SearchEvents returns the top matching upcoming events in a city.
func SearchEvents(deps SearchDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cityUUID, err := uuid.Parse(chi.URLParam(r, "cityId"))
		if err != nil {
			httperr.Write(w, http.StatusBadRequest, "bad_city_id", "cityId is not a valid uuid")
			return
		}
		query, ok := parseSearchQuery(w, r)
		if !ok {
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		// Only widen past the top 10 when there is a second leg to fuse
		// against -- otherwise the lexical leg alone would fetch (and pay for)
		// 50 rows it will never use.
		limit := int32(searchResultLimit)
		if deps.SemanticEnabled {
			limit = searchCandidateLimit
		}

		rows, err := deps.Queries.SearchEvents(ctx, store.SearchEventsParams{
			Query:       query,
			CityID:      pgtype.UUID{Bytes: cityUUID, Valid: true},
			ResultLimit: limit,
		})
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, "db_error",
				"could not search events", err)
			return
		}

		rows = applySemanticLeg(ctx, deps, pgtype.UUID{Bytes: cityUUID, Valid: true}, query, rows)

		// Non-nil so an empty result encodes as [] rather than null.
		out := searchResponse{Results: make([]searchResult, 0, len(rows))}
		for _, row := range rows {
			out.Results = append(out.Results, searchResult{
				ID:       uuid.UUID(row.ID.Bytes).String(),
				Title:    row.Title,
				StartsAt: row.StartsAt.Time.UTC().Format(time.RFC3339),
				ImageURL: deref(row.ImageUrl),
				Segment:  deref(row.Segment),
				Venue:    searchVenue{Name: row.VenueName},
			})
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}

// applySemanticLeg reorders rows by fusing the lexical ranking with a pgvector
// ranking of the same query. Every failure path returns rows unchanged: a TEI
// outage or a bad embedding degrades search to lexical-only rather than
// failing the request, which is what makes the flag safe to flip against a
// service running on Fargate Spot.
func applySemanticLeg(
	ctx context.Context,
	deps SearchDeps,
	cityID pgtype.UUID,
	query string,
	rows []store.SearchEventsRow,
) []store.SearchEventsRow {
	if !deps.SemanticEnabled || deps.Embedder == nil {
		return truncateRows(rows, searchResultLimit)
	}

	vecs, err := deps.Embedder.Embed(ctx, []string{query})
	if err != nil || len(vecs) == 0 || len(vecs[0]) == 0 {
		log.Printf("search: semantic leg unavailable, serving lexical only: err=%v vectors=%d",
			err, len(vecs))
		return truncateRows(rows, searchResultLimit)
	}

	vec := pgvector.NewVector(vecs[0])
	semanticIDs, err := deps.Queries.SearchEventsSemantic(ctx, store.SearchEventsSemanticParams{
		CityID:         cityID,
		QueryEmbedding: &vec,
		CandidateLimit: searchCandidateLimit,
	})
	if err != nil {
		log.Printf("search: semantic query failed, serving lexical only: %v", err)
		return truncateRows(rows, searchResultLimit)
	}

	byID := make(map[uuid.UUID]store.SearchEventsRow, len(rows))
	lexical := make([]uuid.UUID, 0, len(rows))
	for _, row := range rows {
		id := uuid.UUID(row.ID.Bytes)
		byID[id] = row
		lexical = append(lexical, id)
	}
	semantic := make([]uuid.UUID, 0, len(semanticIDs))
	for _, pgID := range semanticIDs {
		semantic = append(semantic, uuid.UUID(pgID.Bytes))
	}

	fused := fuseRRF(lexical, semantic, searchResultLimit)
	out := make([]store.SearchEventsRow, 0, len(fused))
	for _, id := range fused {
		// Semantic-only hits have no lexical row to render; the lexical leg is
		// the source of display data, so they are dropped rather than
		// re-queried. Widening candidates to 50 keeps this rare.
		if row, ok := byID[id]; ok {
			out = append(out, row)
		}
	}
	return out
}

func truncateRows(rows []store.SearchEventsRow, limit int) []store.SearchEventsRow {
	if len(rows) > limit {
		return rows[:limit]
	}
	return rows
}
