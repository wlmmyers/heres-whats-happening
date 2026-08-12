package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wmyers/heres-whats-happening/internal/http/httperr"
	"github.com/wmyers/heres-whats-happening/internal/http/middleware"
	"github.com/wmyers/heres-whats-happening/internal/poster"
	"github.com/wmyers/heres-whats-happening/internal/store"
)

// stalePendingAfter is how long a pending row may sit before another POST may
// re-claim it. The Lambda's own cap is 300s; past this the goroutine that owned
// the job is gone (task restart) and nothing else will ever clear the row.
const stalePendingAfter = 6 * time.Minute

type PosterDeps struct {
	Queries   *store.Queries
	Generator poster.Generator
	Presigner poster.Presigner
}

type posterRequest struct {
	Performer string `json:"performer"`
	Venue     string `json:"venue"`
	Date      string `json:"date"`
	Force     bool   `json:"force"`
}

// CreatePoster claims (or joins) an async poster-generation job and returns
// 202 immediately. The natural key (user, performer, venue, date) is hashed
// into a job id via poster.JobID, so a later GET without any id supplied by
// the client can look the same job back up.
//
// Jobs are scoped to the authenticated user: force:true re-claims a ready row,
// so a shared row would let any confirmed user blank any other user's poster.
// The accepted cost is that two users wanting the same show each generate
// their own copy.
func CreatePoster(d PosterDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// The route lives inside the authenticated + confirmed group, so this
		// is always present — checked rather than assumed, because the failure
		// mode of a wrong answer here is one user's row keyed to the zero uuid.
		uid, ok := middleware.UserIDFromContext(r.Context())
		if !ok {
			httperr.Write(w, http.StatusUnauthorized, "no_user", "user not in context")
			return
		}

		var in posterRequest
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			httperr.Write(w, http.StatusBadRequest, "invalid_body", "request body is not valid JSON")
			return
		}
		if in.Performer == "" || in.Venue == "" || in.Date == "" {
			httperr.Write(w, http.StatusBadRequest, "invalid_body", "performer, venue and date are required")
			return
		}

		id := poster.JobID(uid.String(), in.Performer, in.Venue, in.Date)
		_, err := d.Queries.ClaimPosterJob(r.Context(), store.ClaimPosterJobParams{
			ID: id, UserID: pgtype.UUID{Bytes: uid, Valid: true},
			Performer: in.Performer, Venue: in.Venue, Date: in.Date,
			StaleBefore: pgtype.Timestamptz{Time: time.Now().Add(-stalePendingAfter), Valid: true},
			Force:       in.Force,
		})
		switch {
		case err == nil:
			// We won the claim: this request owns the generation.
			startGeneration(d, id, poster.Request{
				Performer: in.Performer, Venue: in.Venue, Date: in.Date, Force: in.Force,
			})
		case errors.Is(err, pgx.ErrNoRows):
			// Someone else already has it in flight, or it is already ready.
			// Either way this caller just polls.
		default:
			httperr.WriteErr(w, r, http.StatusInternalServerError, "poster_claim_failed", "could not queue poster generation", err)
			return
		}

		writeJSON(w, http.StatusAccepted, map[string]any{"status": "pending"})
	}
}

// startGeneration runs the job off the request goroutine.
//
// context.Background() is deliberate and load-bearing: the request context is
// cancelled as soon as the 202 is written, so inheriting it would kill the
// generation immediately. The timeout here is the only bound.
func startGeneration(d PosterDeps, id string, req poster.Request) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
		defer cancel()

		res, err := d.Generator.Generate(ctx, req)
		if err != nil {
			slog.Error("poster generation failed", "job", id, "error", err)
			// The upstream detail is logged, never returned to the client.
			_ = d.Queries.MarkPosterJobFailed(ctx, store.MarkPosterJobFailedParams{
				ID: id, FailureStage: ptr("svg"), FailureReason: ptr("poster service unavailable"),
			})
			return
		}
		if res.FailureStage != "" {
			_ = d.Queries.MarkPosterJobFailed(ctx, store.MarkPosterJobFailedParams{
				ID: id, FailureStage: &res.FailureStage, FailureReason: &res.FailureReason,
			})
			return
		}
		// The key is checked before the row goes ready. GetPoster presigns it,
		// and ValidateKey is what PresignGet itself enforces — so a ready row
		// holding a bad png_key makes every later GET fail the presign and
		// return 500, permanently: a ready row is neither failed nor
		// stale-pending, so nothing ever re-claims it and there is no self-heal.
		// Failing the job here instead leaves a state the client can act on and
		// a POST can legitimately retry. The stage is "svg", matching the
		// Lambda's own stage vocabulary ("image" | "svg") — the png is a render
		// of the source artwork, not a stage of its own.
		if _, err := poster.ValidateKey(res.PngKey); err != nil {
			slog.Error("poster returned an unexpected key", "job", id, "error", err)
			_ = d.Queries.MarkPosterJobFailed(ctx, store.MarkPosterJobFailedParams{
				ID: id, FailureStage: ptr("svg"), FailureReason: ptr("poster service returned an unexpected artifact"),
			})
			return
		}
		_ = d.Queries.MarkPosterJobReady(ctx, store.MarkPosterJobReadyParams{
			ID: id, PngKey: &res.PngKey,
			Artist: res.Artist, Credit: res.Credit,
		})
	}()
}

// GetPoster looks up the caller's own job for (performer, venue, date) — read
// from the query string, since the client never learns the id — and reports
// its status. A ready job's artifacts are presigned fresh on every call: the
// stored value is an S3 object key, not a URL, and presigned URLs expire.
//
// The three values are read through r.URL.Query(), i.e. percent-decoded and
// with "+" decoded to a space. Those decoded strings are the canonical form —
// the same strings POST reads out of its JSON body — so a client wanting a
// literal "+" in a name must percent-encode it as "%2B". Anything built with
// url.Values.Encode() gets this right for free.
func GetPoster(d PosterDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid, ok := middleware.UserIDFromContext(r.Context())
		if !ok {
			httperr.Write(w, http.StatusUnauthorized, "no_user", "user not in context")
			return
		}

		performer := r.URL.Query().Get("performer")
		venue := r.URL.Query().Get("venue")
		date := r.URL.Query().Get("date")
		if performer == "" || venue == "" || date == "" {
			httperr.Write(w, http.StatusBadRequest, "invalid_query", "performer, venue and date are required")
			return
		}

		id := poster.JobID(uid.String(), performer, venue, date)
		job, err := d.Queries.GetPosterJob(r.Context(), store.GetPosterJobParams{
			ID: id, UserID: pgtype.UUID{Bytes: uid, Valid: true},
		})
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			writeJSON(w, http.StatusNotFound, map[string]any{"status": "unknown"})
			return
		case err != nil:
			httperr.WriteErr(w, r, http.StatusInternalServerError, "poster_lookup_failed", "could not look up poster job", err)
			return
		}

		switch job.Status {
		case "pending":
			writeJSON(w, http.StatusAccepted, map[string]any{"status": "pending"})
		case "failed":
			writeJSON(w, http.StatusOK, map[string]any{
				"status":         "failed",
				"failure_stage":  job.FailureStage,
				"failure_reason": job.FailureReason,
			})
		case "ready":
			pngURL, err := d.Presigner.PresignGet(r.Context(), strVal(job.PngKey))
			if err != nil {
				httperr.WriteErr(w, r, http.StatusInternalServerError, "poster_presign_failed", "could not presign poster artifact", err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"status": "ready",
				"pngUrl": pngURL,
				"artist": json.RawMessage(job.Artist),
				"credit": json.RawMessage(job.Credit),
			})
		default:
			httperr.WriteErr(w, r, http.StatusInternalServerError, "poster_bad_status", "poster job has an unrecognized status", errors.New(job.Status))
		}
	}
}

func ptr[T any](v T) *T { return &v }

// strVal dereferences a possibly-nil *string, returning "" for nil. Used for
// png_key on a ready row, which is set together with status and so is never
// nil by the time this runs — but the column type is nullable.
func strVal(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
