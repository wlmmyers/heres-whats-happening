package matcher

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildEventText_AllFields(t *testing.T) {
	in := EventText{
		Title:       "Phoebe Bridgers Live",
		Performers:  []string{"Phoebe Bridgers", "MUNA"},
		Genres:      []string{"indie", "rock"},
		Description: "Indie rock concert at the bowl",
	}
	got := BuildEventText(in)
	require.Equal(t, "Phoebe Bridgers Live — Phoebe Bridgers, MUNA. indie, rock. Indie rock concert at the bowl", got)
}

func TestBuildEventText_TruncatesDescription(t *testing.T) {
	desc := strings.Repeat("a", 600)
	in := EventText{Title: "T", Performers: []string{"P"}, Genres: []string{"g"}, Description: desc}
	got := BuildEventText(in)
	require.LessOrEqual(t, len(got), 600)
}

func TestBuildEventText_OmitsEmptyParts(t *testing.T) {
	in := EventText{Title: "Just A Title"}
	got := BuildEventText(in)
	require.Equal(t, "Just A Title", got)
}

func TestBuildUserText_AllSections(t *testing.T) {
	in := UserText{
		TopArtists: []string{"Phoebe Bridgers", "MUNA", "Big Thief"},
		TopGenres:  []string{"indie rock", "indie pop"},
		ManualTags: []string{"theater", "comedy"},
	}
	got := BuildUserText(in)
	require.Contains(t, got, "Top artists: Phoebe Bridgers, MUNA, Big Thief")
	require.Contains(t, got, "Top genres: indie rock, indie pop")
	require.Contains(t, got, "Interests: theater, comedy")
}

func TestBuildUserText_OnlyTagsPresent(t *testing.T) {
	in := UserText{ManualTags: []string{"jazz"}}
	got := BuildUserText(in)
	require.Equal(t, "Interests: jazz", got)
}

func TestBuildUserText_Empty(t *testing.T) {
	got := BuildUserText(UserText{})
	require.Equal(t, "", got)
}
