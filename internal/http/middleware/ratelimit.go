package middleware

import (
	"log"
	"math"
	"net/http"
	"strconv"
	"time"

	chimw "github.com/go-chi/chi/v5/middleware"

	"github.com/wmyers/heres-whats-happening/internal/http/httperr"
	"github.com/wmyers/heres-whats-happening/internal/observability"
	"github.com/wmyers/heres-whats-happening/internal/ratelimit"
)

// Rate-limit endpoint identifiers. These are the metric `endpoint` dimension
// VALUES emitted on a 429 and are mirrored as the alarm map keys in
// terraform/prod/observability.tf — keep the two in sync.
const (
	EndpointSignup  = "signup"
	EndpointLogin   = "login"
	EndpointRefresh = "refresh"
	EndpointAuthed  = "authed"
)

// keyFunc selects the rate-limit bucket key for a request.
type keyFunc func(*http.Request) string

// ipKey buckets by client IP — the right choice for routes reachable without
// authentication.
func ipKey(r *http.Request) string { return "ip:" + ClientIP(r) }

// userKey buckets by authenticated user.
//
// Keying authenticated traffic by IP fails in both directions: CGNAT and
// corporate proxies put many real users behind one address, while an attacker
// holding one account and a proxy pool defeats IP keying entirely.
//
// RequireAuth 401s before this runs, so an absent user ID means a
// middleware-ordering bug. Falling back to the client IP degrades to the
// pre-existing behavior; returning an empty key would collapse every affected
// request into a single shared bucket.
func userKey(r *http.Request) string {
	if uid, ok := UserIDFromContext(r.Context()); ok {
		return "u:" + uid.String()
	}
	log.Printf("ratelimit: no user id in context, falling back to client IP")
	return ipKey(r)
}

// RateLimit rejects requests over the limit before they reach the handler,
// keyed on client IP. Every request counts. name appears in log lines and as
// the metric's endpoint dimension.
func RateLimit(l ratelimit.Limiter, name string) func(http.Handler) http.Handler {
	return limitEvery(l, name, ipKey)
}

// RateLimitByUser is RateLimit keyed on the authenticated user instead of the
// client IP. Install it inside a RequireAuth group.
func RateLimitByUser(l ratelimit.Limiter, name string) func(http.Handler) http.Handler {
	return limitEvery(l, name, userKey)
}

// limitEvery gates on the limit and spends a token for every request that gets
// through.
func limitEvery(l ratelimit.Limiter, name string, key keyFunc) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			k := key(r)
			if !checkAllowed(w, r, l, name, k) {
				return
			}
			if err := l.Record(r.Context(), k); err != nil {
				log.Printf("ratelimit %s: record failed: %v", name, err)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RateLimitOnSuccess rejects requests over the limit, then counts the request
// only if the handler returned 2xx. Keyed on client IP.
//
// Signup uses this: a rejected signup (bad email, weak password, duplicate
// address) must cost the caller nothing, or one typo would lock a real user out
// for the whole window without an account to show for it.
//
// This requires a Limiter whose Allow does not itself consume — see the Memory
// doc comment. Pairing it with a limiter that spends on Allow silently degrades
// it to RateLimit.
func RateLimitOnSuccess(l ratelimit.Limiter, name string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			k := ipKey(r)
			if !checkAllowed(w, r, l, name, k) {
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
			if err := l.Record(r.Context(), k); err != nil {
				log.Printf("ratelimit %s: record failed: %v", name, err)
			}
		})
	}
}

// checkAllowed reports whether the request may proceed, writing 429 if not.
func checkAllowed(w http.ResponseWriter, r *http.Request, l ratelimit.Limiter, name, key string) bool {
	d, err := l.Allow(r.Context(), key)
	if err != nil {
		// Allow already failed open; log and honor its decision.
		log.Printf("ratelimit %s: check failed: %v", name, err)
	}
	if d.Allowed {
		return true
	}
	// The IP rides along as a searchable property even for user-keyed buckets —
	// it is what an incident responder pivots on. It is never a dimension.
	observability.Default.RateLimitRejection(name, ClientIP(r))
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
