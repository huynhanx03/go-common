package response

// MergeStrategy controls how conflicting keys are resolved during merging.
type MergeStrategy int

const (
	// MergeFirst keeps the value from the first source (default).
	MergeFirst MergeStrategy = iota
	// MergeLast keeps the value from the last source (overwrites).
	MergeLast
	// MergeAppend collects all values into a slice.
	MergeAppend
)

// MergeMaps combines multiple maps into one using the given strategy.
// Useful for aggregating responses from parallel data sources.
func MergeMaps(strategy MergeStrategy, sources ...map[string]any) map[string]any {
	result := make(map[string]any)

	for _, src := range sources {
		for k, v := range src {
			existing, exists := result[k]
			if !exists {
				result[k] = v
				continue
			}

			switch strategy {
			case MergeFirst:
				// Keep existing, skip
			case MergeLast:
				result[k] = v
			case MergeAppend:
				result[k] = appendToSlice(existing, v)
			}
		}
	}

	return result
}

// MergeStructs merges multiple structs-as-maps (via converter) into a single map.
// The converter turns each source into a map[string]any.
func MergeStructs[T any](strategy MergeStrategy, convert func(T) map[string]any, sources ...T) map[string]any {
	maps := make([]map[string]any, 0, len(sources))
	for _, src := range sources {
		maps = append(maps, convert(src))
	}
	return MergeMaps(strategy, maps...)
}

// appendToSlice ensures the value is accumulated as a slice.
func appendToSlice(existing, new any) []any {
	var result []any
	if slice, ok := existing.([]any); ok {
		result = slice
	} else {
		result = []any{existing}
	}
	return append(result, new)
}
