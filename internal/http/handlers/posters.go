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
// 202 immediately. The natural key (performer, venue, date) is hashed into a
// job id via poster.JobID, so a later GET without any id supplied by the
// client can look the same job back up.
func CreatePoster(d PosterDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in posterRequest
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			httperr.Write(w, http.StatusBadRequest, "invalid_body", "request body is not valid JSON")
			return
		}
		if in.Performer == "" || in.Venue == "" || in.Date == "" {
			httperr.Write(w, http.StatusBadRequest, "invalid_body", "performer, venue and date are required")
			return
		}

		id := poster.JobID(in.Performer, in.Venue, in.Date)
		_, err := d.Queries.ClaimPosterJob(r.Context(), store.ClaimPosterJobParams{
			ID: id, Performer: in.Performer, Venue: in.Venue, Date: in.Date,
			StaleBefore: pgtype.Timestamptz{Time: time.Now().Add(-stalePendingAfter), Valid: true},
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
		if _, err := poster.ValidateKey(res.SvgKey); err != nil {
			slog.Error("poster returned an unexpected key", "job", id, "error", err)
			_ = d.Queries.MarkPosterJobFailed(ctx, store.MarkPosterJobFailedParams{
				ID: id, FailureStage: ptr("svg"), FailureReason: ptr("poster service returned an unexpected artifact"),
			})
			return
		}
		_ = d.Queries.MarkPosterJobReady(ctx, store.MarkPosterJobReadyParams{
			ID: id, SvgKey: &res.SvgKey, PngKey: &res.PngKey,
			Artist: res.Artist, Credit: res.Credit,
		})
	}()
}

// GetPoster looks up the job for (performer, venue, date) — read from the
// query string, since the client never learns the id — and reports its
// status. A ready job's artifacts are presigned fresh on every call: the
// stored value is an S3 object key, not a URL, and presigned URLs expire.
func GetPoster(d PosterDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		performer := r.URL.Query().Get("performer")
		venue := r.URL.Query().Get("venue")
		date := r.URL.Query().Get("date")
		if performer == "" || venue == "" || date == "" {
			httperr.Write(w, http.StatusBadRequest, "invalid_query", "performer, venue and date are required")
			return
		}

		id := poster.JobID(performer, venue, date)
		job, err := d.Queries.GetPosterJob(r.Context(), id)
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
			svgURL, err := d.Presigner.PresignGet(r.Context(), strVal(job.SvgKey))
			if err != nil {
				httperr.WriteErr(w, r, http.StatusInternalServerError, "poster_presign_failed", "could not presign poster artifact", err)
				return
			}
			pngURL, err := d.Presigner.PresignGet(r.Context(), strVal(job.PngKey))
			if err != nil {
				httperr.WriteErr(w, r, http.StatusInternalServerError, "poster_presign_failed", "could not presign poster artifact", err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"status": "ready",
				"svgUrl": svgURL,
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
// svg_key/png_key on a ready row, which are set together with status and so
// are never nil by the time this runs — but the column type is nullable.
func strVal(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
