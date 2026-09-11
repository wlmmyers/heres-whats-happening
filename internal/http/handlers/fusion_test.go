package handlers

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func ids(n int) []uuid.UUID {
	out := make([]uuid.UUID, n)
	for i := range out {
		out[i] = uuid.New()
	}
	return out
}

// RRF rewards consistent placement across both legs over a single strong hit --
// but only when the disagreement is wide. With k=60 the curve is nearly flat
// across the top ranks: #1-and-#3 scores 1/61+1/63 = 0.0322665, which actually
// EDGES OUT #2-and-#2 at 1/62+1/62 = 0.0322581. So a one-rank gap demonstrates
// nothing and this fixture uses a real one.
func TestFuseRRF_ConsistentPlacementBeatsSingleLegSpike(t *testing.T) {
	u := ids(32)
	agreed, spiky := u[0], u[1]
	filler := u[2:]

	// agreed: #2 in both legs. spiky: #1 lexically, #30 semantically.
	lexical := []uuid.UUID{spiky, agreed}
	semantic := []uuid.UUID{filler[0], agreed}
	semantic = append(semantic, filler[1:28]...) // pads so spiky lands at #30
	semantic = append(semantic, spiky)

	got := fuseRRF(lexical, semantic, 10)
	require.Equal(t, agreed, got[0],
		"agreed 1/62+1/62 = 0.032258 must beat spiky 1/61+1/90 = 0.027505")
}

func TestFuseRRF_IncludesResultsPresentInOnlyOneLeg(t *testing.T) {
	u := ids(2)
	got := fuseRRF([]uuid.UUID{u[0]}, []uuid.UUID{u[1]}, 10)
	require.ElementsMatch(t, []uuid.UUID{u[0], u[1]}, got)
}

// A genuine tie: both ids are #1 in exactly one leg and absent from the
// other, so each scores exactly 1/61 -- not merely close, identical, since
// both come from the same literal expression 1.0/(rrfK+1). Order-independent
// assertions (ElementsMatch) can't see a tie-break bug, so this uses Equal:
// lexical is scanned before semantic, so the lexical-only id must win.
func TestFuseRRF_TiedScoreBreaksByFirstAppearanceOrder(t *testing.T) {
	u := ids(2)
	lexOnly, semOnly := u[0], u[1]

	got := fuseRRF([]uuid.UUID{lexOnly}, []uuid.UUID{semOnly}, 10)
	require.Equal(t, []uuid.UUID{lexOnly, semOnly}, got,
		"tied at 1/61 each; lexical-leg-first must win the tie-break")
}

func TestFuseRRF_TruncatesToLimit(t *testing.T) {
	u := ids(20)
	require.Len(t, fuseRRF(u, u, 10), 10)
}

func TestFuseRRF_EmptySemanticLegPreservesLexicalOrder(t *testing.T) {
	u := ids(3)
	require.Equal(t, u, fuseRRF(u, nil, 10))
}

func TestFuseRRF_EmptyLexicalLegPreservesSemanticOrder(t *testing.T) {
	u := ids(3)
	require.Equal(t, u, fuseRRF(nil, u, 10))
}

func TestFuseRRF_BothLegsEmptyReturnsEmpty(t *testing.T) {
	require.Empty(t, fuseRRF(nil, nil, 10))
}

// Ties must not reorder between identical calls, or the dropdown flickers.
func TestFuseRRF_IsDeterministic(t *testing.T) {
	u := ids(6)
	lex, sem := u[:4], u[2:]
	first := fuseRRF(lex, sem, 10)
	for i := 0; i < 10; i++ {
		require.Equal(t, first, fuseRRF(lex, sem, 10))
	}
}
