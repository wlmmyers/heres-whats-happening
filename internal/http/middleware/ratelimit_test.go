package middleware_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/http/middleware"
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

	require.Equal(t, []string{"203.0.113.9"}, l.recorded)
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
