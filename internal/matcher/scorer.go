package matcher

import "math"

// Score computes the match score for one (user, event) pair.
// Pure function — no I/O.
func Score(user UserProfile, event EventProfile, cfg Config) MatchScore {
	// String score — artists.
	performerSet := make(map[string]string, len(event.Performers))
	for _, p := range event.Performers {
		performerSet[p.Normalized] = p.Display
	}
	var artistScore float64
	var matchedPerformers []string
	for _, ui := range user.SpotifyArtists {
		if display, ok := performerSet[ui.Normalized]; ok {
			artistScore += ui.Weight * cfg.ArtistFactor
			matchedPerformers = append(matchedPerformers, display)
		}
	}

	// String score — genres (Spotify genres + manual tags both match against event.Genres).
	genreSet := make(map[string]struct{}, len(event.Genres))
	for _, g := range event.Genres {
		genreSet[g] = struct{}{}
	}
	var genreScore float64
	matchedGenresSet := make(map[string]struct{})
	for _, src := range [][]NormalizedInterest{user.SpotifyGenres, user.ManualTags} {
		for _, ui := range src {
			key := ui.Value
			if _, ok := genreSet[key]; ok {
				genreScore += ui.Weight * cfg.GenreFactor
				matchedGenresSet[key] = struct{}{}
			}
		}
	}
	matchedGenres := make([]string, 0, len(matchedGenresSet))
	for g := range matchedGenresSet {
		matchedGenres = append(matchedGenres, g)
	}

	stringScore := (artistScore + genreScore) / cfg.StringMax
	if stringScore > 1.0 {
		stringScore = 1.0
	} else if stringScore < 0 {
		stringScore = 0
	}

	// Embedding score.
	var embedScore float64
	if len(user.Embedding) > 0 && len(event.Embedding) == len(user.Embedding) {
		cs := cosineSimilarity(user.Embedding, event.Embedding)
		embedScore = (cs + 1.0) / 2.0
	}

	total := cfg.WString*stringScore + cfg.WEmbedding*embedScore

	return MatchScore{
		StringScore:       stringScore,
		EmbeddingScore:    embedScore,
		TotalScore:        total,
		MatchedPerformers: matchedPerformers,
		MatchedGenres:     matchedGenres,
	}
}

func cosineSimilarity(a, b []float32) float64 {
	if len(a) == 0 || len(b) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}
