package middleware

import (
	"net"
	"net/http"
	"strings"
)

// ClientIPResolver returns a middleware that normalizes r.RemoteAddr to a bare
// client IP address, replacing chi's RealIP.
//
// When trustProxy is true the address is taken from the RIGHTMOST entry of
// X-Forwarded-For — the hop our ALB appended. Every entry to its left was
// supplied by the caller and is forgeable. True-Client-IP and X-Real-IP are
// ignored entirely: nothing in our request path sets them, so honoring them
// would let any caller choose their own rate-limit bucket.
//
// When trustProxy is false (local development, tests) the host portion of
// r.RemoteAddr is used as-is.
func ClientIPResolver(trustProxy bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.RemoteAddr = resolveClientIP(r, trustProxy)
			next.ServeHTTP(w, r)
		})
	}
}

func resolveClientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			candidate := strings.TrimSpace(parts[len(parts)-1])
			if net.ParseIP(candidate) != nil {
				return candidate
			}
		}
	}
	return hostOnly(r.RemoteAddr)
}

// ClientIP returns the bare client IP for r. ClientIPResolver should be
// installed early in the chain; this stays correct either way.
func ClientIP(r *http.Request) string {
	return hostOnly(r.RemoteAddr)
}

func hostOnly(addr string) string {
	if host, _, err := net.SplitHostPort(addr); err == nil {
		return host
	}
	return addr
}
