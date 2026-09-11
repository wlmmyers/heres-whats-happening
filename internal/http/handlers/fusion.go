package handlers

import (
	"sort"

	"github.com/google/uuid"
)

// rrfK damps the contribution of top ranks so a single leg cannot dominate.
// 60 is the value from the original RRF paper and needs no tuning.
const rrfK = 60.0

// fuseRRF merges two ranked id lists by Reciprocal Rank Fusion:
//
//	score(id) = Σ 1 / (k + rank_in_leg)
//
// It uses ONLY the ordering of each leg, never the underlying scores. That is
// the whole point: trigram similarity and cosine distance are not on comparable
// scales, and the relevant/irrelevant cosine bands overlap across queries --
// 0.561 is a correct hit for one query while 0.556 is noise for another. No
// score cutoff can separate them, so the scores are discarded here.
//
// Ties break on first appearance in the lexical leg, then the semantic leg, so
// the result is deterministic across identical calls.
func fuseRRF(lexical, semantic []uuid.UUID, limit int) []uuid.UUID {
	type entry struct {
		id    uuid.UUID
		score float64
		order int
	}

	byID := make(map[uuid.UUID]*entry)
	next := 0

	add := func(ids []uuid.UUID) {
		for rank, id := range ids {
			e, ok := byID[id]
			if !ok {
				e = &entry{id: id, order: next}
				next++
				byID[id] = e
			}
			e.score += 1.0 / (rrfK + float64(rank+1))
		}
	}
	add(lexical)
	add(semantic)

	out := make([]*entry, 0, len(byID))
	for _, e := range byID {
		out = append(out, e)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].score != out[j].score {
			return out[i].score > out[j].score
		}
		return out[i].order < out[j].order
	})

	if limit > len(out) {
		limit = len(out)
	}
	ids := make([]uuid.UUID, 0, limit)
	for _, e := range out[:limit] {
		ids = append(ids, e.id)
	}
	return ids
}
