package http

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// When TrustProxy is unset, Run must surface a warning: keying on RemoteAddr
// behind the ALB collapses every caller into one bucket and throttles the whole
// site, so a deploy that forgot TRUST_PROXY=true has to be loud, not silent.
func TestTrustProxyWarning_WarnsWhenUnset(t *testing.T) {
	s := &Server{TrustProxy: false}
	w := s.trustProxyWarning()
	require.NotEmpty(t, w, "an unset TrustProxy must produce a startup warning")
	require.Contains(t, w, "TRUST_PROXY")
	require.True(t, strings.HasPrefix(w, "WARNING"), "the line must be recognizable as a warning")
}

func TestTrustProxyWarning_SilentWhenSet(t *testing.T) {
	s := &Server{TrustProxy: true}
	require.Empty(t, s.trustProxyWarning(), "trusting the proxy is the correct prod state — no warning")
}
