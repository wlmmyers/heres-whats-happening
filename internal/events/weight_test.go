package events_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/events"
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
		require.InDelta(t, c.want, events.RankWeight(c.rank), 1e-9, "rank %d", c.rank)
	}

	prev := events.RankWeight(1)
	for r := 2; r <= 50; r++ {
		w := events.RankWeight(r)
		require.LessOrEqual(t, w, prev, "rank %d should not exceed rank %d", r, r-1)
		require.GreaterOrEqual(t, w, 0.6, "rank %d below floor", r)
		require.LessOrEqual(t, w, 1.0, "rank %d above 1.0", r)
		prev = w
	}
}
