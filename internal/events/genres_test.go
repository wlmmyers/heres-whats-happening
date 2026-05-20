package events

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizeGenre_KnownAlias(t *testing.T) {
	require.Equal(t, "rock", NormalizeGenre("Rock"))
	require.Equal(t, "rock", NormalizeGenre("ROCK"))
	require.Equal(t, "rock", NormalizeGenre("  Rock  "))
	require.Equal(t, "hip-hop", NormalizeGenre("Hip Hop"))
	require.Equal(t, "hip-hop", NormalizeGenre("Hip-Hop/Rap"))
	require.Equal(t, "rnb", NormalizeGenre("R&B"))
	require.Equal(t, "indie", NormalizeGenre("Alternative"))
	require.Equal(t, "electronic", NormalizeGenre("EDM"))
}

func TestNormalizeGenre_UnknownReturnsEmpty(t *testing.T) {
	require.Equal(t, "", NormalizeGenre("Nonexistent Genre"))
}

func TestNormalizeString(t *testing.T) {
	require.Equal(t, "phoebe bridgers", NormalizeString("Phoebe Bridgers"))
	require.Equal(t, "the bowl", NormalizeString(" The Bowl "))
	require.Equal(t, "cafe luca", NormalizeString("Café Luca"))
}
