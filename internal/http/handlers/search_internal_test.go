package handlers

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// A long paste is truncated to exactly the first searchMaxRunes runes, not
// rejected. Exercised directly against parseSearchQuery rather than through
// the HTTP layer: an HTTP-level test can only observe the status code, and
// nothing downstream rejects an over-length query string -- an untruncated
// query would just reach Postgres and most likely match nothing, returning
// 200 with zero results either way. That makes "200 OK" alone unable to tell
// a truncating implementation from a non-truncating one. Proving truncation
// requires inspecting the query string parseSearchQuery actually produced.
func TestParseSearchQuery_TruncatesToSearchMaxRunes(t *testing.T) {
	// 150 runes of a 3-byte character, well past both the floor and the
	// ceiling, and multi-byte so a byte-slicing bug (instead of rune-slicing)
	// would corrupt the cut rather than merely mis-count it.
	long := strings.Repeat("東", searchMaxRunes+50)

	q := url.Values{}
	q.Set("q", long)
	req := httptest.NewRequest(http.MethodGet, "/search/x/events?"+q.Encode(), nil)
	rec := httptest.NewRecorder()

	got, ok := parseSearchQuery(rec, req)
	require.True(t, ok)

	gotRunes := []rune(got)
	require.Len(t, gotRunes, searchMaxRunes)
	require.Equal(t, []rune(long)[:searchMaxRunes], gotRunes)
}
