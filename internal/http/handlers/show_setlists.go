package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wmyers/heres-whats-happening/internal/http/httperr"
	"github.com/wmyers/heres-whats-happening/internal/http/middleware"
	"github.com/wmyers/heres-whats-happening/internal/store"
)

// Pointer so an omitted field is distinguishable from an explicit `false`;
// silently opting a user back out on a malformed request would be worse than
// rejecting it.
type updateShowSetlistsRequest struct {
	ShowSetlists *bool `json:"show_setlists"`
}

// UpdateShowSetlists persists whether the caller wants setlists revealed on the
// event detail page. Purely presentational — it has no effect on matching, so
// unlike the match threshold there is nothing to re-score afterwards.
func UpdateShowSetlists(q *store.Queries) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid, ok := middleware.UserIDFromContext(r.Context())
		if !ok {
			httperr.Write(w, http.StatusUnauthorized, "no_user", "user not in context")
			return
		}
		var req updateShowSetlistsRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httperr.Write(w, http.StatusBadRequest, "bad_json", "request body is not valid JSON")
			return
		}
		if req.ShowSetlists == nil {
			httperr.Write(w, http.StatusBadRequest, "missing_field", "show_setlists is required")
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if err := q.UpdateUserShowSetlists(ctx, store.UpdateUserShowSetlistsParams{
			ID:           pgtype.UUID{Bytes: uid, Valid: true},
			ShowSetlists: *req.ShowSetlists,
		}); err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, "db_error", "could not update setlist visibility", err)
			return
		}

		w.WriteHeader(http.StatusNoContent)
	}
}
