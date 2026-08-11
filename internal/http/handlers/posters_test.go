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

// posterFixture returns a (performer, venue, date) triple unique to the
// calling test.
//
// Every test in this file must use it rather than a hand-picked triple.
// poster.JobID is a digest of the natural key, so two tests sharing a triple
// share a poster_jobs row — and the tests here deliberately leave background
// work in flight: TestCreatePosterGenerationSurvivesRequestCancellation
// returns the instant it has read sawCtxOK, with its startGeneration goroutine
// still running. That goroutine then writes MarkPosterJobFailed onto whatever
// row now holds that id, i.e. the NEXT test's. A failed row is legitimately
// re-claimable, so that test's second POST starts a real second generation and
// trips its require.Never — a false failure in a test that is doing nothing
// wrong.
//
// The values are deliberately free of spaces and of anything else needing
// percent-encoding: the GET tests paste them straight into a query string.
func posterFixture(t *testing.T) (performer, venue, date string) {
	t.Helper()
	name := strings.ReplaceAll(t.Name(), "/", "-")
	return name + "-performer", name + "-venue", "2026-08-20"
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
	performer, venue, date := posterFixture(t)

	ctx, cancel := context.WithCancel(context.Background())
	body, _ := json.Marshal(map[string]string{"performer": performer, "venue": venue, "date": date})
	req := httptest.NewRequest(http.MethodPost, "/posters",
		strings.NewReader(string(body))).WithContext(ctx)
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
	performer, venue, date := posterFixture(t)
	body, _ := json.Marshal(map[string]string{"performer": performer, "venue": venue, "date": date})

	post := func() int {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/posters",
			strings.NewReader(string(body))))
		return rec.Code
	}

	if got := post(); got != http.StatusAccepted {
		t.Fatalf("first POST = %d, want 202", got)
	}
	if got := post(); got != http.StatusAccepted {
		t.Fatalf("second POST = %d, want 202", got)
	}
	close(gen.release)

	// close() only unblocks goroutines already parked on <-s.release; a bogus
	// second goroutine spawned by the second POST may not have been scheduled
	// yet at this instant, so reading callCount() once here is a race — it
	// can read 1 even when a second call is about to land. Wait for the
	// (legitimate) first call to show up, then hold the assertion open for a
	// settle window so a bogus second call has a fair chance to appear before
	// declaring success.
	require.Eventually(t, func() bool { return gen.callCount() >= 1 }, 2*time.Second, 5*time.Millisecond,
		"generation never ran")
	require.Never(t, func() bool { return gen.callCount() > 1 }, 300*time.Millisecond, 10*time.Millisecond,
		"generation ran more than once — the second POST must join the pending job, not start its own")
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
	performer, venue, date := posterFixture(t)
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

	performer, venue, date := posterFixture(t)
	req := httptest.NewRequest(http.MethodGet,
		"/posters?performer="+performer+"&venue="+venue+"&date="+date, nil)
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

	performer, venue, date := posterFixture(t)
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

	performer, venue, date := posterFixture(t)
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

	performer, venue, date := posterFixture(t)
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

	performer, venue, date := posterFixture(t)
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

// Either key failing poster.ValidateKey must fail the JOB — neither may reach
// "ready". GetPoster presigns both keys and PresignGet re-runs the very same
// check, so a ready row holding a bad key returns 500 on every GET, forever: a
// ready row is neither failed nor stale-pending, so nothing re-claims it and
// there is no self-heal. png_key used to be unvalidated, which made exactly
// that state reachable; the failed row this produces instead is one a client
// can see and a POST can retry.
func TestCreatePoster_BadArtifactKeyMarksJobFailed(t *testing.T) {
	for _, tc := range []struct {
		name   string
		result poster.Result
	}{
		{"bad-svg-key", poster.Result{SvgKey: "../../etc/passwd", PngKey: "posters/v1/ok/ok.png"}},
		{"bad-png-key", poster.Result{SvgKey: "posters/v1/ok/ok.svg", PngKey: "../../etc/passwd"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pool := testdb.MustOpen(t)
			q := store.New(pool)
			gen := &stubGenerator{result: tc.result}
			deps := handlers.PosterDeps{Queries: q, Generator: gen, Presigner: stubPresigner{}}

			performer, venue, date := posterFixture(t)
			body, _ := json.Marshal(map[string]string{"performer": performer, "venue": venue, "date": date})
			rec := httptest.NewRecorder()
			handlers.CreatePoster(deps)(rec, httptest.NewRequest(http.MethodPost, "/posters", strings.NewReader(string(body))))
			require.Equal(t, http.StatusAccepted, rec.Code)

			id := poster.JobID(performer, venue, date)
			require.Eventually(t, func() bool {
				job, err := q.GetPosterJob(context.Background(), id)
				return err == nil && job.Status == "failed"
			}, 2*time.Second, 20*time.Millisecond, "an unusable %s must fail the job, not mark it ready", tc.name)

			job, err := q.GetPosterJob(context.Background(), id)
			require.NoError(t, err)
			require.Equal(t, "failed", job.Status)
			require.Nil(t, job.SvgKey, "a failed job must not carry artifact keys")
			require.Nil(t, job.PngKey)
			require.NotNil(t, job.FailureReason)
			require.Equal(t, "poster service returned an unexpected artifact", *job.FailureReason)

			// And the GET that follows reports the failure instead of 500ing.
			getRec := httptest.NewRecorder()
			handlers.GetPoster(deps)(getRec, httptest.NewRequest(http.MethodGet,
				"/posters?performer="+performer+"&venue="+venue+"&date="+date, nil))
			require.Equal(t, http.StatusOK, getRec.Code)
			require.Equal(t, "failed", decodeBody(t, getRec)["status"])
		})
	}
}

// force is the escape hatch for regenerating a poster the user dislikes —
// i.e. one that is already "ready". See
// docs/superpowers/specs/2026-08-09-file-backed-poster-artifacts-design.md.
func TestCreatePoster_ForceReclaimsReadyJobAndCallsGenerator(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	performer, venue, date := posterFixture(t)
	seedReadyPosterJob(t, q, performer, venue, date)

	gen := &stubGenerator{}
	deps := handlers.PosterDeps{Queries: q, Generator: gen, Presigner: stubPresigner{}}

	body, _ := json.Marshal(map[string]any{"performer": performer, "venue": venue, "date": date, "force": true})
	req := httptest.NewRequest(http.MethodPost, "/posters", strings.NewReader(string(body)))
	rec := httptest.NewRecorder()
	handlers.CreatePoster(deps)(rec, req)
	require.Equal(t, http.StatusAccepted, rec.Code)

	require.Eventually(t, func() bool { return gen.callCount() >= 1 }, 2*time.Second, 10*time.Millisecond,
		"force:true on a ready job must reclaim it and call the generator")
}

// The current (pinned) behavior: without force, a ready job is never
// reclaimed, no matter how many POSTs arrive for it.
func TestCreatePoster_WithoutForceDoesNotReclaimReadyJob(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	performer, venue, date := posterFixture(t)
	seedReadyPosterJob(t, q, performer, venue, date)

	gen := &stubGenerator{}
	deps := handlers.PosterDeps{Queries: q, Generator: gen, Presigner: stubPresigner{}}

	body, _ := json.Marshal(map[string]any{"performer": performer, "venue": venue, "date": date, "force": false})
	req := httptest.NewRequest(http.MethodPost, "/posters", strings.NewReader(string(body)))
	rec := httptest.NewRecorder()
	handlers.CreatePoster(deps)(rec, req)
	require.Equal(t, http.StatusAccepted, rec.Code)

	require.Never(t, func() bool { return gen.callCount() >= 1 }, 300*time.Millisecond, 10*time.Millisecond,
		"force:false must not reclaim a ready job")

	id := poster.JobID(performer, venue, date)
	job, err := q.GetPosterJob(context.Background(), id)
	require.NoError(t, err)
	require.Equal(t, "ready", job.Status)
}

// force must not let a caller jump a job that is already being generated —
// the force clause is scoped to status = 'ready' for exactly this reason.
// Two generations racing for the same poster would let the second overwrite
// the first.
func TestCreatePoster_ForceDoesNotReclaimFreshPendingJob(t *testing.T) {
	gen := &stubGenerator{release: make(chan struct{})}
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	deps := handlers.PosterDeps{Queries: q, Generator: gen, Presigner: stubPresigner{}}

	post := func(force bool) int {
		body, _ := json.Marshal(map[string]any{
			"performer": "FreshPendingBand", "venue": "SomeVenue", "date": "2026-09-08", "force": force,
		})
		rec := httptest.NewRecorder()
		handlers.CreatePoster(deps)(rec, httptest.NewRequest(http.MethodPost, "/posters", strings.NewReader(string(body))))
		return rec.Code
	}

	require.Equal(t, http.StatusAccepted, post(false)) // starts the one legitimate generation
	require.Equal(t, http.StatusAccepted, post(true))  // force must NOT reclaim a fresh pending row
	close(gen.release)

	// Same fair-scheduling-window pattern as
	// TestCreatePosterDoesNotStartASecondGenerationForAPendingJob: wait for the
	// legitimate call to land, then hold the assertion open so a bogus second
	// call — one that force wrongly let through — has a chance to appear.
	require.Eventually(t, func() bool { return gen.callCount() >= 1 }, 2*time.Second, 5*time.Millisecond,
		"generation never ran")
	require.Never(t, func() bool { return gen.callCount() > 1 }, 300*time.Millisecond, 10*time.Millisecond,
		"force:true reclaimed a fresh pending job — two generations are now racing for the same poster")
}

// A forced reclaim must clear the previous artifacts at claim time, not just
// at completion — otherwise a failed regeneration leaves the old svg/png
// keys and artist/credit in place, and a client polling GetPoster mid-flight
// would see a "ready" job whose links still work but whose content is about
// to be replaced or never was regenerated at all. Checked immediately after
// the claim, before the (deliberately blocked) generator runs, since the
// clearing happens in the same UPDATE that flips the row back to pending.
func TestCreatePoster_ForceReclaimClearsPreviousArtifacts(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	performer, venue, date := posterFixture(t)
	id := seedReadyPosterJob(t, q, performer, venue, date)

	before, err := q.GetPosterJob(context.Background(), id)
	require.NoError(t, err)
	require.Equal(t, "ready", before.Status)
	require.NotNil(t, before.SvgKey)
	require.NotNil(t, before.PngKey)
	require.NotEmpty(t, before.Artist)
	require.NotEmpty(t, before.Credit)

	gen := &stubGenerator{release: make(chan struct{})} // block so the row can be inspected mid-flight
	t.Cleanup(func() { close(gen.release) })
	deps := handlers.PosterDeps{Queries: q, Generator: gen, Presigner: stubPresigner{}}

	body, _ := json.Marshal(map[string]any{"performer": performer, "venue": venue, "date": date, "force": true})
	req := httptest.NewRequest(http.MethodPost, "/posters", strings.NewReader(string(body)))
	rec := httptest.NewRecorder()
	handlers.CreatePoster(deps)(rec, req)
	require.Equal(t, http.StatusAccepted, rec.Code)

	after, err := q.GetPosterJob(context.Background(), id)
	require.NoError(t, err)
	require.Equal(t, "pending", after.Status)
	require.Nil(t, after.SvgKey)
	require.Nil(t, after.PngKey)
	require.Empty(t, after.Artist)
	require.Empty(t, after.Credit)
	require.Nil(t, after.FailureStage)
	require.Nil(t, after.FailureReason)
}

func TestCreatePoster_GeneratorErrorMarksJobFailedWithGenericReason(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	gen := &stubGenerator{err: errors.New("upstream 500: something very specific and internal that must not leak")}
	deps := handlers.PosterDeps{Queries: q, Generator: gen, Presigner: stubPresigner{}}

	performer, venue, date := posterFixture(t)
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
