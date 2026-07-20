package spotify

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/musicbrainz"
	"github.com/wmyers/heres-whats-happening/internal/store"
	"github.com/wmyers/heres-whats-happening/internal/testdb"
)

// stubMB implements mbClient with canned responses and a call counter.
type stubMB struct {
	searchID    map[string]string
	genres      map[string][]musicbrainz.Genre
	searchErr   error
	searchCalls int
}

func (s *stubMB) SearchArtist(ctx context.Context, name string) (string, error) {
	s.searchCalls++
	if s.searchErr != nil {
		return "", s.searchErr
	}
	return s.searchID[name], nil
}

func (s *stubMB) GetArtistGenres(ctx context.Context, mbid string) ([]musicbrainz.Genre, error) {
	return s.genres[mbid], nil
}

func backdate(t *testing.T, pool *pgxpool.Pool, key string, at time.Time) {
	t.Helper()
	_, err := pool.Exec(context.Background(),
		"UPDATE artist_genre_cache SET resolved_at = $1 WHERE name_key = $2", at, key)
	require.NoError(t, err)
}

func TestResolve_MissFetchesThenCacheHitSkipsClient(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()

	mb := &stubMB{
		searchID: map[string]string{"Phoebe Bridgers": "mbid-pb"},
		genres:   map[string][]musicbrainz.Genre{"mbid-pb": {{Name: "indie folk", Count: 9}}},
	}
	r := &genreResolver{q: q, mb: mb}

	got := r.Resolve(ctx, "Phoebe Bridgers")
	require.Equal(t, []musicbrainz.Genre{{Name: "indie folk", Count: 9}}, got)
	require.Equal(t, 1, mb.searchCalls)

	// Second call served from cache — client not touched again.
	got = r.Resolve(ctx, "Phoebe Bridgers")
	require.Equal(t, []musicbrainz.Genre{{Name: "indie folk", Count: 9}}, got)
	require.Equal(t, 1, mb.searchCalls)
}

func TestResolve_NotFoundIsNegativeCached(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()

	mb := &stubMB{searchID: map[string]string{}} // no match for anyone
	r := &genreResolver{q: q, mb: mb}

	require.Nil(t, r.Resolve(ctx, "Nonexistent Band 9000"))
	require.Equal(t, 1, mb.searchCalls)

	// Within the 14-day window → served from the negative cache.
	require.Nil(t, r.Resolve(ctx, "Nonexistent Band 9000"))
	require.Equal(t, 1, mb.searchCalls)
}

func TestResolve_StaleOKRefetches(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()

	mb := &stubMB{
		searchID: map[string]string{"Radiohead": "mbid-rh"},
		genres:   map[string][]musicbrainz.Genre{"mbid-rh": {{Name: "art rock", Count: 5}}},
	}
	r := &genreResolver{q: q, mb: mb}

	require.NotNil(t, r.Resolve(ctx, "Radiohead"))
	require.Equal(t, 1, mb.searchCalls)

	// Age the row past the 90-day OK TTL → next call re-resolves.
	backdate(t, pool, "radiohead", time.Now().Add(-91*24*time.Hour))
	require.NotNil(t, r.Resolve(ctx, "Radiohead"))
	require.Equal(t, 2, mb.searchCalls)
}

func TestResolve_CacheOnly_MissReturnsNilWithoutClient(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()

	mb := &stubMB{searchID: map[string]string{"Phoebe Bridgers": "mbid-pb"}}
	r := &genreResolver{q: q, mb: mb, cacheOnly: true}

	// Cold cache → cache-only returns nil and never touches the client.
	require.Nil(t, r.Resolve(ctx, "Phoebe Bridgers"))
	require.Equal(t, 0, mb.searchCalls)
}

func TestResolve_CacheOnly_ServesFreshHit(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()

	// Warm the cache with a live resolver, then read it cache-only.
	live := &genreResolver{q: q, mb: &stubMB{
		searchID: map[string]string{"Radiohead": "mbid-rh"},
		genres:   map[string][]musicbrainz.Genre{"mbid-rh": {{Name: "art rock", Count: 5}}},
	}}
	require.NotNil(t, live.Resolve(ctx, "Radiohead"))

	coStub := &stubMB{}
	co := &genreResolver{q: q, mb: coStub, cacheOnly: true}
	got := co.Resolve(ctx, "Radiohead")
	require.Equal(t, []musicbrainz.Genre{{Name: "art rock", Count: 5}}, got)
	require.Equal(t, 0, coStub.searchCalls)
}

func TestResolve_TransientSearchErrorNotCached(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()

	mb := &stubMB{searchErr: context.DeadlineExceeded}
	r := &genreResolver{q: q, mb: mb}

	require.Nil(t, r.Resolve(ctx, "Boygenius"))
	// Nothing cached → a retry hits the client again.
	require.Nil(t, r.Resolve(ctx, "Boygenius"))
	require.Equal(t, 2, mb.searchCalls)
}
