package testdb

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestTruncateAllCoversEveryTable guards against the failure mode that has bitten
// this repo twice already (artist_genre_cache, then rate_limit_events): a new
// migration adds a table, truncateTables isn't updated to match, and rows from
// one test leak into the next, surfacing as an intermittent, confusing failure
// in some unrelated test rather than a clear error here.
//
// It queries the live test database for every table actually in the public
// schema and requires each one to be either in truncateTables or in the
// documented referenceTables exclusion set. If it fails, add the named
// table(s) to truncateTables in internal/testdb/testdb.go.
func TestTruncateAllCoversEveryTable(t *testing.T) {
	pool := MustOpen(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	rows, err := pool.Query(ctx, "SELECT tablename FROM pg_tables WHERE schemaname = 'public'")
	require.NoError(t, err)
	defer rows.Close()

	inList := make(map[string]bool, len(truncateTables))
	for _, tbl := range truncateTables {
		inList[tbl] = true
	}

	var missing []string
	for rows.Next() {
		var tbl string
		require.NoError(t, rows.Scan(&tbl))
		if referenceTables[tbl] || inList[tbl] {
			continue
		}
		missing = append(missing, tbl)
	}
	require.NoError(t, rows.Err())

	sort.Strings(missing)
	require.Empty(t, missing,
		"table(s) %v exist in the database but are missing from testdb.truncateTables "+
			"(internal/testdb/testdb.go) — add them there so their rows are cleared "+
			"between tests, or to referenceTables if they hold fixed seed data no test "+
			"ever writes to", missing)
}
