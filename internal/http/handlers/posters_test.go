package handlers_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/http/handlers"
	"github.com/wmyers/heres-whats-happening/internal/poster"
	"github.com/wmyers/heres-whats-happening/internal/store"
	"github.com/wmyers/heres-whats-happening/internal/testdb"
)

// stubGenerator records calls and blocks until released, so a test can assert
// on what happens while generation is still in flight.
type stubGenerator struct {
	mu       sync.Mutex
	calls    int
	release  chan struct{}
	result   poster.Result
	err      error
	sawCtxOK chan bool
}

func (s *stubGenerator) Generate(ctx context.Context, _ poster.Request) (poster.Result, error) {
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()
	if s.release != nil {
		<-s.release
	}
	if s.sawCtxOK != nil {
		s.sawCtxOK <- ctx.Err() == nil
	}
	return s.result, s.err
}

func (s *stubGenerator) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// stubPresigner mints a distinct URL on every call — package-wide, since the
// counter is shared across every stubPresigner value — so a test can tell
// "presigned again" apart from "read from a cache".
type stubPresigner struct{}

var presignSeq int64

func (stubPresigner) PresignGet(_ context.Context, key string) (string, error) {
	n := atomic.AddInt64(&presignSeq, 1)
	return fmt.Sprintf("https://example-bucket.s3.amazonaws.com/%s?sig=%d", key, n), nil
}

// newPosterHandlerForTest wires CreatePoster against a real, truncated test
// database (via internal/testdb, matching how the rest of this package's
// handler tests obtain a *store.Queries) and the given stub generator.
func newPosterHandlerForTest(t *testing.T, gen poster.Generator) http.HandlerFunc {
	t.Helper()
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	return handlers.CreatePoster(handlers.PosterDeps{
		Queries:   q,
		Generator: gen,
		Presigner: stubPresigner{},
	})
}

// THE test that matters: the background goroutine must NOT inherit the request
// context, which is cancelled the moment the 202 is written. If it does,
// generation dies instantly and every job fails for no visible reason.
func TestCreatePosterGenerationSurvivesRequestCancellation(t *testing.T) {
	gen := &stubGenerator{release: make(chan struct{}), sawCtxOK: make(chan bool, 1)}
	h := newPosterHandlerForTest(t, gen)

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodPost, "/posters",
		strings.NewReader(`{"performer":"La Luz","venue":"Neumos","date":"2026-08-20"}`)).WithContext(ctx)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", rec.Code)
	}

	cancel() // exactly what the server does once the response is written
	close(gen.release)

	select {
	case alive := <-gen.sawCtxOK:
		if !alive {
			t.Fatal("generation saw a cancelled context: the goroutine inherited the request context")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("generation never ran")
	}
}

func TestCreatePosterDoesNotStartASecondGenerationForAPendingJob(t *testing.T) {
	gen := &stubGenerator{release: make(chan struct{})}
	h := newPosterHandlerForTest(t, gen)

	post := func() int {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/posters",
			strings.NewReader(`{"performer":"La Luz","venue":"Neumos","date":"2026-08-20"}`)))
		return rec.Code
	}

	if got := post(); got != http.StatusAccepted {
		t.Fatalf("first POST = %d, want 202", got)
	}
	if got := post(); got != http.StatusAccepted {
		t.Fatalf("second POST = %d, want 202", got)
	}
	close(gen.release)

	if n := gen.callCount(); n != 1 {
		t.Errorf("generation ran %d times, want 1 — the second POST must join the pending job", n)
	}
}

// seedReadyPosterJob claims and immediately completes a job for
// (performer, venue, date). Called twice for the same key it is idempotent:
// MarkPosterJobReady is an unconditional UPDATE by id, so re-running it just
// re-marks the same row ready — the second ClaimPosterJob's ErrNoRows (a
// ready row is neither failed nor stale-pending, so it isn't reclaimable) is
// expected and ignored.
func seedReadyPosterJob(t *testing.T, q *store.Queries, performer, venue, date string) string {
	t.Helper()
	ctx := context.Background()
	id := poster.JobID(performer, venue, date)
	_, _ = q.ClaimPosterJob(ctx, store.ClaimPosterJobParams{
		ID: id, Performer: performer, Venue: venue, Date: date,
		StaleBefore: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	svgKey := "posters/v1/la-luz/neumos-2026-08-20.svg"
	pngKey := "posters/v1/la-luz/neumos-2026-08-20.png"
	require.NoError(t, q.MarkPosterJobReady(ctx, store.MarkPosterJobReadyParams{
		ID: id, SvgKey: &svgKey, PngKey: &pngKey,
		Artist: json.RawMessage(`{"name":"La Luz"}`),
		Credit: json.RawMessage(`{"text":"Photo by Someone"}`),
	}))
	return id
}

// getPosterURLs seeds a ready job and returns its presigned svgUrl and pngUrl
// joined into one comparable string.
func getPosterURLs(t *testing.T, status string) string {
	t.Helper()
	if status != "ready" {
		t.Fatalf("getPosterURLs only supports status=ready, got %q", status)
	}

	pool := testdb.MustOpen(t)
	q := store.New(pool)
	performer, venue, date := "LaLuz", "Neumos", "2026-08-20"
	seedReadyPosterJob(t, q, performer, venue, date)

	deps := handlers.PosterDeps{Queries: q, Presigner: stubPresigner{}}
	req := httptest.NewRequest(http.MethodGet,
		"/posters?performer="+performer+"&venue="+venue+"&date="+date, nil)
	rec := httptest.NewRecorder()
	handlers.GetPoster(deps)(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	body := decodeBody(t, rec)
	svgURL, _ := body["svgUrl"].(string)
	pngURL, _ := body["pngUrl"].(string)
	return svgURL + "|" + pngURL
}

func TestGetPosterPresignsFreshOnEveryCall(t *testing.T) {
	// A ready job must presign at read time. Returning a stored URL would serve
	// a dead link once the 3600s expiry passed.
	first := getPosterURLs(t, "ready")
	second := getPosterURLs(t, "ready")
	if first == second {
		t.Error("two GETs returned identical urls; they must be signed per request")
	}
}

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&out); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	return out
}

func TestGetPoster_UnknownJobReturns404(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	deps := handlers.PosterDeps{Queries: q, Presigner: stubPresigner{}}

	req := httptest.NewRequest(http.MethodGet, "/posters?performer=Nobody&venue=Nowhere&date=2026-01-01", nil)
	rec := httptest.NewRecorder()
	handlers.GetPoster(deps)(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)
	body := decodeBody(t, rec)
	require.Equal(t, "unknown", body["status"])
}

func TestGetPoster_PendingReturns202(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()

	performer, venue, date := "PendingBand", "SomeVenue", "2026-09-01"
	id := poster.JobID(performer, venue, date)
	_, err := q.ClaimPosterJob(ctx, store.ClaimPosterJobParams{
		ID: id, Performer: performer, Venue: venue, Date: date,
		StaleBefore: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	require.NoError(t, err)

	deps := handlers.PosterDeps{Queries: q, Presigner: stubPresigner{}}
	req := httptest.NewRequest(http.MethodGet,
		"/posters?performer="+performer+"&venue="+venue+"&date="+date, nil)
	rec := httptest.NewRecorder()
	handlers.GetPoster(deps)(rec, req)

	require.Equal(t, http.StatusAccepted, rec.Code)
	body := decodeBody(t, rec)
	require.Equal(t, "pending", body["status"])
}

func TestGetPoster_FailedReturns200WithStageAndReason(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()

	performer, venue, date := "FailedBand", "SomeVenue", "2026-09-02"
	id := poster.JobID(performer, venue, date)
	_, err := q.ClaimPosterJob(ctx, store.ClaimPosterJobParams{
		ID: id, Performer: performer, Venue: venue, Date: date,
		StaleBefore: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	require.NoError(t, err)
	stage, reason := "image", "no usable image found"
	require.NoError(t, q.MarkPosterJobFailed(ctx, store.MarkPosterJobFailedParams{
		ID: id, FailureStage: &stage, FailureReason: &reason,
	}))

	deps := handlers.PosterDeps{Queries: q, Presigner: stubPresigner{}}
	req := httptest.NewRequest(http.MethodGet,
		"/posters?performer="+performer+"&venue="+venue+"&date="+date, nil)
	rec := httptest.NewRecorder()
	handlers.GetPoster(deps)(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	body := decodeBody(t, rec)
	require.Equal(t, "failed", body["status"])
	require.Equal(t, stage, body["failure_stage"])
	require.Equal(t, reason, body["failure_reason"])
}

func TestGetPoster_ReadyReturnsUrlsArtistAndCredit(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()

	performer, venue, date := "ReadyBand", "SomeVenue", "2026-09-03"
	id := poster.JobID(performer, venue, date)
	_, err := q.ClaimPosterJob(ctx, store.ClaimPosterJobParams{
		ID: id, Performer: performer, Venue: venue, Date: date,
		StaleBefore: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	require.NoError(t, err)
	svgKey := "posters/v1/ready-band/some-venue-2026-09-03.svg"
	pngKey := "posters/v1/ready-band/some-venue-2026-09-03.png"
	require.NoError(t, q.MarkPosterJobReady(ctx, store.MarkPosterJobReadyParams{
		ID: id, SvgKey: &svgKey, PngKey: &pngKey,
		Artist: json.RawMessage(`{"name":"Ready Band","mbid":"abc-123"}`),
		Credit: json.RawMessage(`{"text":"Photo by Someone","url":"https://example.com/photo"}`),
	}))

	deps := handlers.PosterDeps{Queries: q, Presigner: stubPresigner{}}
	req := httptest.NewRequest(http.MethodGet,
		"/posters?performer="+performer+"&venue="+venue+"&date="+date, nil)
	rec := httptest.NewRecorder()
	handlers.GetPoster(deps)(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	body := decodeBody(t, rec)
	require.Equal(t, "ready", body["status"])

	svgURL, _ := body["svgUrl"].(string)
	pngURL, _ := body["pngUrl"].(string)
	require.Contains(t, svgURL, svgKey)
	require.Contains(t, pngURL, pngKey)

	artist, ok := body["artist"].(map[string]any)
	require.True(t, ok, "artist should decode as an object, got %T", body["artist"])
	require.Equal(t, "Ready Band", artist["name"])

	credit, ok := body["credit"].(map[string]any)
	require.True(t, ok, "credit should decode as an object, got %T", body["credit"])
	require.Equal(t, "Photo by Someone", credit["text"])
}

func TestCreatePoster_ControlledFailureMarksJobFailed(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	gen := &stubGenerator{result: poster.Result{FailureStage: "musicbrainz", FailureReason: "no match found"}}
	deps := handlers.PosterDeps{Queries: q, Generator: gen, Presigner: stubPresigner{}}

	performer, venue, date := "NoMatchBand", "SomeVenue", "2026-09-04"
	body, _ := json.Marshal(map[string]string{"performer": performer, "venue": venue, "date": date})
	req := httptest.NewRequest(http.MethodPost, "/posters", strings.NewReader(string(body)))
	rec := httptest.NewRecorder()
	handlers.CreatePoster(deps)(rec, req)
	require.Equal(t, http.StatusAccepted, rec.Code)

	id := poster.JobID(performer, venue, date)
	require.Eventually(t, func() bool {
		job, err := q.GetPosterJob(context.Background(), id)
		return err == nil && job.Status == "failed"
	}, 2*time.Second, 20*time.Millisecond, "job never reached failed status")

	job, err := q.GetPosterJob(context.Background(), id)
	require.NoError(t, err)
	require.NotNil(t, job.FailureStage)
	require.Equal(t, "musicbrainz", *job.FailureStage)
	require.NotNil(t, job.FailureReason)
	require.Equal(t, "no match found", *job.FailureReason)
}

func TestCreatePoster_GeneratorErrorMarksJobFailedWithGenericReason(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	gen := &stubGenerator{err: errors.New("upstream 500: something very specific and internal that must not leak")}
	deps := handlers.PosterDeps{Queries: q, Generator: gen, Presigner: stubPresigner{}}

	performer, venue, date := "ErrsBand", "SomeVenue", "2026-09-05"
	body, _ := json.Marshal(map[string]string{"performer": performer, "venue": venue, "date": date})
	req := httptest.NewRequest(http.MethodPost, "/posters", strings.NewReader(string(body)))
	rec := httptest.NewRecorder()
	handlers.CreatePoster(deps)(rec, req)
	require.Equal(t, http.StatusAccepted, rec.Code)

	id := poster.JobID(performer, venue, date)
	require.Eventually(t, func() bool {
		job, err := q.GetPosterJob(context.Background(), id)
		return err == nil && job.Status == "failed"
	}, 2*time.Second, 20*time.Millisecond, "job never reached failed status")

	job, err := q.GetPosterJob(context.Background(), id)
	require.NoError(t, err)
	require.NotNil(t, job.FailureReason)
	require.Equal(t, "poster service unavailable", *job.FailureReason)
	require.NotContains(t, *job.FailureReason, "upstream 500")
}
