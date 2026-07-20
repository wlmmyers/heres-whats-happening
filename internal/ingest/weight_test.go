package ingest

import (
	"testing"

	"github.com/stretchr/testify/require"
)

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
