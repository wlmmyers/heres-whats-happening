package musicbrainz_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/musicbrainz"
)

const testUA = "hwh-test/1.0 ( test@example.com )"

func TestSearchArtist_ReturnsTopMBID(t *testing.T) {
	var gotUA, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		gotQuery = r.URL.Query().Get("query")
		require.Equal(t, "/ws/2/artist", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"artists":[{"id":"96855c21-b832-4366-ba12-0d2330c36a86","name":"Phoebe Bridgers","score":100}]}`))
	}))
	defer srv.Close()

	c := musicbrainz.New(srv.URL, testUA)
	mbid, err := c.SearchArtist(context.Background(), "Phoebe Bridgers")
	require.NoError(t, err)
	require.Equal(t, "96855c21-b832-4366-ba12-0d2330c36a86", mbid)
	require.Equal(t, testUA, gotUA)
	require.Equal(t, `artist:"Phoebe Bridgers"`, gotQuery)
}

func TestSearchArtist_NoMatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"artists":[]}`))
	}))
	defer srv.Close()

	c := musicbrainz.New(srv.URL, testUA)
	mbid, err := c.SearchArtist(context.Background(), "Nonexistent Band 9000")
	require.NoError(t, err)
	require.Equal(t, "", mbid)
}

func TestGetArtistGenres_ParsesCounts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/ws/2/artist/96855c21-b832-4366-ba12-0d2330c36a86", r.URL.Path)
		require.Equal(t, "genres", r.URL.Query().Get("inc"))
		_, _ = w.Write([]byte(`{"genres":[{"name":"indie folk","count":9},{"name":"indie rock","count":8}]}`))
	}))
	defer srv.Close()

	c := musicbrainz.New(srv.URL, testUA)
	gs, err := c.GetArtistGenres(context.Background(), "96855c21-b832-4366-ba12-0d2330c36a86")
	require.NoError(t, err)
	require.Equal(t, []musicbrainz.Genre{{Name: "indie folk", Count: 9}, {Name: "indie rock", Count: 8}}, gs)
}

func TestGet_Non2xxIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("rate limited"))
	}))
	defer srv.Close()

	c := musicbrainz.New(srv.URL, testUA)
	_, err := c.SearchArtist(context.Background(), "X")
	require.Error(t, err)
}
