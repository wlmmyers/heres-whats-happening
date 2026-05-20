package ticketmaster

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

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
