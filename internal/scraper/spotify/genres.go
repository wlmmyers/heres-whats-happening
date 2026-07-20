package spotify

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/wmyers/heres-whats-happening/internal/events"
	"github.com/wmyers/heres-whats-happening/internal/musicbrainz"
	"github.com/wmyers/heres-whats-happening/internal/store"
)

// Freshness windows for the shared artist_genre_cache.
const (
	genreOKTTL       = 90 * 24 * time.Hour
	genreNotFoundTTL = 14 * 24 * time.Hour

	// genreUserAgent identifies the app to MusicBrainz (required); includes a
	// contact per their policy.
	genreUserAgent = "heres-whats-happening/1.0 ( wlmmyers@gmail.com )"
)

// mbClient is the subset of *musicbrainz.Client the resolver uses (stubbed in tests).
type mbClient interface {
	SearchArtist(ctx context.Context, name string) (string, error)
	GetArtistGenres(ctx context.Context, mbid string) ([]musicbrainz.Genre, error)
}

// GenreResolver resolves an artist name to its genres. The adapter depends on
// this interface so tests can stub it.
type GenreResolver interface {
	Resolve(ctx context.Context, name string) []musicbrainz.Genre
}

type genreResolver struct {
	q  *store.Queries
	mb mbClient
}

// NewGenreResolver builds a resolver over the shared cache and a MusicBrainz client.
func NewGenreResolver(q *store.Queries, mb *musicbrainz.Client) GenreResolver {
	return &genreResolver{q: q, mb: mb}
}

// DefaultGenreResolver builds the production resolver with the default
// MusicBrainz host and app User-Agent.
func DefaultGenreResolver(q *store.Queries) GenreResolver {
	return &genreResolver{q: q, mb: musicbrainz.New("", genreUserAgent)}
}

// Resolve returns an artist's genres, cache-first. Best-effort: any MusicBrainz
// or DB failure logs and yields no genres for that artist rather than erroring —
// genres are a soft signal and must never abort a scrape.
func (r *genreResolver) Resolve(ctx context.Context, name string) []musicbrainz.Genre {
	key := events.NormalizeString(name)
	if key == "" {
		return nil
	}

	row, err := r.q.GetArtistGenreCache(ctx, key)
	switch {
	case err == nil:
		if fresh(row) {
			return decodeGenres(row.Genres)
		}
		// stale → fall through and re-resolve
	case errors.Is(err, pgx.ErrNoRows):
		// miss → resolve
	default:
		// Cache read failed → try a live fetch without caching.
		log.Printf("scrape spotify: genre cache read %q (continuing): %v", name, err)
		return r.fetchLive(ctx, name)
	}

	return r.resolveAndCache(ctx, key, name)
}

func fresh(row store.ArtistGenreCache) bool {
	age := time.Since(row.ResolvedAt.Time)
	switch row.Status {
	case "ok":
		return age < genreOKTTL
	case "not_found":
		return age < genreNotFoundTTL
	default:
		return false
	}
}

// resolveAndCache does a live lookup and upserts the result.
func (r *genreResolver) resolveAndCache(ctx context.Context, key, name string) []musicbrainz.Genre {
	mbid, err := r.mb.SearchArtist(ctx, name)
	if err != nil {
		log.Printf("scrape spotify: mb search %q (continuing): %v", name, err)
		return nil // transient — do not cache
	}
	if mbid == "" {
		r.upsert(ctx, key, nil, nil, "not_found")
		return nil
	}
	genres, err := r.mb.GetArtistGenres(ctx, mbid)
	if err != nil {
		log.Printf("scrape spotify: mb genres %q (continuing): %v", name, err)
		return nil // transient — do not cache
	}
	r.upsert(ctx, key, &mbid, genres, "ok")
	return genres
}

// fetchLive looks up without touching the cache (used when the cache read failed).
func (r *genreResolver) fetchLive(ctx context.Context, name string) []musicbrainz.Genre {
	mbid, err := r.mb.SearchArtist(ctx, name)
	if err != nil || mbid == "" {
		return nil
	}
	genres, err := r.mb.GetArtistGenres(ctx, mbid)
	if err != nil {
		return nil
	}
	return genres
}

func (r *genreResolver) upsert(ctx context.Context, key string, mbid *string, genres []musicbrainz.Genre, status string) {
	blob := []byte("[]")
	if len(genres) > 0 {
		b, err := json.Marshal(genres)
		if err != nil {
			log.Printf("scrape spotify: marshal genres %q (continuing): %v", key, err)
			return
		}
		blob = b
	}
	if err := r.q.UpsertArtistGenreCache(ctx, store.UpsertArtistGenreCacheParams{
		NameKey: key,
		Mbid:    mbid,
		Genres:  blob,
		Status:  status,
	}); err != nil {
		log.Printf("scrape spotify: genre cache write %q (continuing): %v", key, err)
	}
}

func decodeGenres(blob []byte) []musicbrainz.Genre {
	if len(blob) == 0 {
		return nil
	}
	var gs []musicbrainz.Genre
	if err := json.Unmarshal(blob, &gs); err != nil {
		return nil
	}
	if len(gs) == 0 {
		return nil
	}
	return gs
}
