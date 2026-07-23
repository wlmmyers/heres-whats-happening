package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/http/middleware"
)

// resolve runs the middleware over a request and reports the IP the chain saw.
func resolve(t *testing.T, trustProxy bool, remoteAddr string, headers map[string]string) string {
	t.Helper()
	var got string
	h := middleware.ClientIPResolver(trustProxy)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = middleware.ClientIP(r)
	}))
	req := httptest.NewRequest(http.MethodPost, "/auth/signup", nil)
	req.RemoteAddr = remoteAddr
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	h.ServeHTTP(httptest.NewRecorder(), req)
	return got
}

func TestClientIP_RightmostXFFWins(t *testing.T) {
	// The ALB appends the real peer; everything left of it came from the caller.
	got := resolve(t, true, "10.0.0.5:443", map[string]string{
		"X-Forwarded-For": "1.2.3.4, 203.0.113.9",
	})
	require.Equal(t, "203.0.113.9", got)
}

// The regression that motivates this task: a forged header must not mint a fresh
// rate-limit bucket.
func TestClientIP_IgnoresSpoofableHeaders(t *testing.T) {
	got := resolve(t, true, "10.0.0.5:443", map[string]string{
		"True-Client-IP":  "9.9.9.9",
		"X-Real-IP":       "8.8.8.8",
		"X-Forwarded-For": "203.0.113.9",
	})
	require.Equal(t, "203.0.113.9", got)
}

func TestClientIP_TrueClientIPAloneIsIgnored(t *testing.T) {
	got := resolve(t, true, "203.0.113.9:443", map[string]string{
		"True-Client-IP": "9.9.9.9",
	})
	require.Equal(t, "203.0.113.9", got)
}

func TestClientIP_NoProxyTrustIgnoresXFF(t *testing.T) {
	got := resolve(t, false, "203.0.113.9:443", map[string]string{
		"X-Forwarded-For": "1.2.3.4",
	})
	require.Equal(t, "203.0.113.9", got)
}

func TestClientIP_FallsBackToRemoteAddr(t *testing.T) {
	got := resolve(t, true, "203.0.113.9:443", nil)
	require.Equal(t, "203.0.113.9", got)
}

// A garbage rightmost entry must fall back to the peer, never to a constant —
// a constant would collapse every caller into one shared bucket.
func TestClientIP_UnparseableXFFFallsBack(t *testing.T) {
	got := resolve(t, true, "203.0.113.9:443", map[string]string{
		"X-Forwarded-For": "not-an-ip",
	})
	require.Equal(t, "203.0.113.9", got)
}

// Repeated X-Forwarded-For header lines are exposed by Go as a []string; the
// resolve helper's Set-based map can't express that, so this test builds the
// request directly. If a proxy fails to coalesce multiple header lines into
// one, r.Header.Get would silently return only the first line — the
// attacker's untouched value. The resolver must instead use the rightmost
// entry of the LAST header line, so the forged first line never wins.
func TestClientIP_RepeatedXFFHeadersUsesLastLine(t *testing.T) {
	var got string
	h := middleware.ClientIPResolver(true)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = middleware.ClientIP(r)
	}))
	req := httptest.NewRequest(http.MethodPost, "/auth/signup", nil)
	req.RemoteAddr = "10.0.0.5:443"
	req.Header.Add("X-Forwarded-For", "1.2.3.4")
	req.Header.Add("X-Forwarded-For", "203.0.113.9")
	h.ServeHTTP(httptest.NewRecorder(), req)
	require.Equal(t, "203.0.113.9", got)
	require.NotEqual(t, "1.2.3.4", got)
}
