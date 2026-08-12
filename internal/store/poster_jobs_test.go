package store_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/store"
	"github.com/wmyers/heres-whats-happening/internal/testdb"
)

// testUserID creates a fresh confirmed user and returns its id. poster_jobs.user_id
// has a foreign key to users, so every claimed job needs a real row to point at.
func testUserID(t *testing.T, q *store.Queries) pgtype.UUID {
	t.Helper()
	return newUser(t, q, t.Name()+"@example.com", true)
}

// TestPosterJobs_RejectsOverlongFieldsAtTheDatabase proves the migration 0023
// CHECK constraints are actually enforced, not merely declared in the schema
// file. The handlers reject over-long input at the edge (see
// internal/http/handlers/posters.go), so these constraints exist as a backstop
// for a writer that does not go through them.
func TestPosterJobs_RejectsOverlongFieldsAtTheDatabase(t *testing.T) {
	q := store.New(testdb.MustOpen(t))

	for _, tc := range []struct {
		name                   string
		performer, venue, date string
	}{
		{"performer", strings.Repeat("a", 201), "V", "D"},
		{"venue", "P", strings.Repeat("a", 201), "D"},
		{"date", "P", "V", strings.Repeat("a", 101)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := q.ClaimPosterJob(context.Background(), store.ClaimPosterJobParams{
				ID: "chk-" + tc.name, UserID: testUserID(t, q),
				Performer: tc.performer, Venue: tc.venue, Date: tc.date,
				StaleBefore: pgtype.Timestamptz{Time: time.Now(), Valid: true},
			})
			// The handlers reject this at the edge; the constraint is the backstop
			// for a writer that does not go through them.
			require.Error(t, err)
			require.Contains(t, err.Error(), "poster_jobs_"+tc.name+"_len")
		})
	}
}

// TestPosterJobs_AcceptsFieldsAtTheLimit is the companion boundary check: values
// exactly at the limit must not be rejected.
func TestPosterJobs_AcceptsFieldsAtTheLimit(t *testing.T) {
	q := store.New(testdb.MustOpen(t))
	_, err := q.ClaimPosterJob(context.Background(), store.ClaimPosterJobParams{
		ID: "chk-ok", UserID: testUserID(t, q),
		Performer: strings.Repeat("a", 200), Venue: strings.Repeat("b", 200),
		Date:        strings.Repeat("c", 100),
		StaleBefore: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	require.NoError(t, err)
}
