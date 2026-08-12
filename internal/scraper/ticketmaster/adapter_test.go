package ticketmaster

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// tmPage builds a minimal Discovery API page holding one event per id. A
// non-empty nextHref is exposed as _links.next.href.
func tmPage(nextHref string, ids ...string) string {
	evs := make([]string, 0, len(ids))
	for _, id := range ids {
		evs = append(evs, fmt.Sprintf(`{
			"id": %q,
			"name": "Event %s",
			"dates": {"start": {"dateTime": "2026-06-15T20:00:00Z"}},
			"_embedded": {"venues": [{"name": "The Bowl"}]}
		}`, id, id))
	}
	links := ""
	if nextHref != "" {
		links = fmt.Sprintf(`, "_links": {"next": {"href": %q}}`, nextHref)
	}
	return fmt.Sprintf(`{"_embedded": {"events": [%s]}%s}`, strings.Join(evs, ","), links)
}

func sourceIDs(t *testing.T, srv *httptest.Server) []string {
	t.Helper()
	a := New(srv.URL, "test-key", "Brooklyn")
	msgs, err := a.Fetch(context.Background())
	require.NoError(t, err)
	ids := make([]string, 0, len(msgs))
	for _, m := range msgs {
		ids = append(ids, m.SourceEventID)
	}
	return ids
}

func TestAdapter_Fetch_ParsesSamplePage(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "sample_page.json"))
	require.NoError(t, err)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/discovery/v2/events.json", r.URL.Path)
		require.Equal(t, "test-key", r.URL.Query().Get("apikey"))
		require.Equal(t, "Brooklyn", r.URL.Query().Get("city"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	a := New(srv.URL, "test-key", "Brooklyn")
	events, err := a.Fetch(context.Background())
	require.NoError(t, err)
	require.Len(t, events, 2)

	// First event: Phoebe Bridgers
	require.Equal(t, "ticketmaster", events[0].SourceID)
	require.Equal(t, "tm-aaa", events[0].SourceEventID)
	require.Equal(t, "Phoebe Bridgers", events[0].Title)
	require.Equal(t, "Indie rock concert", events[0].Description)
	require.Equal(t, "The Bowl", events[0].Venue.Name)
	require.Equal(t, "100 Main St", events[0].Venue.Address)
	require.NotNil(t, events[0].Venue.Lat)
	require.InDelta(t, 40.7, *events[0].Venue.Lat, 0.001)
	require.ElementsMatch(t, []string{"Phoebe Bridgers", "MUNA"}, events[0].Performers)
	require.Contains(t, events[0].Genres, "rock")
	require.Contains(t, events[0].Genres, "indie")
	require.Equal(t, "https://example.com/p.jpg", events[0].ImageURL)

	// Second event: Hamilton
	require.Equal(t, "tm-bbb", events[1].SourceEventID)
	require.Equal(t, "Hamilton", events[1].Title)
	require.Contains(t, events[1].Genres, "theater")
	require.Contains(t, events[1].Genres, "musical")
}

func TestAdapter_Fetch_FollowsNextPageLinks(t *testing.T) {
	var gotPages []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		gotPages = append(gotPages, page)
		w.Header().Set("Content-Type", "application/json")
		switch page {
		case "":
			_, _ = w.Write([]byte(tmPage("/discovery/v2/events.json?city=Brooklyn&size=200&page=1", "tm-1", "tm-2")))
		case "1":
			_, _ = w.Write([]byte(tmPage("/discovery/v2/events.json?city=Brooklyn&size=200&page=2", "tm-3")))
		case "2":
			_, _ = w.Write([]byte(tmPage("", "tm-4")))
		default:
			t.Errorf("unexpected page %q", page)
		}
	}))
	defer srv.Close()

	require.Equal(t, []string{"tm-1", "tm-2", "tm-3", "tm-4"}, sourceIDs(t, srv))
	require.Equal(t, []string{"", "1", "2"}, gotPages)
}

func TestAdapter_Fetch_NextPageCarriesAPIKeyAndDropsURITemplate(t *testing.T) {
	var second url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("page") == "" {
			// Ticketmaster's next link omits the apikey and appends a URI
			// template for the params it did not expand.
			_, _ = w.Write([]byte(tmPage("/discovery/v2/events.json?city=Brooklyn&size=200&page=1{&sort}", "tm-1")))
			return
		}
		second = r.URL.Query()
		_, _ = w.Write([]byte(tmPage("", "tm-2")))
	}))
	defer srv.Close()

	require.Equal(t, []string{"tm-1", "tm-2"}, sourceIDs(t, srv))
	require.Equal(t, "test-key", second.Get("apikey"))
	require.Equal(t, "1", second.Get("page"))
	for k := range second {
		require.NotContains(t, k, "{")
		require.NotContains(t, k, "}")
	}
}

// An unexpanded {&sort} means the link carries no sort value of its own. The
// adapter has to re-pin it, or the next page comes back in Ticketmaster's
// default order and paging over a re-ordered result set silently skips events.
func TestAdapter_Fetch_NextPagePinsSortOrder(t *testing.T) {
	var second url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("page") == "" {
			_, _ = w.Write([]byte(tmPage("/discovery/v2/events.json?city=Brooklyn&size=200&page=1{&sort}", "tm-1")))
			return
		}
		second = r.URL.Query()
		_, _ = w.Write([]byte(tmPage("", "tm-2")))
	}))
	defer srv.Close()

	require.Equal(t, []string{"tm-1", "tm-2"}, sourceIDs(t, srv))
	require.Equal(t, "date,asc", second.Get("sort"))
}

func TestAdapter_Fetch_DeduplicatesAcrossPages(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("page") == "" {
			_, _ = w.Write([]byte(tmPage("/discovery/v2/events.json?page=1", "tm-1", "tm-2")))
			return
		}
		// The result set shifted between requests, so tm-2 repeats.
		_, _ = w.Write([]byte(tmPage("", "tm-2", "tm-3")))
	}))
	defer srv.Close()

	require.Equal(t, []string{"tm-1", "tm-2", "tm-3"}, sourceIDs(t, srv))
}

// Ticketmaster rejects page*size > 1000 with DIS1035. Verified against the
// live API at size=200: page=5 (page*size == 1000) succeeds and page=6 fails,
// so pages 0-5 are reachable and the adapter should request exactly six.
func TestAdapter_Fetch_StopsAtDeepPagingLimit(t *testing.T) {
	var gotPages []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		if page == "" {
			page = "0"
		}
		gotPages = append(gotPages, page)
		w.Header().Set("Content-Type", "application/json")
		// Always advertise another page; the adapter must stop itself.
		next := fmt.Sprintf("/discovery/v2/events.json?size=200&page=%d", len(gotPages))
		_, _ = w.Write([]byte(tmPage(next, fmt.Sprintf("tm-%d", len(gotPages)))))
	}))
	defer srv.Close()

	require.Len(t, sourceIDs(t, srv), 6)
	require.Equal(t, []string{"0", "1", "2", "3", "4", "5"}, gotPages)
}

func TestAdapter_Fetch_LaterPageHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") == "" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(tmPage("/discovery/v2/events.json?page=1", "tm-1")))
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"oops"}`))
	}))
	defer srv.Close()

	a := New(srv.URL, "test-key", "Brooklyn")
	_, err := a.Fetch(context.Background())
	require.ErrorContains(t, err, "500")
}

// tbdEventJSON is a real-shaped Discovery event whose start time is not set:
// no dateTime, only a localDate, interpreted in dates.timezone.
const tbdEventJSON = `{
	"id": "tm-tbd",
	"name": "Season Pass",
	"dates": {
		"start": {"localDate": "2026-03-26", "noSpecificTime": true},
		"timezone": "America/Los_Angeles"
	},
	"_embedded": {"venues": [{"name": "The Bowl"}]}
}`

func decodeEvent(t *testing.T, raw string) discoveryEvent {
	t.Helper()
	var e discoveryEvent
	require.NoError(t, json.Unmarshal([]byte(raw), &e))
	return e
}

func TestAdapter_Fetch_KeepsLocalDateOnlyEvent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"_embedded": {"events": [` + tbdEventJSON + `]}}`))
	}))
	defer srv.Close()

	a := New(srv.URL, "test-key", "Brooklyn")
	msgs, err := a.Fetch(context.Background())
	require.NoError(t, err)
	require.Len(t, msgs, 1, "an event with only a localDate must not be dropped")
	require.Equal(t, "tm-tbd", msgs[0].SourceEventID)
	require.True(t, msgs[0].TimeTBD)
}

func TestToMessage_LocalDateUsesEventTimezone(t *testing.T) {
	e := decodeEvent(t, tbdEventJSON)
	msg, ok := e.toMessage()
	require.True(t, ok)

	la, err := time.LoadLocation("America/Los_Angeles")
	require.NoError(t, err)
	require.True(t, msg.StartsAt.Equal(time.Date(2026, 3, 26, 0, 0, 0, 0, la)),
		"want midnight in the venue's zone, got %s", msg.StartsAt)
	// Parsed as UTC the event would land on the previous calendar day locally.
	require.Equal(t, 26, msg.StartsAt.In(la).Day())
	require.True(t, msg.TimeTBD)
}

// Some events omit dates.timezone but still carry it on the venue. Live data
// for Seattle had 18 of 135 TBD events in exactly this shape, all of them
// resolvable from the venue.
func TestToMessage_LocalDateFallsBackToVenueTimezone(t *testing.T) {
	e := decodeEvent(t, `{
		"id": "tm-tbd", "name": "Venue Zone Only",
		"dates": {"start": {"localDate": "2026-03-26"}},
		"_embedded": {"venues": [{"name": "Hec Ed", "timezone": "America/Los_Angeles"}]}
	}`)
	msg, ok := e.toMessage()
	require.True(t, ok)

	la, err := time.LoadLocation("America/Los_Angeles")
	require.NoError(t, err)
	require.True(t, msg.StartsAt.Equal(time.Date(2026, 3, 26, 0, 0, 0, 0, la)),
		"want midnight in the venue's zone, got %s", msg.StartsAt)
	require.True(t, msg.TimeTBD)
}

func TestToMessage_LocalDateWithoutTimezoneFallsBackToUTC(t *testing.T) {
	e := decodeEvent(t, `{
		"id": "tm-tbd", "name": "No Zone",
		"dates": {"start": {"localDate": "2026-03-26"}},
		"_embedded": {"venues": [{"name": "The Bowl"}]}
	}`)
	msg, ok := e.toMessage()
	require.True(t, ok)
	require.True(t, msg.StartsAt.Equal(time.Date(2026, 3, 26, 0, 0, 0, 0, time.UTC)))
	require.True(t, msg.TimeTBD)
}

func TestToMessage_UnknownTimezoneFallsBackToUTC(t *testing.T) {
	e := decodeEvent(t, `{
		"id": "tm-tbd", "name": "Bad Zone",
		"dates": {"start": {"localDate": "2026-03-26"}, "timezone": "Mars/Olympus_Mons"},
		"_embedded": {"venues": [{"name": "The Bowl"}]}
	}`)
	msg, ok := e.toMessage()
	require.True(t, ok, "an unresolvable zone must not drop the event")
	require.True(t, msg.StartsAt.Equal(time.Date(2026, 3, 26, 0, 0, 0, 0, time.UTC)))
}

// A bare end.localDate means the run goes through the whole of that day, so
// it resolves to the last second of it. Midnight would cut the final day off
// entirely. Live Seattle data had 5 such events, every one a multi-day run.
func TestToMessage_EndLocalDateCoversWholeFinalDay(t *testing.T) {
	e := decodeEvent(t, `{
		"id": "tm-run", "name": "Long Run",
		"dates": {
			"start": {"localDate": "2026-03-26", "noSpecificTime": true},
			"end": {"localDate": "2026-09-27", "noSpecificTime": true},
			"timezone": "America/Los_Angeles"
		},
		"_embedded": {"venues": [{"name": "The Bowl"}]}
	}`)
	msg, ok := e.toMessage()
	require.True(t, ok)
	require.NotNil(t, msg.EndsAt, "an end.localDate must produce an EndsAt")

	la, err := time.LoadLocation("America/Los_Angeles")
	require.NoError(t, err)
	require.True(t, msg.EndsAt.Equal(time.Date(2026, 9, 27, 23, 59, 59, 0, la)),
		"want the last second of the final local day, got %s", msg.EndsAt)
	require.True(t, msg.EndsAt.After(msg.StartsAt))
}

func TestToMessage_EndDateTimeWinsOverEndLocalDate(t *testing.T) {
	e := decodeEvent(t, `{
		"id": "tm-x", "name": "Timed End",
		"dates": {
			"start": {"dateTime": "2026-06-15T20:00:00Z"},
			"end": {"dateTime": "2026-06-15T23:00:00Z", "localDate": "2026-06-15"},
			"timezone": "America/Los_Angeles"
		},
		"_embedded": {"venues": [{"name": "The Bowl"}]}
	}`)
	msg, ok := e.toMessage()
	require.True(t, ok)
	require.NotNil(t, msg.EndsAt)
	require.True(t, msg.EndsAt.Equal(time.Date(2026, 6, 15, 23, 0, 0, 0, time.UTC)),
		"an exact end time must win over the local date, got %s", msg.EndsAt)
}

func TestToMessage_EndLocalDateUsesVenueTimezoneFallback(t *testing.T) {
	e := decodeEvent(t, `{
		"id": "tm-run", "name": "Venue Zone Run",
		"dates": {
			"start": {"localDate": "2026-03-26"},
			"end": {"localDate": "2026-09-27"}
		},
		"_embedded": {"venues": [{"name": "Hec Ed", "timezone": "America/Los_Angeles"}]}
	}`)
	msg, ok := e.toMessage()
	require.True(t, ok)
	require.NotNil(t, msg.EndsAt)

	la, err := time.LoadLocation("America/Los_Angeles")
	require.NoError(t, err)
	require.True(t, msg.EndsAt.Equal(time.Date(2026, 9, 27, 23, 59, 59, 0, la)),
		"want the venue zone applied to the end date, got %s", msg.EndsAt)
}

func TestToMessage_NoEndDateLeavesEndsAtNil(t *testing.T) {
	e := decodeEvent(t, `{
		"id": "tm-x", "name": "No End",
		"dates": {"start": {"dateTime": "2026-06-15T20:00:00Z"}},
		"_embedded": {"venues": [{"name": "The Bowl"}]}
	}`)
	msg, ok := e.toMessage()
	require.True(t, ok)
	require.Nil(t, msg.EndsAt)
}

func TestToMessage_NoStartDateAtAllIsDropped(t *testing.T) {
	e := decodeEvent(t, `{
		"id": "tm-x", "name": "Undated",
		"dates": {"start": {}},
		"_embedded": {"venues": [{"name": "The Bowl"}]}
	}`)
	_, ok := e.toMessage()
	require.False(t, ok)
}

func TestToMessage_ExactStartTimeIsNotMarkedTBD(t *testing.T) {
	e := decodeEvent(t, `{
		"id": "tm-x", "name": "Timed",
		"dates": {"start": {"dateTime": "2026-06-15T20:00:00Z", "localDate": "2026-06-15"},
		          "timezone": "America/Los_Angeles"},
		"_embedded": {"venues": [{"name": "The Bowl"}]}
	}`)
	msg, ok := e.toMessage()
	require.True(t, ok)
	require.False(t, msg.TimeTBD)
	require.True(t, msg.StartsAt.Equal(time.Date(2026, 6, 15, 20, 0, 0, 0, time.UTC)))
}

func TestAdapter_Fetch_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"oops"}`))
	}))
	defer srv.Close()
	a := New(srv.URL, "k", "X")
	_, err := a.Fetch(context.Background())
	require.Error(t, err)
}

func TestAdapter_Name(t *testing.T) {
	a := New("http://x", "k", "X")
	require.Equal(t, "ticketmaster", a.Name())
}
