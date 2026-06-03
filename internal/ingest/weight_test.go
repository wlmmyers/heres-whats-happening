package ingest

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRankWeight(t *testing.T) {
	cases := []struct {
		rank int
		want float64
	}{
		{rank: 1, want: 1.0},
		{rank: 50, want: 0.6},
		{rank: 51, want: 0.6}, // clamped at the 0.6 floor past the 50-item list
		{rank: 100, want: 0.6},
		{rank: 0, want: 1.0},  // guard: non-positive ranks get full weight
		{rank: -5, want: 1.0}, // guard
	}
	for _, c := range cases {
		require.InDelta(t, c.want, rankWeight(c.rank), 1e-9, "rank %d", c.rank)
	}

	// Monotonic non-increasing from rank 1 to 50, and every value stays within
	// [0.6, 1.0].
	prev := rankWeight(1)
	for r := 2; r <= 50; r++ {
		w := rankWeight(r)
		require.LessOrEqual(t, w, prev, "rank %d should not exceed rank %d", r, r-1)
		require.GreaterOrEqual(t, w, 0.6, "rank %d below floor", r)
		require.LessOrEqual(t, w, 1.0, "rank %d above 1.0", r)
		prev = w
	}
}

func TestRankGenreWeight(t *testing.T) {
	cases := []struct {
		rank int
		want float64
	}{
		{rank: 1, want: 1.0},
		{rank: 2, want: 0.98},
		{rank: 46, want: 0.1}, // 1.0 - 45*0.02 = 0.1, the floor
		{rank: 50, want: 0.1}, // clamped — unbounded genre list decays to floor
		{rank: 100, want: 0.1},
	}
	for _, c := range cases {
		require.InDelta(t, c.want, rankGenreWeight(c.rank), 1e-9, "rank %d", c.rank)
	}

	// Monotonic non-increasing, every value within [0.1, 1.0].
	prev := rankGenreWeight(1)
	for r := 2; r <= 100; r++ {
		w := rankGenreWeight(r)
		require.LessOrEqual(t, w, prev, "rank %d should not exceed rank %d", r, r-1)
		require.GreaterOrEqual(t, w, 0.1, "rank %d below floor", r)
		prev = w
	}
}
