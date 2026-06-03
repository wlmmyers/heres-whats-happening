package matcher

// foldDeduped appends each item from extra onto base, skipping any whose key is
// already present. Keys are supplied by the caller (already normalized), so the
// comparison is consistent across call sites. base's items take precedence — a
// track artist that duplicates a top artist is dropped rather than double-counted.
func foldDeduped[T any](base, extra []T, key func(T) string) []T {
	seen := make(map[string]struct{}, len(base)+len(extra))
	for _, b := range base {
		seen[key(b)] = struct{}{}
	}
	for _, e := range extra {
		k := key(e)
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		base = append(base, e)
	}
	return base
}
