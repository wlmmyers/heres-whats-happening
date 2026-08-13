package handlers

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wmyers/heres-whats-happening/internal/http/httperr"
	"github.com/wmyers/heres-whats-happening/internal/http/middleware"
	"github.com/wmyers/heres-whats-happening/internal/store"
)

type calendarEvent struct {
	ID             string          `json:"id"`
	Title          string          `json:"title"`
	Description    string          `json:"description,omitempty"`
	StartsAt       string          `json:"starts_at"`
	EndsAt         string          `json:"ends_at,omitempty"`
	ImageURL       string          `json:"image_url,omitempty"`
	URL            string          `json:"url,omitempty"`
	Venue          calendarVenue   `json:"venue"`
	Score          float64         `json:"score"`
	MatchedBecause calendarMatch   `json:"matched_because"`
	Artist         *calendarArtist `json:"artist,omitempty"`

	// Unexported, so encoding/json ignores it entirely — no tag needed. Carries
	// the row's headline_artist_id from the page query through to attachArtists.
	artistID pgtype.UUID
}

type calendarVenue struct {
	Name    string `json:"name"`
	Address string `json:"address,omitempty"`
}

type calendarMatch struct {
	Performers []string `json:"performers"`
	Genres     []string `json:"genres"`
}

type calendarResponse struct {
	Events     []calendarEvent `json:"events"`
	NextCursor string          `json:"next_cursor,omitempty"`
}

// calendarPageSize is the maximum number of events in one calendar response.
// Fixed server-side: there is no client-supplied limit.
const calendarPageSize = 20

// parseCursor reads the optional cursor query param into keyset params. Absent
// cursor yields two invalid pgtypes, which pgx sends as NULL — the query reads
// that as "first page". On bad input it writes the error response and returns
// ok=false.
func parseCursor(w http.ResponseWriter, r *http.Request) (startsAt pgtype.Timestamptz, eventID pgtype.UUID, ok bool) {
	raw := r.URL.Query().Get("cursor")
	if raw == "" {
		return pgtype.Timestamptz{}, pgtype.UUID{}, true
	}
	ts, id, err := decodeCursor(raw)
	if err != nil {
		httperr.Write(w, http.StatusBadRequest, "bad_cursor", "cursor is not valid")
		return pgtype.Timestamptz{}, pgtype.UUID{}, false
	}
	return pgtype.Timestamptz{Time: ts, Valid: true}, pgtype.UUID{Bytes: id, Valid: true}, true
}

// attachArtists batch-loads enrichment for a page of events and hangs it off
// each one, following the ListEventPerformersBatch pattern: one page query,
// then one round trip for the whole page rather than N.
//
// Deliberately does NOT fall image_url back to the artist photo: Wikimedia
// Commons images are predominantly CC-BY/CC-BY-SA, and attribution is a
// licence condition that the frontend does not render yet. Serving the photo
// only under artist.image, alongside its credit block, means any client that
// renders it has the attribution in hand. This also keeps today's image_url
// purely scraper-sourced.
func attachArtists(ctx context.Context, q *store.Queries, evs []calendarEvent, artistIDs []pgtype.UUID) {
	if len(artistIDs) == 0 {
		return
	}
	rows, err := q.GetArtistEnrichmentBatch(ctx, artistIDs)
	if err != nil {
		// Enrichment is decoration: a failure here must not fail the calendar.
		log.Printf("calendar: artist enrichment lookup: %v", err)
		return
	}
	byID := make(map[[16]byte]store.GetArtistEnrichmentBatchRow, len(rows))
	for _, r := range rows {
		byID[r.ArtistID.Bytes] = r
	}
	for i := range evs {
		if !evs[i].artistID.Valid {
			continue
		}
		row, ok := byID[evs[i].artistID.Bytes]
		if !ok {
			continue
		}
		a := buildArtist(row)
		evs[i].Artist = &a
	}
}

// GetMyCalendar returns one page of the authenticated user's matched events,
// ordered by start time, beginning at the optional cursor. At most
// calendarPageSize events come back; next_cursor is present only when more
// exist.
func GetMyCalendar(q *store.Queries) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid, ok := middleware.UserIDFromContext(r.Context())
		if !ok {
			httperr.Write(w, http.StatusUnauthorized, "no_user", "user not in context")
			return
		}
		cursorStartsAt, cursorEventID, ok := parseCursor(w, r)
		if !ok {
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		// One more than the page size: if it comes back, there is a next page.
		rows, err := q.GetUserCalendarPage(ctx, store.GetUserCalendarPageParams{
			UserID:         pgtype.UUID{Bytes: uid, Valid: true},
			CursorStartsAt: cursorStartsAt,
			CursorEventID:  cursorEventID,
			PageLimit:      calendarPageSize + 1,
		})
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, "db_error", "could not load calendar", err)
			return
		}

		out := calendarResponse{Events: make([]calendarEvent, 0, len(rows))}
		if len(rows) > calendarPageSize {
			rows = rows[:calendarPageSize]
			last := rows[len(rows)-1]
			out.NextCursor = encodeCursor(last.StartsAt.Time, last.EventID)
		}
		var artistIDs []pgtype.UUID
		for _, row := range rows {
			bd := parseBreakdown(row.ScoreBreakdown)
			ev := calendarEvent{
				ID:          uuidString(row.EventID),
				Title:       row.Title,
				Description: row.Description,
				Score:       row.Score,
				StartsAt:    row.StartsAt.Time.UTC().Format(time.RFC3339),
				Venue: calendarVenue{
					Name:    row.VenueName,
					Address: textPtrToString(row.VenueAddress),
				},
				MatchedBecause: bd,
				artistID:       row.HeadlineArtistID,
			}
			if row.EndsAt.Valid {
				ev.EndsAt = row.EndsAt.Time.UTC().Format(time.RFC3339)
			}
			ev.ImageURL = textPtrToString(row.ImageUrl)
			ev.URL = textPtrToString(row.Url)
			out.Events = append(out.Events, ev)
			if row.HeadlineArtistID.Valid {
				artistIDs = append(artistIDs, row.HeadlineArtistID)
			}
		}
		attachArtists(ctx, q, out.Events, artistIDs)
		writeJSON(w, http.StatusOK, out)
	}
}

// GetCityCalendar returns one page of every showable event in the given city —
// no match filtering, so events the caller has no user_event_match row for are
// included. This is what the calendar page falls back to when the user has no
// interests to match against, so the response is deliberately identical for
// every caller: no not-interested filtering, and score/matched_because are
// always the empty values. Paginated exactly like GetMyCalendar.
func GetCityCalendar(q *store.Queries) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cityUUID, err := uuid.Parse(chi.URLParam(r, "cityId"))
		if err != nil {
			httperr.Write(w, http.StatusBadRequest, "bad_city_id", "cityId is not a valid uuid")
			return
		}
		cursorStartsAt, cursorEventID, ok := parseCursor(w, r)
		if !ok {
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		rows, err := q.GetCityCalendarPage(ctx, store.GetCityCalendarPageParams{
			CityID:         pgtype.UUID{Bytes: cityUUID, Valid: true},
			CursorStartsAt: cursorStartsAt,
			CursorEventID:  cursorEventID,
			PageLimit:      calendarPageSize + 1,
		})
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, "db_error", "could not load city calendar", err)
			return
		}

		out := calendarResponse{Events: make([]calendarEvent, 0, len(rows))}
		if len(rows) > calendarPageSize {
			rows = rows[:calendarPageSize]
			last := rows[len(rows)-1]
			out.NextCursor = encodeCursor(last.StartsAt.Time, last.EventID)
		}
		var artistIDs []pgtype.UUID
		for _, row := range rows {
			ev := calendarEvent{
				ID:          uuidString(row.EventID),
				Title:       row.Title,
				Description: row.Description,
				StartsAt:    row.StartsAt.Time.UTC().Format(time.RFC3339),
				Venue: calendarVenue{
					Name:    row.VenueName,
					Address: textPtrToString(row.VenueAddress),
				},
				// No match exists for these events; parseBreakdown(nil) gives the
				// empty non-nil slices the FE expects.
				MatchedBecause: parseBreakdown(nil),
				artistID:       row.HeadlineArtistID,
			}
			if row.EndsAt.Valid {
				ev.EndsAt = row.EndsAt.Time.UTC().Format(time.RFC3339)
			}
			ev.ImageURL = textPtrToString(row.ImageUrl)
			ev.URL = textPtrToString(row.Url)
			out.Events = append(out.Events, ev)
			if row.HeadlineArtistID.Valid {
				artistIDs = append(artistIDs, row.HeadlineArtistID)
			}
		}
		attachArtists(ctx, q, out.Events, artistIDs)
		writeJSON(w, http.StatusOK, out)
	}
}

// parseBreakdown unmarshals a user_event_match.score_breakdown JSON blob
// into the matched_because shape. Empty input → empty (non-nil) slices.
func parseBreakdown(raw []byte) calendarMatch {
	bd := calendarMatch{Performers: []string{}, Genres: []string{}}
	if len(raw) == 0 {
		return bd
	}
	var in struct {
		Performers []string `json:"matched_performers"`
		Genres     []string `json:"matched_genres"`
	}
	_ = json.Unmarshal(raw, &in)
	if in.Performers != nil {
		bd.Performers = in.Performers
	}
	if in.Genres != nil {
		bd.Genres = in.Genres
	}
	return bd
}

func textPtrToString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func uuidString(u pgtype.UUID) string {
	const hex = "0123456789abcdef"
	out := make([]byte, 36)
	j := 0
	for i := 0; i < 16; i++ {
		b := u.Bytes[i]
		out[j] = hex[b>>4]
		out[j+1] = hex[b&0x0F]
		j += 2
		switch i {
		case 3, 5, 7, 9:
			out[j] = '-'
			j++
		}
	}
	return string(out)
}

// GetEventByIDForUser returns one event with the user's match info (or
// score=0 + empty matched_because if the user doesn't have a match for it).
func GetEventByIDForUser(q *store.Queries) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid, ok := middleware.UserIDFromContext(r.Context())
		if !ok {
			httperr.Write(w, http.StatusUnauthorized, "no_user", "user not in context")
			return
		}
		idStr := chi.URLParam(r, "id")
		eventUUID, err := uuid.Parse(idStr)
		if err != nil {
			httperr.Write(w, http.StatusBadRequest, "bad_id", "id is not a valid uuid")
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		row, err := q.GetMatchedEventForUser(ctx, store.GetMatchedEventForUserParams{
			ID:     pgtype.UUID{Bytes: eventUUID, Valid: true},
			UserID: pgtype.UUID{Bytes: uid, Valid: true},
		})
		if err != nil {
			httperr.Write(w, http.StatusNotFound, "not_found", "event not found")
			return
		}

		bd := parseBreakdown(row.ScoreBreakdown)
		var score float64
		if row.Score != nil {
			score = *row.Score
		}
		ev := calendarEvent{
			ID:          uuidString(row.EventID),
			Title:       row.Title,
			Description: row.Description,
			StartsAt:    row.StartsAt.Time.UTC().Format(time.RFC3339),
			Score:       score,
			Venue: calendarVenue{
				Name:    row.VenueName,
				Address: textPtrToString(row.VenueAddress),
			},
			MatchedBecause: bd,
			artistID:       row.HeadlineArtistID,
		}
		if row.EndsAt.Valid {
			ev.EndsAt = row.EndsAt.Time.UTC().Format(time.RFC3339)
		}
		ev.ImageURL = textPtrToString(row.ImageUrl)
		ev.URL = textPtrToString(row.Url)
		var ids []pgtype.UUID
		if row.HeadlineArtistID.Valid {
			ids = append(ids, row.HeadlineArtistID)
		}
		evs := []calendarEvent{ev}
		attachArtists(ctx, q, evs, ids)
		ev = evs[0]
		writeJSON(w, http.StatusOK, ev)
	}
}
