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
//
// One call is not enough to prove that. byID here holds 2 entries, which Go
// places in a single 8-slot bucket in insertion order (no hash scatter at
// this size); iteration then starts at a uniformly random offset in 0..7 and
// walks cyclically, so insertion order survives 7 of the 8 offsets and flips
// on only 1. A comparator that dropped the order-based tiebreak and fell back
// on raw map order would therefore still produce the correct answer ~87.5% of
// the time -- a single assertion only catches the regression 12.5% of the
// time, indistinguishable from a flake. Repeating across N independent calls
// (fresh map, fresh random offset each time) drives detection to 1-(7/8)^N:
// N=20 -> 93.1%, N=50 -> 99.87%, N=100 -> 99.99984%. fuseRRF is a pure
// function over two-element slices, so 100 iterations costs microseconds.
// Do not shrink this loop -- it is not redundant, it is the only thing
// standing between this test and a silent 87.5% miss rate on a reintroduced
// regression. Asserting every run against the expected slice also proves the
// runs agree with each other, so this covers both a consistently-wrong order
// and outright nondeterminism in one pass.
func TestFuseRRF_TiedScoreBreaksByFirstAppearanceOrder(t *testing.T) {
	u := ids(2)
	lexOnly, semOnly := u[0], u[1]
	want := []uuid.UUID{lexOnly, semOnly}

	const repetitions = 100 // 1-(7/8)^100 ≈ 99.99984% detection; see comment above
	for i := 0; i < repetitions; i++ {
		got := fuseRRF([]uuid.UUID{lexOnly}, []uuid.UUID{semOnly}, 10)
		require.Equal(t, want, got,
			"run %d/%d: tied at 1/61 each; lexical-leg-first must win the tie-break", i+1, repetitions)
	}
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

// No caller passes a negative limit today; this pins that a future one gets an
// empty list rather than a panic inside a request.
func TestFuseRRF_NegativeLimitReturnsEmpty(t *testing.T) {
	u := ids(3)
	require.Empty(t, fuseRRF(u, u, -1))
}
