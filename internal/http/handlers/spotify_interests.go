package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wmyers/heres-whats-happening/internal/http/httperr"
	"github.com/wmyers/heres-whats-happening/internal/http/middleware"
	"github.com/wmyers/heres-whats-happening/internal/store"
)

// spotifyKinds is the set of Spotify-derived interest kinds. The order here is
// the display order of the response groups. Adding a fifth Spotify kind is a
// change to this slice plus spotifyKindLabels — the frontend renders whatever
// groups it is handed.
var spotifyKinds = []string{
	"spotify_top_artist",
	"spotify_top_track_artist",
	"spotify_saved_song_artist",
	"spotify_top_genre",
}

var spotifyKindLabels = map[string]string{
	"spotify_top_artist":        "Top artists",
	"spotify_top_track_artist":  "Artists from your top tracks",
	"spotify_saved_song_artist": "Artists from your saved songs",
	"spotify_top_genre":         "Top genres",
}

type spotifyInterestGroup struct {
	Kind      string        `json:"kind"`
	Label     string        `json:"label"`
	Interests []interestOut `json:"interests"`
}

type spotifyInterestsResponse struct {
	Groups []spotifyInterestGroup `json:"groups"`
}

// SpotifyInterests returns the caller's Spotify-derived interests grouped by
// kind. Read-only: these rows are owned by the Spotify scraper, so there is no
// create or delete counterpart. Groups with no rows are omitted entirely.
func SpotifyInterests(q *store.Queries) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid, ok := middleware.UserIDFromContext(r.Context())
		if !ok {
			httperr.Write(w, http.StatusUnauthorized, "no_user", "user not in context")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		rows, err := q.ListInterestsByUserAndKinds(ctx, store.ListInterestsByUserAndKindsParams{
			UserID: pgtype.UUID{Bytes: uid, Valid: true},
			Kinds:  spotifyKinds,
		})
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, "db_error", "could not list spotify interests", err)
			return
		}

		byKind := make(map[string][]interestOut, len(spotifyKinds))
		for _, row := range rows {
			byKind[row.Kind] = append(byKind[row.Kind], interestOut{
				ID:              uuid.UUID(row.ID.Bytes).String(),
				Value:           row.Value,
				NormalizedValue: row.NormalizedValue,
				Weight:          row.Weight,
				CreatedAt:       row.CreatedAt.Time.UTC().Format(time.RFC3339),
			})
		}

		groups := make([]spotifyInterestGroup, 0, len(spotifyKinds))
		for _, kind := range spotifyKinds {
			items := byKind[kind]
			if len(items) == 0 {
				continue
			}
			groups = append(groups, spotifyInterestGroup{
				Kind:      kind,
				Label:     spotifyKindLabels[kind],
				Interests: items,
			})
		}
		writeJSON(w, http.StatusOK, spotifyInterestsResponse{Groups: groups})
	}
}
