package handlers_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/http/handlers"
	"github.com/wmyers/heres-whats-happening/internal/http/middleware"
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
func posterFixture(t *testing.T) (performer, venue, date string) {
	t.Helper()
	name := strings.ReplaceAll(t.Name(), "/", "-")
	return name + "-performer", name + "-venue", "2026-08-20"
}

// posterUser creates a confirmed user and returns its id. poster_jobs.user_id
// is a real foreign key, so every poster test needs a real users row; label
// keeps the email unique when one test needs two users.
func posterUser(t *testing.T, q *store.Queries, label string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	city, err := q.GetDefaultCity(ctx)
	require.NoError(t, err)
	row, err := q.CreateUser(ctx, store.CreateUserParams{
		Email:        label + "+" + strings.ReplaceAll(t.Name(), "/", "-") + "@example.com",
		PasswordHash: "stub",
		CityID:       city.ID,
		Confirmed:    true,
	})
	require.NoError(t, err)
	return uuid.UUID(row.ID.Bytes)
}

// posterPostRequest builds a POST /posters carrying uid in its context, the
// way middleware.RequireAuth would on the real route.
func posterPostRequest(uid uuid.UUID, performer, venue, date string, force bool) *http.Request {
	body, _ := json.Marshal(map[string]any{
		"performer": performer, "venue": venue, "date": date, "force": force,
	})
	req := httptest.NewRequest(http.MethodPost, "/posters", strings.NewReader(string(body)))
	return req.WithContext(middleware.ContextWithUserID(req.Context(), uid))
}

// posterGetRequest builds a GET /posters for the same natural key.
//
// The query string is assembled with url.Values.Encode(), never by pasting the
// values in raw. Encode() percent-encodes what has to be percent-encoded — a
// literal "+" becomes "%2B", a space becomes "+" — and r.URL.Query() on the
// handler side reverses exactly that. The decoded value is the canonical form,
// so this is what makes a GET agree with the POST that created the job.
func posterGetRequest(uid uuid.UUID, performer, venue, date string) *http.Request {
	qs := url.Values{"performer": {performer}, "venue": {venue}, "date": {date}}.Encode()
	req := httptest.NewRequest(http.MethodGet, "/posters?"+qs, nil)
	return req.WithContext(middleware.ContextWithUserID(req.Context(), uid))
}

// newPosterHandlerForTest wires CreatePoster against a real, truncated test
// database (via internal/testdb, matching how the rest of this package's
// handler tests obtain a *store.Queries) and the given stub generator.
func newPosterHandlerForTest(t *testing.T, gen poster.Generator) (http.HandlerFunc, *store.Queries) {
	t.Helper()
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	return handlers.CreatePoster(handlers.PosterDeps{
		Queries:   q,
		Generator: gen,
		Presigner: stubPresigner{},
	}), q
}

// THE test that matters: the background goroutine must NOT inherit the request
// context, which is cancelled the moment the 202 is written. If it does,
// generation dies instantly and every job fails for no visible reason.
func TestCreatePosterGenerationSurvivesRequestCancellation(t *testing.T) {
	gen := &stubGenerator{release: make(chan struct{}), sawCtxOK: make(chan bool, 1)}
	h, q := newPosterHandlerForTest(t, gen)
	uid := posterUser(t, q, "a")
	performer, venue, date := posterFixture(t)

	ctx, cancel := context.WithCancel(context.Background())
	req := posterPostRequest(uid, performer, venue, date, false)
	req = req.WithContext(middleware.ContextWithUserID(ctx, uid))
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
	h, q := newPosterHandlerForTest(t, gen)
	uid := posterUser(t, q, "a")
	performer, venue, date := posterFixture(t)

	post := func() int {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, posterPostRequest(uid, performer, venue, date, false))
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

// The authenticated user is part of the natural key, so no request may be
// served without one. The routes sit inside the authenticated + confirmed
// group and so always have it — but an absent id must be an explicit 401
// rather than a silent fall-through that keys the row to the zero uuid (and,
// with a NOT NULL foreign key, 500s on the insert).
func TestPosterHandlers_RejectARequestWithNoUserInContext(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	deps := handlers.PosterDeps{Queries: q, Generator: &stubGenerator{}, Presigner: stubPresigner{}}
	performer, venue, date := posterFixture(t)

	for _, tc := range []struct {
		name string
		req  *http.Request
		h    http.HandlerFunc
	}{
		{
			name: "POST",
			req: httptest.NewRequest(http.MethodPost, "/posters",
				strings.NewReader(`{"performer":"x","venue":"y","date":"z"}`)),
			h: handlers.CreatePoster(deps),
		},
		{
			name: "GET",
			req: httptest.NewRequest(http.MethodGet,
				"/posters?"+url.Values{"performer": {performer}, "venue": {venue}, "date": {date}}.Encode(), nil),
			h: handlers.GetPoster(deps),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			tc.h(rec, tc.req)
			require.Equal(t, http.StatusUnauthorized, rec.Code)
		})
	}
}

// seedReadyPosterJob claims and immediately completes a job for
// (uid, performer, venue, date). Called twice for the same key it is
// idempotent: MarkPosterJobReady is an unconditional UPDATE by id, so
// re-running it just re-marks the same row ready — the second
// ClaimPosterJob's ErrNoRows (a ready row is neither failed nor stale-pending,
// so it isn't reclaimable) is expected and ignored.
func seedReadyPosterJob(t *testing.T, q *store.Queries, uid uuid.UUID, performer, venue, date string) string {
	t.Helper()
	ctx := context.Background()
	id := poster.JobID(uid.String(), performer, venue, date)
	_, _ = q.ClaimPosterJob(ctx, store.ClaimPosterJobParams{
		ID: id, UserID: pgtype.UUID{Bytes: uid, Valid: true},
		Performer: performer, Venue: venue, Date: date,
		StaleBefore: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	pngKey := "posters/v1/la-luz/neumos-2026-08-20.png"
	require.NoError(t, q.MarkPosterJobReady(ctx, store.MarkPosterJobReadyParams{
		ID: id, PngKey: &pngKey,
		Artist: json.RawMessage(`{"name":"La Luz"}`),
		Credit: json.RawMessage(`{"text":"Photo by Someone"}`),
	}))
	return id
}

// getPosterJob reads a row the way the tests want it: by natural key, for one
// user.
func getPosterJob(t *testing.T, q *store.Queries, uid uuid.UUID, performer, venue, date string) (store.PosterJob, error) {
	t.Helper()
	return q.GetPosterJob(context.Background(), store.GetPosterJobParams{
		ID:     poster.JobID(uid.String(), performer, venue, date),
		UserID: pgtype.UUID{Bytes: uid, Valid: true},
	})
}

// A ready job must presign at read time. Returning a stored URL would serve a
// dead link once the 3600s expiry passed, so two GETs of one unchanged row
// must still produce two different signatures.
func TestGetPosterPresignsFreshOnEveryCall(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	uid := posterUser(t, q, "a")
	performer, venue, date := posterFixture(t)
	seedReadyPosterJob(t, q, uid, performer, venue, date)

	deps := handlers.PosterDeps{Queries: q, Presigner: stubPresigner{}}
	urls := func() string {
		rec := httptest.NewRecorder()
		handlers.GetPoster(deps)(rec, posterGetRequest(uid, performer, venue, date))
		require.Equal(t, http.StatusOK, rec.Code)
		body := decodeBody(t, rec)
		// The SVG artifact is gone: a ready response must carry pngUrl only, no
		// svgUrl key at all — not an absent one masked by an empty string.
		require.NotContains(t, body, "svgUrl")
		pngURL, _ := body["pngUrl"].(string)
		return pngURL
	}

	if first, second := urls(), urls(); first == second {
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
	uid := posterUser(t, q, "a")
	deps := handlers.PosterDeps{Queries: q, Presigner: stubPresigner{}}
	performer, venue, date := posterFixture(t)

	rec := httptest.NewRecorder()
	handlers.GetPoster(deps)(rec, posterGetRequest(uid, performer, venue, date))

	require.Equal(t, http.StatusNotFound, rec.Code)
	body := decodeBody(t, rec)
	require.Equal(t, "unknown", body["status"])
}

func TestGetPoster_PendingReturns202(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	uid := posterUser(t, q, "a")
	performer, venue, date := posterFixture(t)
	_, err := q.ClaimPosterJob(context.Background(), store.ClaimPosterJobParams{
		ID: poster.JobID(uid.String(), performer, venue, date), UserID: pgtype.UUID{Bytes: uid, Valid: true},
		Performer: performer, Venue: venue, Date: date,
		StaleBefore: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	require.NoError(t, err)

	deps := handlers.PosterDeps{Queries: q, Presigner: stubPresigner{}}
	rec := httptest.NewRecorder()
	handlers.GetPoster(deps)(rec, posterGetRequest(uid, performer, venue, date))

	require.Equal(t, http.StatusAccepted, rec.Code)
	body := decodeBody(t, rec)
	require.Equal(t, "pending", body["status"])
}

func TestGetPoster_FailedReturns200WithStageAndReason(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()
	uid := posterUser(t, q, "a")
	performer, venue, date := posterFixture(t)

	id := poster.JobID(uid.String(), performer, venue, date)
	_, err := q.ClaimPosterJob(ctx, store.ClaimPosterJobParams{
		ID: id, UserID: pgtype.UUID{Bytes: uid, Valid: true},
		Performer: performer, Venue: venue, Date: date,
		StaleBefore: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	require.NoError(t, err)
	stage, reason := "image", "no usable image found"
	require.NoError(t, q.MarkPosterJobFailed(ctx, store.MarkPosterJobFailedParams{
		ID: id, FailureStage: &stage, FailureReason: &reason,
	}))

	deps := handlers.PosterDeps{Queries: q, Presigner: stubPresigner{}}
	rec := httptest.NewRecorder()
	handlers.GetPoster(deps)(rec, posterGetRequest(uid, performer, venue, date))

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
	uid := posterUser(t, q, "a")
	performer, venue, date := posterFixture(t)

	id := poster.JobID(uid.String(), performer, venue, date)
	_, err := q.ClaimPosterJob(ctx, store.ClaimPosterJobParams{
		ID: id, UserID: pgtype.UUID{Bytes: uid, Valid: true},
		Performer: performer, Venue: venue, Date: date,
		StaleBefore: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	require.NoError(t, err)
	pngKey := "posters/v1/ready-band/some-venue-2026-09-03.png"
	require.NoError(t, q.MarkPosterJobReady(ctx, store.MarkPosterJobReadyParams{
		ID: id, PngKey: &pngKey,
		Artist: json.RawMessage(`{"name":"Ready Band","mbid":"abc-123"}`),
		Credit: json.RawMessage(`{"text":"Photo by Someone","url":"https://example.com/photo"}`),
	}))

	deps := handlers.PosterDeps{Queries: q, Presigner: stubPresigner{}}
	rec := httptest.NewRecorder()
	handlers.GetPoster(deps)(rec, posterGetRequest(uid, performer, venue, date))

	require.Equal(t, http.StatusOK, rec.Code)
	body := decodeBody(t, rec)
	require.Equal(t, "ready", body["status"])

	// The SVG artifact is gone: the ready body must carry pngUrl only.
	require.NotContains(t, body, "svgUrl")
	pngURL, _ := body["pngUrl"].(string)
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
	uid := posterUser(t, q, "a")
	gen := &stubGenerator{result: poster.Result{FailureStage: "musicbrainz", FailureReason: "no match found"}}
	deps := handlers.PosterDeps{Queries: q, Generator: gen, Presigner: stubPresigner{}}

	performer, venue, date := posterFixture(t)
	rec := httptest.NewRecorder()
	handlers.CreatePoster(deps)(rec, posterPostRequest(uid, performer, venue, date, false))
	require.Equal(t, http.StatusAccepted, rec.Code)

	require.Eventually(t, func() bool {
		job, err := getPosterJob(t, q, uid, performer, venue, date)
		return err == nil && job.Status == "failed"
	}, 2*time.Second, 20*time.Millisecond, "job never reached failed status")

	job, err := getPosterJob(t, q, uid, performer, venue, date)
	require.NoError(t, err)
	require.NotNil(t, job.FailureStage)
	require.Equal(t, "musicbrainz", *job.FailureStage)
	require.NotNil(t, job.FailureReason)
	require.Equal(t, "no match found", *job.FailureReason)
}

// A bad png_key failing poster.ValidateKey must fail the JOB, not reach
// "ready". GetPoster presigns the key and PresignGet re-runs the very same
// check, so a ready row holding a bad key returns 500 on every GET, forever: a
// ready row is neither failed nor stale-pending, so nothing re-claims it and
// there is no self-heal. png_key used to be unvalidated, which made exactly
// that state reachable; the failed row this produces instead is one a client
// can see and a POST can retry.
func TestCreatePoster_BadArtifactKeyMarksJobFailed(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	uid := posterUser(t, q, "a")
	gen := &stubGenerator{result: poster.Result{PngKey: "../../etc/passwd"}}
	deps := handlers.PosterDeps{Queries: q, Generator: gen, Presigner: stubPresigner{}}

	performer, venue, date := posterFixture(t)
	rec := httptest.NewRecorder()
	handlers.CreatePoster(deps)(rec, posterPostRequest(uid, performer, venue, date, false))
	require.Equal(t, http.StatusAccepted, rec.Code)

	require.Eventually(t, func() bool {
		job, err := getPosterJob(t, q, uid, performer, venue, date)
		return err == nil && job.Status == "failed"
	}, 2*time.Second, 20*time.Millisecond, "an unusable png_key must fail the job, not mark it ready")

	job, err := getPosterJob(t, q, uid, performer, venue, date)
	require.NoError(t, err)
	require.Equal(t, "failed", job.Status)
	require.Nil(t, job.PngKey, "a failed job must not carry an artifact key")
	require.NotNil(t, job.FailureReason)
	require.Equal(t, "poster service returned an unexpected artifact", *job.FailureReason)

	// And the GET that follows reports the failure instead of 500ing.
	getRec := httptest.NewRecorder()
	handlers.GetPoster(deps)(getRec, posterGetRequest(uid, performer, venue, date))
	require.Equal(t, http.StatusOK, getRec.Code)
	require.Equal(t, "failed", decodeBody(t, getRec)["status"])
}

// force is the escape hatch for regenerating a poster the user dislikes —
// i.e. one that is already "ready". See
// docs/superpowers/specs/2026-08-09-file-backed-poster-artifacts-design.md.
func TestCreatePoster_ForceReclaimsReadyJobAndCallsGenerator(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	uid := posterUser(t, q, "a")
	performer, venue, date := posterFixture(t)
	seedReadyPosterJob(t, q, uid, performer, venue, date)

	gen := &stubGenerator{}
	deps := handlers.PosterDeps{Queries: q, Generator: gen, Presigner: stubPresigner{}}

	rec := httptest.NewRecorder()
	handlers.CreatePoster(deps)(rec, posterPostRequest(uid, performer, venue, date, true))
	require.Equal(t, http.StatusAccepted, rec.Code)

	require.Eventually(t, func() bool { return gen.callCount() >= 1 }, 2*time.Second, 10*time.Millisecond,
		"force:true on a ready job must reclaim it and call the generator")
}

// THE reason poster jobs are keyed per user. force:true re-claims a ready row,
// blanking its artifacts (see
// TestCreatePoster_ForceReclaimClearsPreviousArtifacts). While the natural key
// was only (performer, venue, date), every user shared one row for a show — so
// any confirmed user could destroy any other user's poster by asking for the
// same gig with force:true, and would then also see the regenerated result as
// their own.
func TestCreatePoster_ForceCannotReclaimAnotherUsersJob(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	victim := posterUser(t, q, "victim")
	attacker := posterUser(t, q, "attacker")

	// One show, requested by both users.
	performer, venue, date := posterFixture(t)
	seedReadyPosterJob(t, q, victim, performer, venue, date)

	gen := &stubGenerator{release: make(chan struct{})}
	t.Cleanup(func() { close(gen.release) })
	deps := handlers.PosterDeps{Queries: q, Generator: gen, Presigner: stubPresigner{}}

	rec := httptest.NewRecorder()
	handlers.CreatePoster(deps)(rec, posterPostRequest(attacker, performer, venue, date, true))
	require.Equal(t, http.StatusAccepted, rec.Code)

	// The victim's row is untouched: still ready, still holding its artifacts.
	victimJob, err := getPosterJob(t, q, victim, performer, venue, date)
	require.NoError(t, err)
	require.Equal(t, "ready", victimJob.Status,
		"another user's force:true re-claimed this row — one user can blank another's poster")
	require.NotNil(t, victimJob.PngKey)
	require.NotEmpty(t, victimJob.Artist)

	// The attacker got their own separate, pending row.
	attackerJob, err := getPosterJob(t, q, attacker, performer, venue, date)
	require.NoError(t, err)
	require.Equal(t, "pending", attackerJob.Status)
	require.NotEqual(t, victimJob.ID, attackerJob.ID, "two users must not share one job id")

	// And the victim's GET still reports ready — the read path is scoped too.
	getRec := httptest.NewRecorder()
	handlers.GetPoster(deps)(getRec, posterGetRequest(victim, performer, venue, date))
	require.Equal(t, http.StatusOK, getRec.Code)
	require.Equal(t, "ready", decodeBody(t, getRec)["status"])
}

// POST reads the natural key from a JSON body, GET from a query string. Those
// are different encodings of the same string, and the canonical form — the one
// poster.JobID hashes on both paths — is the DECODED value: what JSON carries
// verbatim, and what r.URL.Query() produces after percent-decoding and turning
// "+" into a space.
//
// No existing fixture could catch a disagreement, because they are all
// deliberately boring ("LaLuz", "ReadyBand"): a space is exactly where a raw
// space, "+" and "%20" diverge. These values are picked to break a client that
// pastes them into a URL unencoded — a literal "+", an "&", a "%", and spaces
// throughout. A mismatch here means the job completes invisibly under one id
// while the caller polls another and gets 404 forever.
func TestPoster_PostThenGetRoundTripsValuesNeedingEncoding(t *testing.T) {
	for _, tc := range []struct{ name, performer, venue, date string }{
		{"literal-plus", "AC+DC", "The Showbox SoDo", "Thursday, August 20"},
		{"spaces-and-ampersand", "La Luz", "Neumos & Barboza", "Thu 20 Aug"},
		{"percent-sign", "100% Silk", "El Rey", "2026-08-20"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gen := &stubGenerator{release: make(chan struct{})} // hold the job in pending
			t.Cleanup(func() { close(gen.release) })
			pool := testdb.MustOpen(t)
			q := store.New(pool)
			uid := posterUser(t, q, "a")
			deps := handlers.PosterDeps{Queries: q, Generator: gen, Presigner: stubPresigner{}}

			rec := httptest.NewRecorder()
			handlers.CreatePoster(deps)(rec, posterPostRequest(uid, tc.performer, tc.venue, tc.date, false))
			require.Equal(t, http.StatusAccepted, rec.Code)

			// The row stores the decoded values verbatim.
			job, err := getPosterJob(t, q, uid, tc.performer, tc.venue, tc.date)
			require.NoError(t, err)
			require.Equal(t, tc.performer, job.Performer)
			require.Equal(t, tc.venue, job.Venue)
			require.Equal(t, tc.date, job.Date)

			// And the GET finds it: same id, not a 404 while the job completes
			// invisibly under another one.
			getRec := httptest.NewRecorder()
			handlers.GetPoster(deps)(getRec, posterGetRequest(uid, tc.performer, tc.venue, tc.date))
			require.Equal(t, http.StatusAccepted, getRec.Code,
				"GET computed a different job id than POST for %q", tc.performer)
			require.Equal(t, "pending", decodeBody(t, getRec)["status"])
		})
	}
}

// The decoding rule itself, pinned. In a query string "+" means a space —
// that is what url.Values.Encode() emits for a space and what r.URL.Query()
// reverses — so a client that POSTs "AC DC" finds its job at
// "?performer=AC+DC", and one that POSTs "AC+DC" must ask for "%2B".
//
// Worth pinning because the opposite reading (treat a raw "+" as a literal
// plus) is one line away, looks harmless, and would strand every job whose
// performer contains a space.
func TestGetPoster_PlusInTheQueryStringIsASpace(t *testing.T) {
	gen := &stubGenerator{release: make(chan struct{})}
	t.Cleanup(func() { close(gen.release) })
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	uid := posterUser(t, q, "a")
	deps := handlers.PosterDeps{Queries: q, Generator: gen, Presigner: stubPresigner{}}

	const spaced = "AC DC" // a space, POSTed as JSON
	_, venue, date := posterFixture(t)
	rec := httptest.NewRecorder()
	handlers.CreatePoster(deps)(rec, posterPostRequest(uid, spaced, venue, date, false))
	require.Equal(t, http.StatusAccepted, rec.Code)

	// A hand-built query string, not url.Values.Encode(), so the raw "+" and
	// the escaped "%2B" reach the handler exactly as written.
	get := func(rawPerformer string) *httptest.ResponseRecorder {
		target := fmt.Sprintf("/posters?performer=%s&venue=%s&date=%s",
			rawPerformer, url.QueryEscape(venue), url.QueryEscape(date))
		req := httptest.NewRequest(http.MethodGet, target, nil)
		rec := httptest.NewRecorder()
		handlers.GetPoster(deps)(rec, req.WithContext(middleware.ContextWithUserID(req.Context(), uid)))
		return rec
	}

	require.Equal(t, http.StatusAccepted, get("AC+DC").Code,
		`"+" in a query string must decode to a space and find the job POSTed as "AC DC"`)
	require.Equal(t, http.StatusNotFound, get("AC%2BDC").Code,
		`"%2B" is a literal "+", a different performer than "AC DC"`)
}

// One user's job must not answer another user's GET, even for the same show:
// a 404 ("you never asked for this") is the correct answer, not a peek at
// someone else's presigned artifact URLs.
func TestGetPoster_DoesNotSeeAnotherUsersJob(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	owner := posterUser(t, q, "owner")
	stranger := posterUser(t, q, "stranger")

	performer, venue, date := posterFixture(t)
	seedReadyPosterJob(t, q, owner, performer, venue, date)

	deps := handlers.PosterDeps{Queries: q, Presigner: stubPresigner{}}
	rec := httptest.NewRecorder()
	handlers.GetPoster(deps)(rec, posterGetRequest(stranger, performer, venue, date))

	require.Equal(t, http.StatusNotFound, rec.Code)
	require.Equal(t, "unknown", decodeBody(t, rec)["status"])
}

// The current (pinned) behavior: without force, a ready job is never
// reclaimed, no matter how many POSTs arrive for it.
func TestCreatePoster_WithoutForceDoesNotReclaimReadyJob(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	uid := posterUser(t, q, "a")
	performer, venue, date := posterFixture(t)
	seedReadyPosterJob(t, q, uid, performer, venue, date)

	gen := &stubGenerator{}
	deps := handlers.PosterDeps{Queries: q, Generator: gen, Presigner: stubPresigner{}}

	rec := httptest.NewRecorder()
	handlers.CreatePoster(deps)(rec, posterPostRequest(uid, performer, venue, date, false))
	require.Equal(t, http.StatusAccepted, rec.Code)

	require.Never(t, func() bool { return gen.callCount() >= 1 }, 300*time.Millisecond, 10*time.Millisecond,
		"force:false must not reclaim a ready job")

	job, err := getPosterJob(t, q, uid, performer, venue, date)
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
	uid := posterUser(t, q, "a")
	performer, venue, date := posterFixture(t)
	deps := handlers.PosterDeps{Queries: q, Generator: gen, Presigner: stubPresigner{}}

	post := func(force bool) int {
		rec := httptest.NewRecorder()
		handlers.CreatePoster(deps)(rec, posterPostRequest(uid, performer, venue, date, force))
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
	uid := posterUser(t, q, "a")
	performer, venue, date := posterFixture(t)
	seedReadyPosterJob(t, q, uid, performer, venue, date)

	before, err := getPosterJob(t, q, uid, performer, venue, date)
	require.NoError(t, err)
	require.Equal(t, "ready", before.Status)
	require.NotNil(t, before.PngKey)
	require.NotEmpty(t, before.Artist)
	require.NotEmpty(t, before.Credit)

	gen := &stubGenerator{release: make(chan struct{})} // block so the row can be inspected mid-flight
	t.Cleanup(func() { close(gen.release) })
	deps := handlers.PosterDeps{Queries: q, Generator: gen, Presigner: stubPresigner{}}

	rec := httptest.NewRecorder()
	handlers.CreatePoster(deps)(rec, posterPostRequest(uid, performer, venue, date, true))
	require.Equal(t, http.StatusAccepted, rec.Code)

	after, err := getPosterJob(t, q, uid, performer, venue, date)
	require.NoError(t, err)
	require.Equal(t, "pending", after.Status)
	require.Nil(t, after.PngKey)
	require.Empty(t, after.Artist)
	require.Empty(t, after.Credit)
	require.Nil(t, after.FailureStage)
	require.Nil(t, after.FailureReason)
}

func TestCreatePoster_GeneratorErrorMarksJobFailedWithGenericReason(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	uid := posterUser(t, q, "a")
	gen := &stubGenerator{err: errors.New("upstream 500: something very specific and internal that must not leak")}
	deps := handlers.PosterDeps{Queries: q, Generator: gen, Presigner: stubPresigner{}}

	performer, venue, date := posterFixture(t)
	rec := httptest.NewRecorder()
	handlers.CreatePoster(deps)(rec, posterPostRequest(uid, performer, venue, date, false))
	require.Equal(t, http.StatusAccepted, rec.Code)

	require.Eventually(t, func() bool {
		job, err := getPosterJob(t, q, uid, performer, venue, date)
		return err == nil && job.Status == "failed"
	}, 2*time.Second, 20*time.Millisecond, "job never reached failed status")

	job, err := getPosterJob(t, q, uid, performer, venue, date)
	require.NoError(t, err)
	require.NotNil(t, job.FailureReason)
	require.Equal(t, "poster service unavailable", *job.FailureReason)
	require.NotContains(t, *job.FailureReason, "upstream 500")
}
