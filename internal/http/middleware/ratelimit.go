package middleware

import (
	"log"
	"math"
	"net/http"
	"strconv"
	"time"

	chimw "github.com/go-chi/chi/v5/middleware"

	"github.com/wmyers/heres-whats-happening/internal/http/httperr"
	"github.com/wmyers/heres-whats-happening/internal/ratelimit"
)

// RateLimit rejects requests over the limit before they reach the handler.
// Every request counts. name appears in log lines only.
func RateLimit(l ratelimit.Limiter, name string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !checkAllowed(w, r, l, name) {
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RateLimitOnSuccess rejects requests over the limit, then counts the request
// only if the handler returned 2xx.
//
// Signup uses this: a rejected signup (bad email, weak password, duplicate
// address) must cost the caller nothing, or one typo would lock a real user out
// for the whole window without an account to show for it.
func RateLimitOnSuccess(l ratelimit.Limiter, name string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !checkAllowed(w, r, l, name) {
				return
			}

			ww := chimw.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)

			status := ww.Status()
			if status == 0 {
				// The handler wrote nothing at all — no WriteHeader, no Write — so
				// the wrapper never observed a status. net/http still sends an
				// implicit 200 once the response completes, so treat it as one.
				status = http.StatusOK
			}
			if status < 200 || status > 299 {
				return
			}
			if err := l.Record(r.Context(), ClientIP(r)); err != nil {
				log.Printf("ratelimit %s: record failed: %v", name, err)
			}
		})
	}
}

// checkAllowed reports whether the request may proceed, writing 429 if not.
func checkAllowed(w http.ResponseWriter, r *http.Request, l ratelimit.Limiter, name string) bool {
	d, err := l.Allow(r.Context(), ClientIP(r))
	if err != nil {
		// Allow already failed open; log and honor its decision.
		log.Printf("ratelimit %s: check failed: %v", name, err)
	}
	if d.Allowed {
		return true
	}
	w.Header().Set("Retry-After", strconv.Itoa(retryAfterSeconds(d.RetryAfter)))
	httperr.Write(w, http.StatusTooManyRequests, "rate_limited",
		"too many requests, please try again later")
	return false
}

// retryAfterSeconds rounds up, never below 1 — "Retry-After: 0" would invite an
// immediate retry.
func retryAfterSeconds(d time.Duration) int {
	if d <= 0 {
		return 1
	}
	return int(math.Max(1, math.Ceil(d.Seconds())))
}
