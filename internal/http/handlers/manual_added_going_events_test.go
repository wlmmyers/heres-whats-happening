package handlers_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/auth"
	"github.com/wmyers/heres-whats-happening/internal/http/handlers"
	"github.com/wmyers/heres-whats-happening/internal/http/middleware"
	"github.com/wmyers/heres-whats-happening/internal/store"
	"github.com/wmyers/heres-whats-happening/internal/testdb"
)

type manualEventOut struct {
	ID        string `json:"id"`
	Date      string `json:"date"`
	EventName string `json:"event_name"`
	VenueName string `json:"venue_name"`
}

// manualEventsFixture spins up the pieces every test in this file needs: a
// pool, a signer, and one confirmed user's access token.
type manualEventsFixture struct {
	q      *store.Queries
	signer *auth.JWTSigner
	cityID string
	access string
}

func newManualEventsFixture(t *testing.T, email string) *manualEventsFixture {
	t.Helper()
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	cityID := defaultCityID(t, q)
	return &manualEventsFixture{
		q:      q,
		signer: signer,
		cityID: cityID,
		access: signupAndAccess(t, q, signer, cityID, email),
	}
}

func (f *manualEventsFixture) newUser(t *testing.T, email string) string {
	t.Helper()
	return signupAndAccess(t, f.q, f.signer, f.cityID, email)
}

func (f *manualEventsFixture) create(t *testing.T, access string, body map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/me/manual-added-going-events", bytes.NewReader(raw))
	req.Header.Set("Authorization", "Bearer "+access)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	middleware.RequireAuth(f.signer)(handlers.CreateManualAddedGoingEvent(f.q)).ServeHTTP(rec, req)
	return rec
}

func (f *manualEventsFixture) list(t *testing.T, access string) []manualEventOut {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/me/manual-added-going-events", nil)
	req.Header.Set("Authorization", "Bearer "+access)
	rec := httptest.NewRecorder()
	middleware.RequireAuth(f.signer)(handlers.ListManualAddedGoingEvents(f.q)).ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var out struct {
		Events []manualEventOut `json:"events"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&out))
	return out.Events
}

// router mounts the two id-bearing routes so chi populates the {id} URL param.
func (f *manualEventsFixture) router() chi.Router {
	r := chi.NewRouter()
	mw := middleware.RequireAuth(f.signer)
	r.With(mw).Put("/me/manual-added-going-events/{id}", handlers.UpdateManualAddedGoingEvent(f.q))
	r.With(mw).Delete("/me/manual-added-going-events/{id}", handlers.DeleteManualAddedGoingEvent(f.q))
	return r
}

func TestPostManualAddedGoingEvent_CreatesAndEchoesTheDayEntered(t *testing.T) {
	f := newManualEventsFixture(t, "man1@example.com")

	rec := f.create(t, f.access, map[string]string{
		"date":       "2026-03-10",
		"event_name": "Built to Spill",
		"venue_name": "The Chapel",
	})

	require.Equal(t, http.StatusCreated, rec.Code)
	var out manualEventOut
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&out))
	require.NotEmpty(t, out.ID)
	// The day the user typed comes back unchanged. A DATE rendered through a
	// timestamp would slip to the 9th or 11th depending on the server's zone.
	require.Equal(t, "2026-03-10", out.Date)
	require.Equal(t, "Built to Spill", out.EventName)
	require.Equal(t, "The Chapel", out.VenueName)
}

func TestPostManualAddedGoingEvent_TrimsSurroundingWhitespace(t *testing.T) {
	f := newManualEventsFixture(t, "man2@example.com")

	rec := f.create(t, f.access, map[string]string{
		"date":       "2026-03-10",
		"event_name": "  Built to Spill  ",
		"venue_name": "  The Chapel  ",
	})

	require.Equal(t, http.StatusCreated, rec.Code)
	var out manualEventOut
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&out))
	require.Equal(t, "Built to Spill", out.EventName)
	require.Equal(t, "The Chapel", out.VenueName)
}

func TestPostManualAddedGoingEvent_RejectsIncompleteEntries(t *testing.T) {
	f := newManualEventsFixture(t, "man3@example.com")

	cases := map[string]map[string]string{
		"missing date":    {"date": "", "event_name": "Show", "venue_name": "Venue"},
		"unparseable day": {"date": "2026-13-45", "event_name": "Show", "venue_name": "Venue"},
		"not a date":      {"date": "sometime last spring", "event_name": "Show", "venue_name": "Venue"},
		"timestamp":       {"date": "2026-03-10T20:00:00Z", "event_name": "Show", "venue_name": "Venue"},
		"blank name":      {"date": "2026-03-10", "event_name": "   ", "venue_name": "Venue"},
		"blank venue":     {"date": "2026-03-10", "event_name": "Show", "venue_name": "   "},
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, http.StatusBadRequest, f.create(t, f.access, body).Code)
		})
	}
	require.Empty(t, f.list(t, f.access))
}

func TestGetManualAddedGoingEvents_ReturnsOnlyOwn(t *testing.T) {
	f := newManualEventsFixture(t, "man4a@example.com")
	other := f.newUser(t, "man4b@example.com")

	require.Equal(t, http.StatusCreated, f.create(t, f.access, map[string]string{
		"date": "2026-03-10", "event_name": "Mine", "venue_name": "The Chapel",
	}).Code)
	require.Equal(t, http.StatusCreated, f.create(t, other, map[string]string{
		"date": "2026-03-11", "event_name": "Theirs", "venue_name": "The Bowl",
	}).Code)

	mine := f.list(t, f.access)
	require.Len(t, mine, 1)
	require.Equal(t, "Mine", mine[0].EventName)
}

func TestPutManualAddedGoingEvent_UpdatesOwnRow(t *testing.T) {
	f := newManualEventsFixture(t, "man5@example.com")
	rec := f.create(t, f.access, map[string]string{
		"date": "2026-03-10", "event_name": "Wrong Name", "venue_name": "Wrong Venue",
	})
	require.Equal(t, http.StatusCreated, rec.Code)
	var created manualEventOut
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&created))

	body, _ := json.Marshal(map[string]string{
		"date": "2026-04-02", "event_name": "Right Name", "venue_name": "Right Venue",
	})
	req := httptest.NewRequest(http.MethodPut, "/me/manual-added-going-events/"+created.ID, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+f.access)
	req.Header.Set("Content-Type", "application/json")
	rec2 := httptest.NewRecorder()
	f.router().ServeHTTP(rec2, req)

	require.Equal(t, http.StatusOK, rec2.Code)
	var out manualEventOut
	require.NoError(t, json.NewDecoder(rec2.Body).Decode(&out))
	require.Equal(t, created.ID, out.ID)
	require.Equal(t, "2026-04-02", out.Date)
	require.Equal(t, "Right Name", out.EventName)
	require.Equal(t, "Right Venue", out.VenueName)
}

func TestPutManualAddedGoingEvent_AnotherUsersRowIs404(t *testing.T) {
	f := newManualEventsFixture(t, "man6a@example.com")
	thief := f.newUser(t, "man6b@example.com")

	rec := f.create(t, f.access, map[string]string{
		"date": "2026-03-10", "event_name": "Untouched", "venue_name": "The Chapel",
	})
	require.Equal(t, http.StatusCreated, rec.Code)
	var created manualEventOut
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&created))

	body, _ := json.Marshal(map[string]string{
		"date": "1999-01-01", "event_name": "Vandalised", "venue_name": "Nowhere",
	})
	req := httptest.NewRequest(http.MethodPut, "/me/manual-added-going-events/"+created.ID, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+thief)
	req.Header.Set("Content-Type", "application/json")
	rec2 := httptest.NewRecorder()
	f.router().ServeHTTP(rec2, req)

	require.Equal(t, http.StatusNotFound, rec2.Code)
	mine := f.list(t, f.access)
	require.Len(t, mine, 1)
	require.Equal(t, "Untouched", mine[0].EventName)
	require.Equal(t, "2026-03-10", mine[0].Date)
}

func TestDeleteManualAddedGoingEvent_RemovesOwnRow(t *testing.T) {
	f := newManualEventsFixture(t, "man7@example.com")
	rec := f.create(t, f.access, map[string]string{
		"date": "2026-03-10", "event_name": "Doomed", "venue_name": "The Chapel",
	})
	require.Equal(t, http.StatusCreated, rec.Code)
	var created manualEventOut
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&created))

	req := httptest.NewRequest(http.MethodDelete, "/me/manual-added-going-events/"+created.ID, nil)
	req.Header.Set("Authorization", "Bearer "+f.access)
	rec2 := httptest.NewRecorder()
	f.router().ServeHTTP(rec2, req)

	require.Equal(t, http.StatusNoContent, rec2.Code)
	require.Empty(t, f.list(t, f.access))
}

func TestDeleteManualAddedGoingEvent_OwnershipEnforced(t *testing.T) {
	f := newManualEventsFixture(t, "man8a@example.com")
	thief := f.newUser(t, "man8b@example.com")

	rec := f.create(t, f.access, map[string]string{
		"date": "2026-03-10", "event_name": "Survives", "venue_name": "The Chapel",
	})
	require.Equal(t, http.StatusCreated, rec.Code)
	var created manualEventOut
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&created))

	req := httptest.NewRequest(http.MethodDelete, "/me/manual-added-going-events/"+created.ID, nil)
	req.Header.Set("Authorization", "Bearer "+thief)
	rec2 := httptest.NewRecorder()
	f.router().ServeHTTP(rec2, req)

	// Idempotent like the manual-interests delete: the thief gets 204, but the
	// row is scoped by user_id so nothing was removed.
	require.Equal(t, http.StatusNoContent, rec2.Code)
	mine := f.list(t, f.access)
	require.Len(t, mine, 1)
	require.Equal(t, "Survives", mine[0].EventName)
}

func TestManualAddedGoingEvent_RejectsMalformedID(t *testing.T) {
	f := newManualEventsFixture(t, "man9@example.com")

	req := httptest.NewRequest(http.MethodDelete, "/me/manual-added-going-events/not-a-uuid", nil)
	req.Header.Set("Authorization", "Bearer "+f.access)
	rec := httptest.NewRecorder()
	f.router().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}
