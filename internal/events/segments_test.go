package events

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizeSegment_TicketmasterVocabulary(t *testing.T) {
	require.Equal(t, "music", NormalizeSegment("Music"))
	require.Equal(t, "sports", NormalizeSegment("Sports"))
	require.Equal(t, "arts-theatre", NormalizeSegment("Arts & Theatre"))
	require.Equal(t, "miscellaneous", NormalizeSegment("Miscellaneous"))
	require.Equal(t, "undefined", NormalizeSegment("Undefined"))
}

func TestNormalizeSegment_CaseAndSpaceInsensitive(t *testing.T) {
	require.Equal(t, "music", NormalizeSegment("  MUSIC  "))
	require.Equal(t, "arts-theatre", NormalizeSegment("arts & theatre"))
}

// An unrecognized segment is slugified and kept rather than dropped: the label
// is the only record that a new source category exists, and discarding it would
// make the events indistinguishable from unclassified ones.
func TestNormalizeSegment_UnknownIsSlugifiedNotDropped(t *testing.T) {
	require.Equal(t, "esports", NormalizeSegment("Esports"))
	require.Equal(t, "film-tv", NormalizeSegment("Film & TV"))
	require.Equal(t, "food-drink", NormalizeSegment("Food / Drink"))
}

func TestNormalizeSegment_EmptyStaysEmpty(t *testing.T) {
	require.Equal(t, "", NormalizeSegment(""))
	require.Equal(t, "", NormalizeSegment("   "))
	// Punctuation-only slugifies to nothing, which must not become a bare "-".
	require.Equal(t, "", NormalizeSegment("&"))
}

func TestValidSegment_ClosedSetForAPIFilter(t *testing.T) {
	for _, s := range []string{"music", "sports", "arts-theatre", "miscellaneous", "undefined"} {
		require.True(t, ValidSegment(s), "%q must be an accepted filter value", s)
	}
}

// The filter vocabulary is deliberately closed even though NormalizeSegment is
// open: a typo must 400 rather than silently return only NULL-segment events.
func TestValidSegment_RejectsUnknownAndEmpty(t *testing.T) {
	require.False(t, ValidSegment("musci"))
	require.False(t, ValidSegment("esports"))
	require.False(t, ValidSegment("Music"), "filter values are compared as canonical slugs")
	require.False(t, ValidSegment(""))
}
