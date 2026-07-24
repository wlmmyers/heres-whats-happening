package middleware_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/http/middleware"
	"github.com/wmyers/heres-whats-happening/internal/observability"
	"github.com/wmyers/heres-whats-happening/internal/ratelimit"
)

// stubLimiter lets each test dictate decisions and observe Record calls.
type stubLimiter struct {
	decision ratelimit.Decision
	allowErr error
	recorded []string
}

func (s *stubLimiter) Allow(_ context.Context, _ string) (ratelimit.Decision, error) {
	return s.decision, s.allowErr
}

func (s *stubLimiter) Record(_ context.Context, key string) error {
	s.recorded = append(s.recorded, key)
	return nil
}

func okHandler(status int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
	})
}

func TestRateLimit_AllowsWhenUnderLimit(t *testing.T) {
	l := &stubLimiter{decision: ratelimit.Decision{Allowed: true}}
	h := middleware.RateLimit(l, "login")(okHandler(http.StatusOK))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/auth/login", nil))

	require.Equal(t, http.StatusOK, rec.Code)
}

func TestRateLimit_Returns429WithRetryAfter(t *testing.T) {
	l := &stubLimiter{decision: ratelimit.Decision{Allowed: false, RetryAfter: 90 * time.Second}}
	reached := false
	h := middleware.RateLimit(l, "login")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/auth/login", nil))

	require.Equal(t, http.StatusTooManyRequests, rec.Code)
	require.False(t, reached, "a limited request must never reach the handler")

	secs, err := strconv.Atoi(rec.Header().Get("Retry-After"))
	require.NoError(t, err)
	require.Equal(t, 90, secs)

	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, "rate_limited", body.Error.Code)
}

// Sub-second retry windows must not round down to "Retry-After: 0", which would
// invite an immediate retry.
func TestRateLimit_RetryAfterRoundsUpToAtLeastOne(t *testing.T) {
	l := &stubLimiter{decision: ratelimit.Decision{Allowed: false, RetryAfter: 200 * time.Millisecond}}
	h := middleware.RateLimit(l, "login")(okHandler(http.StatusOK))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/auth/login", nil))

	require.Equal(t, "1", rec.Header().Get("Retry-After"))
}

func TestRateLimit_FailsOpenOnLimiterError(t *testing.T) {
	l := &stubLimiter{
		decision: ratelimit.Decision{Allowed: true},
		allowErr: context.DeadlineExceeded,
	}
	h := middleware.RateLimit(l, "signup")(okHandler(http.StatusOK))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/auth/signup", nil))

	require.Equal(t, http.StatusOK, rec.Code)
}

// An error accompanying a denial must never be mistaken for permission:
// Postgres.Allow can return Allowed=false with a non-nil error when the
// caller is genuinely over the limit but the secondary oldest-event lookup
// failed. The request must still be rejected, not let through.
func TestRateLimit_RejectsWhenDeniedWithError(t *testing.T) {
	l := &stubLimiter{
		decision: ratelimit.Decision{Allowed: false, RetryAfter: 45 * time.Second},
		allowErr: context.DeadlineExceeded,
	}
	reached := false
	h := middleware.RateLimit(l, "signup")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/auth/signup", nil))

	require.Equal(t, http.StatusTooManyRequests, rec.Code)
	require.False(t, reached, "a denial accompanied by an error must still reject, never fail open")

	secs, err := strconv.Atoi(rec.Header().Get("Retry-After"))
	require.NoError(t, err)
	require.Equal(t, 45, secs)
}

func TestRateLimitOnSuccess_RecordsOn201(t *testing.T) {
	l := &stubLimiter{decision: ratelimit.Decision{Allowed: true}}
	h := middleware.RateLimitOnSuccess(l, "signup")(okHandler(http.StatusCreated))

	req := httptest.NewRequest(http.MethodPost, "/auth/signup", nil)
	req.RemoteAddr = "203.0.113.9:1234"
	h.ServeHTTP(httptest.NewRecorder(), req)

	require.Equal(t, []string{"ip:203.0.113.9"}, l.recorded)
}

// The core of the successes-only policy: a mistyped password must cost nothing.
func TestRateLimitOnSuccess_DoesNotRecordOn400(t *testing.T) {
	l := &stubLimiter{decision: ratelimit.Decision{Allowed: true}}
	h := middleware.RateLimitOnSuccess(l, "signup")(okHandler(http.StatusBadRequest))

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/auth/signup", nil))

	require.Empty(t, l.recorded, "a validation failure must not consume signup budget")
}

func TestRateLimitOnSuccess_DoesNotRecordOn409(t *testing.T) {
	l := &stubLimiter{decision: ratelimit.Decision{Allowed: true}}
	h := middleware.RateLimitOnSuccess(l, "signup")(okHandler(http.StatusConflict))

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/auth/signup", nil))

	require.Empty(t, l.recorded, "a duplicate email must not consume signup budget")
}

func TestRateLimitOnSuccess_DoesNotRecordWhenDenied(t *testing.T) {
	l := &stubLimiter{decision: ratelimit.Decision{Allowed: false, RetryAfter: time.Hour}}
	h := middleware.RateLimitOnSuccess(l, "signup")(okHandler(http.StatusCreated))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/auth/signup", nil))

	require.Equal(t, http.StatusTooManyRequests, rec.Code)
	require.Empty(t, l.recorded)
}

// Same as TestRateLimit_RejectsWhenDeniedWithError for the success-conditional
// middleware: an error accompanying a denial must still reject the request
// and must not be treated as a success worth recording.
func TestRateLimitOnSuccess_RejectsAndDoesNotRecordWhenDeniedWithError(t *testing.T) {
	l := &stubLimiter{
		decision: ratelimit.Decision{Allowed: false, RetryAfter: 45 * time.Second},
		allowErr: context.DeadlineExceeded,
	}
	h := middleware.RateLimitOnSuccess(l, "signup")(okHandler(http.StatusCreated))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/auth/signup", nil))

	require.Equal(t, http.StatusTooManyRequests, rec.Code)
	require.Empty(t, l.recorded, "a denial accompanied by an error must not be recorded as a success")
}

// A handler that writes a body without an explicit WriteHeader still gets
// Status()==200 straight from chi's WrapResponseWriter, so this does not
// exercise the ratelimit.go status==0 fallback — see
// TestRateLimitOnSuccess_RecordsWhenHandlerWritesNothing for the case that
// fallback actually covers.
func TestRateLimitOnSuccess_RecordsOnImplicit200(t *testing.T) {
	l := &stubLimiter{decision: ratelimit.Decision{Allowed: true}}
	h := middleware.RateLimitOnSuccess(l, "signup")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/auth/signup", nil))

	require.Len(t, l.recorded, 1)
}

// The status==0 fallback exists for a handler that writes nothing at all: no
// WriteHeader, no Write. WrapResponseWriter never observes a status, but
// net/http still sends an implicit 200 once the response completes, so this
// must still be recorded as a success.
func TestRateLimitOnSuccess_RecordsWhenHandlerWritesNothing(t *testing.T) {
	l := &stubLimiter{decision: ratelimit.Decision{Allowed: true}}
	h := middleware.RateLimitOnSuccess(l, "signup")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Intentionally does nothing.
	}))

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/auth/signup", nil))

	require.Len(t, l.recorded, 1)
}

func TestRateLimit_EmitsMetricOnRejection(t *testing.T) {
	var buf bytes.Buffer
	old := observability.Default
	observability.Default = observability.NewEmitter(&buf)
	defer func() { observability.Default = old }()

	l := &stubLimiter{decision: ratelimit.Decision{Allowed: false, RetryAfter: time.Minute}}
	h := middleware.RateLimit(l, "login")(okHandler(http.StatusOK))
	req := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
	req.RemoteAddr = "203.0.113.9:1234"
	h.ServeHTTP(httptest.NewRecorder(), req)

	out := buf.String()
	require.Contains(t, out, `"endpoint":"login"`, "metric must carry the endpoint dimension")
	require.Contains(t, out, `"ip":"203.0.113.9"`, "metric must carry the client ip property")
	require.Contains(t, out, "RateLimitRejections")
}

func TestRateLimit_NoMetricWhenAllowed(t *testing.T) {
	var buf bytes.Buffer
	old := observability.Default
	observability.Default = observability.NewEmitter(&buf)
	defer func() { observability.Default = old }()

	l := &stubLimiter{decision: ratelimit.Decision{Allowed: true}}
	h := middleware.RateLimit(l, "login")(okHandler(http.StatusOK))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/auth/login", nil))

	require.Empty(t, buf.String(), "an allowed request must emit no rejection metric")
}

// A fail-open request (Allowed:true WITH an error) succeeded; it must not
// emit a rejection metric.
func TestRateLimit_NoEmitOnFailOpen(t *testing.T) {
	var buf bytes.Buffer
	old := observability.Default
	observability.Default = observability.NewEmitter(&buf)
	defer func() { observability.Default = old }()

	l := &stubLimiter{
		decision: ratelimit.Decision{Allowed: true},
		allowErr: context.DeadlineExceeded,
	}
	h := middleware.RateLimit(l, middleware.EndpointLogin)(okHandler(http.StatusOK))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/auth/login", nil))

	require.Equal(t, http.StatusOK, rec.Code)
	require.Empty(t, buf.String(), "a fail-open request must emit no rejection metric")
}

// Genuinely over the limit but a secondary lookup errored (Allowed:false WITH
// an error): the request must still be rejected and the rejection still
// emitted.
func TestRateLimit_EmitsOnDeniedWithError(t *testing.T) {
	var buf bytes.Buffer
	old := observability.Default
	observability.Default = observability.NewEmitter(&buf)
	defer func() { observability.Default = old }()

	l := &stubLimiter{
		decision: ratelimit.Decision{Allowed: false, RetryAfter: time.Minute},
		allowErr: context.DeadlineExceeded,
	}
	h := middleware.RateLimit(l, middleware.EndpointLogin)(okHandler(http.StatusOK))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/auth/login", nil))

	require.Equal(t, http.StatusTooManyRequests, rec.Code)
	out := buf.String()
	require.Contains(t, out, `"endpoint":"login"`, "metric must carry the endpoint dimension")
	require.Contains(t, out, "RateLimitRejections")
}

// These endpoint values are the metric `endpoint` dimension the CloudWatch
// alarms in terraform/prod/observability.tf key on. If you change one, update
// that file's ratelimit_alarms map keys or the matching alarm goes blind.
func TestEndpointConstants(t *testing.T) {
	require.Equal(t, "signup", middleware.EndpointSignup)
	require.Equal(t, "login", middleware.EndpointLogin)
	require.Equal(t, "refresh", middleware.EndpointRefresh)
	require.Equal(t, "logout", middleware.EndpointLogout)
	require.Equal(t, "ical_feed", middleware.EndpointIcalFeed)
	require.Equal(t, "readyz", middleware.EndpointReadyz)
}

// keyRecordingLimiter captures the key each call was made with, so tests can
// assert on the key strategy rather than only on the status code.
type keyRecordingLimiter struct {
	allowedKeys  []string
	recordedKeys []string
}

func (k *keyRecordingLimiter) Allow(_ context.Context, key string) (ratelimit.Decision, error) {
	k.allowedKeys = append(k.allowedKeys, key)
	return ratelimit.Decision{Allowed: true}, nil
}

func (k *keyRecordingLimiter) Record(_ context.Context, key string) error {
	k.recordedKeys = append(k.recordedKeys, key)
	return nil
}

func TestRateLimitByUser_KeysOnUserID(t *testing.T) {
	uid := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	l := &keyRecordingLimiter{}
	h := middleware.RateLimitByUser(l, middleware.EndpointAuthed)(okHandler(http.StatusOK))

	req := httptest.NewRequest(http.MethodGet, "/me", nil)
	req = req.WithContext(middleware.ContextWithUserID(req.Context(), uid))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, []string{"u:" + uid.String()}, l.allowedKeys)
	require.Equal(t, []string{"u:" + uid.String()}, l.recordedKeys,
		"an allowed request must spend its token")
}

func TestRateLimitByUser_TwoUsersHaveIndependentBudgets(t *testing.T) {
	alice := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	bob := uuid.MustParse("22222222-2222-2222-2222-222222222222")

	l := ratelimit.NewMemory(1, time.Minute)
	h := middleware.RateLimitByUser(l, middleware.EndpointAuthed)(okHandler(http.StatusOK))

	do := func(uid uuid.UUID) int {
		req := httptest.NewRequest(http.MethodGet, "/me", nil)
		req = req.WithContext(middleware.ContextWithUserID(req.Context(), uid))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}

	require.Equal(t, http.StatusOK, do(alice))
	require.Equal(t, http.StatusTooManyRequests, do(alice), "alice is out of budget")
	require.Equal(t, http.StatusOK, do(bob), "bob has his own budget")
}

// RequireAuth 401s before this middleware runs, so a missing user ID means a
// middleware-ordering bug. Falling back to the IP degrades to the old behavior
// instead of collapsing every such request into one shared empty-string bucket.
func TestRateLimitByUser_FallsBackToIPWhenNoUser(t *testing.T) {
	l := &keyRecordingLimiter{}
	h := middleware.RateLimitByUser(l, middleware.EndpointAuthed)(okHandler(http.StatusOK))

	req := httptest.NewRequest(http.MethodGet, "/me", nil)
	req.RemoteAddr = "203.0.113.9:5555"

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, []string{"ip:203.0.113.9"}, l.allowedKeys)
}

func TestRateLimit_KeysOnIPAndSpendsToken(t *testing.T) {
	l := &keyRecordingLimiter{}
	h := middleware.RateLimit(l, middleware.EndpointLogin)(okHandler(http.StatusOK))

	req := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
	req.RemoteAddr = "198.51.100.4:1234"

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	require.Equal(t, []string{"ip:198.51.100.4"}, l.allowedKeys)
	require.Equal(t, []string{"ip:198.51.100.4"}, l.recordedKeys)
}

// The signup limit is 3/hour. A user who fat-fingers their email or picks a
// weak password must not burn that budget — otherwise three typos lock them out
// for an hour with no account to show for it. This is an integration test
// against the real Memory limiter on purpose: it is exactly the pairing that
// used to be a silent no-op.
func TestRateLimitOnSuccess_WithMemory_FailuresConsumeNoBudget(t *testing.T) {
	l := ratelimit.NewMemory(3, time.Hour)

	status := http.StatusBadRequest
	h := middleware.RateLimitOnSuccess(l, middleware.EndpointSignup)(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
		}))

	do := func() int {
		req := httptest.NewRequest(http.MethodPost, "/auth/signup", nil)
		req.RemoteAddr = "203.0.113.7:4444"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}

	for i := range 3 {
		require.Equal(t, http.StatusBadRequest, do(), "rejected signup %d", i+1)
	}

	status = http.StatusCreated
	require.Equal(t, http.StatusCreated, do(),
		"three failed signups must leave the hourly budget untouched")
}

func TestRateLimitOnSuccess_WithMemory_SuccessesConsumeBudget(t *testing.T) {
	var buf bytes.Buffer
	old := observability.Default
	observability.Default = observability.NewEmitter(&buf)
	defer func() { observability.Default = old }()

	l := ratelimit.NewMemory(3, time.Hour)
	h := middleware.RateLimitOnSuccess(l, middleware.EndpointSignup)(
		okHandler(http.StatusCreated))

	do := func() int {
		req := httptest.NewRequest(http.MethodPost, "/auth/signup", nil)
		req.RemoteAddr = "203.0.113.8:4444"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}

	for i := range 3 {
		require.Equal(t, http.StatusCreated, do(), "signup %d", i+1)
	}
	require.Equal(t, http.StatusTooManyRequests, do(), "the 4th signup in an hour is limited")
}
