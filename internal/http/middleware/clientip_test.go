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
