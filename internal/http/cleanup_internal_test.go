package http

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/store"
	"github.com/wmyers/heres-whats-happening/internal/testdb"
)

func TestDeleteExpiredRateLimitEvents(t *testing.T) {
	q := store.New(testdb.MustOpen(t))
	s := &Server{Queries: q}
	ctx := context.Background()
	now := time.Now()

	insert := func(key string, at time.Time) {
		t.Helper()
		require.NoError(t, q.InsertRateLimitEvent(ctx, store.InsertRateLimitEventParams{
			Bucket:    "signup",
			Key:       key,
			CreatedAt: pgtype.Timestamptz{Time: at, Valid: true},
		}))
	}
	count := func(key string, since time.Time) int64 {
		t.Helper()
		n, err := q.CountRateLimitEvents(ctx, store.CountRateLimitEventsParams{
			Bucket: "signup",
			Key:    key,
			Since:  pgtype.Timestamptz{Time: since, Valid: true},
		})
		require.NoError(t, err)
		return n
	}

	insert("1.1.1.1", now)                    // fresh
	insert("2.2.2.2", now.Add(-48*time.Hour)) // past the 24h retention

	require.NoError(t, s.deleteExpiredRateLimitEvents(ctx, now))

	require.Equal(t, int64(1), count("1.1.1.1", now.Add(-time.Hour)),
		"a row inside the retention window must survive")
	require.Equal(t, int64(0), count("2.2.2.2", now.Add(-72*time.Hour)),
		"a row past retention must be deleted")
}
