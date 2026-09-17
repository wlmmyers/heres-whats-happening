package db_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/testdb"
)

// The whole accent design rests on this. Untreated, pg_trgm scores
// ('eden munoz', 'Edén Muñoz') at 0.294 -- under the default 0.3 threshold AND
// under the 0.2 this app uses -- so the event is unreachable without accent
// keys. immutable_unaccent folds both sides to 1.0.
func TestImmutableUnaccent_FoldsAccentsForTrigramMatching(t *testing.T) {
	pool := testdb.MustOpen(t)
	ctx := context.Background()

	var raw float32
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT similarity('eden munoz', 'Edén Muñoz')`).Scan(&raw))
	require.Less(t, raw, float32(0.3), "precondition: raw accented compare is below threshold")

	var folded float32
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT similarity(immutable_unaccent('eden munoz'), immutable_unaccent('Edén Muñoz'))`).
		Scan(&folded))
	require.Equal(t, float32(1), folded)
}

// unaccent() is STABLE and cannot appear in an index expression; the wrapper
// must be IMMUTABLE or every index in the migration fails to create.
func TestImmutableUnaccent_IsImmutable(t *testing.T) {
	pool := testdb.MustOpen(t)
	// Cast to text: pgx v5's codec for Postgres's internal "char" type only
	// scans into *byte/*rune, not *string.
	var volatility string
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT provolatile::text FROM pg_proc WHERE proname = 'immutable_unaccent'`).Scan(&volatility))
	require.Equal(t, "i", volatility, "provolatile must be 'i' (immutable)")
}

func TestSearchIndexesExist(t *testing.T) {
	pool := testdb.MustOpen(t)
	for _, name := range []string{
		"events_title_trgm", "event_performers_name_trgm", "venues_name_trgm",
	} {
		var exists bool
		require.NoError(t, pool.QueryRow(context.Background(),
			`SELECT EXISTS (SELECT 1 FROM pg_class WHERE relname = $1)`, name).Scan(&exists))
		require.True(t, exists, "index %s missing", name)
	}
}

// The pool sets this on every connection; 0.3 would silently lose typo
// tolerance for short queries.
func TestPoolSetsSimilarityThreshold(t *testing.T) {
	pool := testdb.MustOpen(t)
	// current_setting(...)::float8 rather than SHOW: SHOW always returns text,
	// and pgx v5's TextCodec does not auto-convert into *float64.
	var threshold float64
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT current_setting('pg_trgm.similarity_threshold')::float8`).Scan(&threshold))
	require.InDelta(t, 0.2, threshold, 0.0001)
}
