package handlers

import (
	"context"
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

		results := applySemanticLeg(ctx, deps,
			pgtype.UUID{Bytes: cityUUID, Valid: true}, query, rows)

		writeJSON(w, http.StatusOK, searchResponse{Results: results})
	}
}

// lexicalResult and semanticResult render one row of each leg into the wire
// shape. The two legs project identical columns on purpose (see the note on
// SearchEventsSemantic in search.sql), but sqlc emits a distinct row struct per
// query, so the mapping is written once per struct rather than shared. They
// must stay in step: a fused id can be rendered from either one, and a
// difference between them would show up as the same event looking different
// depending on which leg found it.
func lexicalResult(row store.SearchEventsRow) searchResult {
	return searchResult{
		ID:       uuid.UUID(row.ID.Bytes).String(),
		Title:    row.Title,
		StartsAt: row.StartsAt.Time.UTC().Format(time.RFC3339),
		ImageURL: textPtrToString(row.ImageUrl),
		Segment:  textPtrToString(row.Segment),
		Venue:    searchVenue{Name: row.VenueName},
	}
}

func semanticResult(row store.SearchEventsSemanticRow) searchResult {
	return searchResult{
		ID:       uuid.UUID(row.ID.Bytes).String(),
		Title:    row.Title,
		StartsAt: row.StartsAt.Time.UTC().Format(time.RFC3339),
		ImageURL: textPtrToString(row.ImageUrl),
		Segment:  textPtrToString(row.Segment),
		Venue:    searchVenue{Name: row.VenueName},
	}
}

// applySemanticLeg produces the final, ordered results. With the flag off it
// is just the lexical rows, truncated. With it on it fuses the lexical ranking
// with a pgvector ranking of the same query.
//
// Every failure path returns the lexical results unchanged: a TEI outage or a
// bad embedding degrades search to lexical-only rather than failing the
// request, which is what makes the flag safe to flip against a service running
// on Fargate Spot.
func applySemanticLeg(
	ctx context.Context,
	deps SearchDeps,
	cityID pgtype.UUID,
	query string,
	rows []store.SearchEventsRow,
) []searchResult {
	// byID carries display data for every id either leg can produce. It is
	// seeded from the lexical leg and topped up from the semantic one below --
	// see the note there for why both legs have to contribute.
	byID := make(map[uuid.UUID]searchResult, len(rows))
	lexical := make([]uuid.UUID, 0, len(rows))
	for _, row := range rows {
		id := uuid.UUID(row.ID.Bytes)
		byID[id] = lexicalResult(row)
		lexical = append(lexical, id)
	}

	if !deps.SemanticEnabled || deps.Embedder == nil {
		return resultsFor(truncateIDs(lexical, searchResultLimit), byID)
	}

	vecs, err := deps.Embedder.Embed(ctx, []string{query})
	if err != nil || len(vecs) == 0 || len(vecs[0]) == 0 {
		log.Printf("search: semantic leg unavailable, serving lexical only: err=%v vectors=%d",
			err, len(vecs))
		return resultsFor(truncateIDs(lexical, searchResultLimit), byID)
	}

	vec := pgvector.NewVector(vecs[0])
	semanticRows, err := deps.Queries.SearchEventsSemantic(ctx, store.SearchEventsSemanticParams{
		CityID:         cityID,
		QueryEmbedding: &vec,
		CandidateLimit: searchCandidateLimit,
	})
	if err != nil {
		log.Printf("search: semantic query failed, serving lexical only: %v", err)
		return resultsFor(truncateIDs(lexical, searchResultLimit), byID)
	}

	semantic := make([]uuid.UUID, 0, len(semanticRows))
	for _, row := range semanticRows {
		id := uuid.UUID(row.ID.Bytes)
		semantic = append(semantic, id)
		// The reason the semantic query projects display columns at all. RRF
		// fuses ORDERINGS, so a fused id may exist only in this leg -- the
		// "baseball" case, where no event contains the string and every hit is
		// semantic. Rendering the fused list out of the lexical rows alone
		// dropped exactly those ids, which made the flag a no-op for the
		// queries the leg was built for and a visible drop in result count for
		// the rest.
		//
		// Lexical wins the collision only because it was inserted first; the
		// two legs project the same columns, so the rendered row is identical
		// either way.
		if _, ok := byID[id]; !ok {
			byID[id] = semanticResult(row)
		}
	}

	return resultsFor(fuseRRF(lexical, semantic, searchResultLimit), byID)
}

// resultsFor renders an ordered id list. The slice is non-nil so an empty
// result encodes as [] rather than null.
func resultsFor(ids []uuid.UUID, byID map[uuid.UUID]searchResult) []searchResult {
	out := make([]searchResult, 0, len(ids))
	for _, id := range ids {
		if r, ok := byID[id]; ok {
			out = append(out, r)
		}
	}
	return out
}

func truncateIDs(ids []uuid.UUID, limit int) []uuid.UUID {
	if len(ids) > limit {
		return ids[:limit]
	}
	return ids
}
