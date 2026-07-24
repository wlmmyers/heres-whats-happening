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
// the LAST X-Forwarded-For header — the hop our ALB appended. Every entry to
// its left, in every header line, was supplied by the caller and is
// forgeable. Go exposes repeated headers as a []string (one entry per header
// line), and r.Header.Get only ever returns the first line; a client can
// send two X-Forwarded-For header lines and rely on Get to hand back their
// untouched first line, whose rightmost entry is attacker-controlled. Reading
// r.Header.Values and using the LAST line's rightmost entry is correct
// regardless of whether the proxy coalesces multiple header lines into one
// comma-joined value or appends its own line — the appended hop is always at
// the end of the last line. True-Client-IP and X-Real-IP are ignored
// entirely: nothing in our request path sets them, so honoring them would
// let any caller choose their own rate-limit bucket.
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
		if lines := r.Header.Values("X-Forwarded-For"); len(lines) > 0 {
			lastLine := lines[len(lines)-1]
			parts := strings.Split(lastLine, ",")
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
