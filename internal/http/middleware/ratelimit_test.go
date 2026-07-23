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

// A handler that writes a body without an explicit WriteHeader implies 200.
func TestRateLimitOnSuccess_RecordsOnImplicit200(t *testing.T) {
	l := &stubLimiter{decision: ratelimit.Decision{Allowed: true}}
	h := middleware.RateLimitOnSuccess(l, "signup")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/auth/signup", nil))

	require.Len(t, l.recorded, 1)
}
