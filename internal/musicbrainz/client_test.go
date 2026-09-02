package musicbrainz_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
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

// The real response for artist:"Pond" (verified 2026-09-01). MusicBrainz's
// score is Lucene relevance, not name equality: it ranks the SUPERSET name
// 'Bardo Pond' (100) above the exact 'Pond' (99). Taking artists[0] is how the
// enrichment Lambda hung a Bardo Pond biography on a Pond show, and this client
// feeds genres off the same search.
const pondSearchJSON = `{"artists":[
 {"id":"2ad8bce1-8e55-48db-82ed-d98b52a3a13f","name":"Bardo Pond","score":100},
 {"id":"69ac44f7-c80d-47b4-9bc8-fcc758d209e6","name":"Pond","score":99},
 {"id":"d4a9be59-13e5-481b-8c68-833c5c1fd458","name":"matt pond PA","score":92},
 {"id":"8154377a-42a4-4fe2-a367-689a86ff07f7","name":"POND","score":92},
 {"id":"1ef504e7-2f2d-41a5-9536-64f161f54174","name":"Pond","score":90}
]}`

func serveJSON(t *testing.T, body string, query *url.Values) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if query != nil {
			*query = r.URL.Query()
		}
		_, _ = w.Write([]byte(body))
	}))
}

func TestSearchArtist_PrefersExactNameOverHigherScore(t *testing.T) {
	srv := serveJSON(t, pondSearchJSON, nil)
	defer srv.Close()

	c := musicbrainz.New(srv.URL, testUA)
	mbid, err := c.SearchArtist(context.Background(), "Pond")
	require.NoError(t, err)
	require.Equal(t, "69ac44f7-c80d-47b4-9bc8-fcc758d209e6", mbid,
		"must pick Pond, not the higher-scored Bardo Pond")
}

func TestSearchArtist_ExactMatchIgnoresCaseAndDiacritics(t *testing.T) {
	srv := serveJSON(t, pondSearchJSON, nil)
	defer srv.Close()

	c := musicbrainz.New(srv.URL, testUA)
	mbid, err := c.SearchArtist(context.Background(), "POND")
	require.NoError(t, err)
	// events.NormalizeString folds case, so the AU 'Pond' still wins on score
	// over the German 'POND' rather than matching it letter-for-letter.
	require.Equal(t, "69ac44f7-c80d-47b4-9bc8-fcc758d209e6", mbid)
}

// The fuzzy path is untouched: with no exact match, MusicBrainz's own ranking
// still decides, so misspellings resolve exactly as they did before.
func TestSearchArtist_FallsBackToTopHitWhenNoExactMatch(t *testing.T) {
	srv := serveJSON(t, pondSearchJSON, nil)
	defer srv.Close()

	c := musicbrainz.New(srv.URL, testUA)
	mbid, err := c.SearchArtist(context.Background(), "Ponnd")
	require.NoError(t, err)
	require.Equal(t, "2ad8bce1-8e55-48db-82ed-d98b52a3a13f", mbid)
}

// limit=1 cannot see past the top hit, so the exact match must not be truncated
// away before we rank. Same single request either way.
func TestSearchArtist_RequestsDeeperPoolThanOne(t *testing.T) {
	var q url.Values
	srv := serveJSON(t, pondSearchJSON, &q)
	defer srv.Close()

	c := musicbrainz.New(srv.URL, testUA)
	_, err := c.SearchArtist(context.Background(), "Pond")
	require.NoError(t, err)

	limit, err := strconv.Atoi(q.Get("limit"))
	require.NoError(t, err)
	require.GreaterOrEqual(t, limit, 25)
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

func TestSearchArtist_EscapesQuoteInName(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("query")
		_, _ = w.Write([]byte(`{"artists":[]}`))
	}))
	defer srv.Close()
	c := musicbrainz.New(srv.URL, testUA)
	_, err := c.SearchArtist(context.Background(), `The "Band"`)
	require.NoError(t, err)
	require.Equal(t, `artist:"The \"Band\""`, gotQuery)
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
