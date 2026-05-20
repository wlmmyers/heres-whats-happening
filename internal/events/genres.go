package events

import (
	"strings"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// genreAliases maps lowercase raw source tags to our canonical slug.
// Unrecognized tags map to "" (caller should drop).
var genreAliases = map[string]string{
	// Music — direct
	"rock":             "rock",
	"pop":              "pop",
	"hip-hop":          "hip-hop",
	"hip hop":          "hip-hop",
	"hip-hop/rap":      "hip-hop",
	"rap":              "hip-hop",
	"electronic":       "electronic",
	"edm":              "electronic",
	"dance/electronic": "electronic",
	"jazz":             "jazz",
	"classical":        "classical",
	"folk":             "folk",
	"country":          "country",
	"metal":            "metal",
	"hard rock":        "metal",
	"indie":            "indie",
	"alternative":      "indie",
	"r&b":              "rnb",
	"rnb":              "rnb",
	"latin":            "latin",
	"world":            "world",
	"blues":            "blues",
	"reggae":           "reggae",
	// Non-music
	"theater":       "theater",
	"theatre":       "theater",
	"musical":       "musical",
	"comedy":        "comedy",
	"dance":         "dance",
	"opera":         "opera",
	"film":          "film",
	"sports":        "sports",
	"food":          "food",
	"art":           "art",
	"family":        "family",
	"other":         "other",
	"miscellaneous": "other",
}

// NormalizeGenre maps a source tag (e.g., "Hip-Hop/Rap") into our controlled
// vocabulary slug ("hip-hop"). Returns "" if the tag is unrecognized.
func NormalizeGenre(s string) string {
	key := strings.ToLower(strings.TrimSpace(s))
	return genreAliases[key]
}

// NormalizeString returns a lowercased, trimmed, diacritic-stripped form
// suitable for comparison (e.g., venue and performer normalized_name columns).
func NormalizeString(s string) string {
	t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	out, _, _ := transform.String(t, s)
	return strings.ToLower(strings.TrimSpace(out))
}
