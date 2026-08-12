package ticketmaster

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// These tests hit the real Discovery API. They skip unless
// TICKETMASTER_API_KEY is set, so CI (which has no key) stays offline.
//
//	set -a; source .env; set +a; go test ./internal/scraper/ticketmaster/ -run Live -v

func liveConfig(t *testing.T) (key, city string) {
	t.Helper()
	key = os.Getenv("TICKETMASTER_API_KEY")
	if key == "" {
		t.Skip("TICKETMASTER_API_KEY not set; skipping live API test")
	}
	city = os.Getenv("TICKETMASTER_CITY")
	if city == "" {
		city = "Brooklyn"
	}
	return key, city
}

// redact keeps the API key out of test output, which matters because net/http
// errors quote the full request URL.
func redact(s, key string) string {
	return strings.ReplaceAll(s, key, "REDACTED")
}

// countingTransport records the page parameter of every outbound request, so
// the test can prove how many pages were actually fetched.
type countingTransport struct {
	base  http.RoundTripper
	mu    sync.Mutex
	pages []string
}

func (t *countingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	t.mu.Lock()
	page := r.URL.Query().Get("page")
	if page == "" {
		page = "0"
	}
	t.pages = append(t.pages, page)
	t.mu.Unlock()
	return t.base.RoundTrip(r)
}

// TestLive_NextHrefShape dumps the real _links.next.href so the assumptions
// nextPageURL is built on (relative path, no apikey, URI template suffix) are
// checked against the actual API rather than the docs.
func TestLive_NextHrefShape(t *testing.T) {
	key, city := liveConfig(t)

	q := url.Values{}
	q.Set("apikey", key)
	q.Set("city", city)
	q.Set("size", "200")
	q.Set("sort", "date,asc")
	q.Set("startDateTime", time.Now().UTC().Format(time.RFC3339))

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet,
		defaultBaseURL+"/discovery/v2/events.json?"+q.Encode(), nil)
	require.NoError(t, err)

	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	require.NoError(t, err, redact(errString(err), key))
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode, redact(string(body), key))

	var raw struct {
		Links map[string]struct {
			Href string `json:"href"`
		} `json:"_links"`
		Page struct {
			Size          int `json:"size"`
			TotalElements int `json:"totalElements"`
			TotalPages    int `json:"totalPages"`
			Number        int `json:"number"`
		} `json:"page"`
	}
	require.NoError(t, json.Unmarshal(body, &raw))

	t.Logf("city=%q totalElements=%d totalPages=%d size=%d number=%d",
		city, raw.Page.TotalElements, raw.Page.TotalPages, raw.Page.Size, raw.Page.Number)
	for name, l := range raw.Links {
		t.Logf("_links.%s.href = %s", name, redact(l.Href, key))
	}

	next := raw.Links["next"].Href
	if next == "" {
		t.Logf("no next link: single page of results")
		return
	}
	require.False(t, strings.HasPrefix(next, "http"), "next href is expected to be relative")
	require.NotContains(t, next, key, "next href unexpectedly carries the apikey")

	a := New("", key, city)
	resolved := a.nextPageURL(next)
	t.Logf("resolved next = %s", redact(resolved, key))
	u, err := url.Parse(resolved)
	require.NoError(t, err)
	require.Equal(t, key, u.Query().Get("apikey"), "apikey must be re-attached")
	for k, v := range u.Query() {
		require.NotContains(t, k, "{", "unexpanded URI template leaked into a param name")
		require.NotContains(t, strings.Join(v, ""), "{", "unexpanded URI template leaked into a param value")
	}
}

// TestLive_FetchPaginates runs the adapter against the real API and checks that
// it walked every advertised page, deduplicated, and returned usable events.
func TestLive_FetchPaginates(t *testing.T) {
	key, city := liveConfig(t)

	a := New("", key, city)
	tr := &countingTransport{base: http.DefaultTransport}
	a.http = &http.Client{Timeout: 30 * time.Second, Transport: tr}

	msgs, err := a.Fetch(context.Background())
	if err != nil {
		t.Fatal(redact(err.Error(), key))
	}

	t.Logf("city=%q events=%d requests=%d pages=%v", city, len(msgs), len(tr.pages), tr.pages)
	require.NotEmpty(t, msgs, "expected at least one event")
	require.LessOrEqual(t, len(tr.pages), maxPages, "must not page past the deep-paging cap")

	seen := map[string]struct{}{}
	for _, m := range msgs {
		require.NotEmpty(t, m.SourceEventID)
		require.NotEmpty(t, m.Title)
		require.False(t, m.StartsAt.IsZero(), "event %s has no start time", m.SourceEventID)
		_, dup := seen[m.SourceEventID]
		require.False(t, dup, "duplicate event %s survived deduplication", m.SourceEventID)
		seen[m.SourceEventID] = struct{}{}
	}

	if len(tr.pages) > 1 {
		require.Greater(t, len(msgs), pageSize/2,
			"multiple pages fetched but suspiciously few events collated")
	} else {
		t.Logf("only one page was available for %q; pagination path not exercised live", city)
	}

	// Events with no start time are dated in the venue's zone, so each should
	// sit at midnight in whatever zone it was parsed in -- StartsAt carries
	// that zone with it. A UTC fallback means no zone resolved (missing on
	// the payload, or no tzdata on the host), which puts the event a day
	// early for anywhere west of Greenwich.
	var tbd, utcFallback int
	for _, m := range msgs {
		if !m.TimeTBD {
			continue
		}
		tbd++
		if tbd == 1 {
			t.Logf("sample TBD event %q starts %s", m.SourceEventID, m.StartsAt.Format(time.RFC3339))
		}
		h, min, sec := m.StartsAt.Clock()
		require.Equal(t, [3]int{0, 0, 0}, [3]int{h, min, sec},
			"TBD event %s is not at midnight in its own zone: %s", m.SourceEventID, m.StartsAt)
		if m.StartsAt.Location() == time.UTC {
			utcFallback++
		}
	}
	t.Logf("TimeTBD events recovered: %d of %d (zone resolved: %d, UTC fallback: %d)",
		tbd, len(msgs), tbd-utcFallback, utcFallback)
	require.NotZero(t, tbd, "expected some time-TBD events; the fallback path went unexercised")
	require.Zero(t, utcFallback, "TBD events fell back to UTC; their local date is off by a day")

	var ends, endOfDay int
	for _, m := range msgs {
		if m.EndsAt == nil {
			continue
		}
		ends++
		require.False(t, m.EndsAt.Before(m.StartsAt),
			"event %s ends before it starts: %s -> %s", m.SourceEventID, m.StartsAt, *m.EndsAt)
		// A date-only end resolves to the last second of its closing day.
		if h, min, sec := m.EndsAt.Clock(); h == 23 && min == 59 && sec == 59 {
			endOfDay++
		}
	}
	t.Logf("events with EndsAt: %d (from a bare end.localDate: %d)", ends, endOfDay)
}

// TestLive_FetchBeatsSinglePage compares the paginating adapter against a
// single unpaginated request, which is the bug this change fixes.
func TestLive_FetchBeatsSinglePage(t *testing.T) {
	key, city := liveConfig(t)

	a := New("", key, city)
	a.http = &http.Client{Timeout: 30 * time.Second}
	msgs, err := a.Fetch(context.Background())
	if err != nil {
		t.Fatal(redact(err.Error(), key))
	}

	first, err := a.fetchPage(context.Background(), a.baseURL+"/discovery/v2/events.json?"+url.Values{
		"apikey":        {key},
		"city":          {city},
		"size":          {"200"},
		"sort":          {"date,asc"},
		"startDateTime": {time.Now().UTC().Format(time.RFC3339)},
	}.Encode())
	if err != nil {
		t.Fatal(redact(err.Error(), key))
	}

	t.Logf("single page: %d raw events; paginated: %d messages",
		len(first.Embedded.Events), len(msgs))
	require.GreaterOrEqual(t, len(msgs), len(first.Embedded.Events)-pageSize/4,
		"paginated fetch returned fewer events than one page")
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
