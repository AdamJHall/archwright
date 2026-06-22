// Package configsrc resolves one or more config references (local path, github
// shorthand, or raw URL), recursively resolves each file's `imports:`, expands
// ${VAR} per layer, and deep-merges everything into a single *config.Config plus
// the flattened YAML bytes suitable for staging into Phase B.
//
// The merge engine works at the generic map[string]any level so it stays
// independent of the config schema: new config fields need no merge-code changes,
// only the per-field list strategy below.
//
// # Per-field merge strategy
//
//	maps                       merge recursively (key by key)
//	string slices              union + dedup, base order first, then new entries
//	structured slices (maps)   key-merge by "name" when elements carry one
//	                           (a later layer with the same name overrides that
//	                           element; new names append); without a "name" key,
//	                           replace the whole slice
//	scalars / other            over replaces base
//	!replace-tagged value      replaces the inherited value wholesale (see below)
//
// # !replace escape hatch
//
// A YAML node tagged `!replace` replaces whatever it inherits, ignoring the
// strategies above. The resolver pre-walks each layer's yaml.Node, and wraps any
// `!replace`-tagged value in a replaceMarker (stripping the tag) before merging.
// Merge unwraps the marker and substitutes its value wholesale.
package configsrc

import (
	"fmt"
)

// replaceMarker wraps a value that carried the `!replace` YAML tag. When Merge
// encounters one in the `over` layer it discards the inherited base value and
// uses marker.value verbatim (the value itself has the marker stripped, so it
// merges normally against an empty base, i.e. is taken as-is).
type replaceMarker struct {
	value any
}

// Merge deep-merges over onto base (base-first, over-wins) and returns the
// result. Inputs are not mutated.
func Merge(base, over map[string]any) (map[string]any, error) {
	merged, err := mergeValue(base, over)
	if err != nil {
		return nil, err
	}
	m, ok := merged.(map[string]any)
	if !ok {
		// Both inputs are maps, so the merge of two maps is always a map.
		return nil, fmt.Errorf("internal: merge of two maps produced %T", merged)
	}
	return m, nil
}

// mergeValue merges over onto base for a single value, dispatching by type.
func mergeValue(base, over any) (any, error) {
	// !replace: discard base, take over's value wholesale (deep-copied).
	if rm, ok := over.(replaceMarker); ok {
		return deepCopy(rm.value), nil
	}

	switch ov := over.(type) {
	case map[string]any:
		bm, ok := base.(map[string]any)
		if !ok {
			// base is a scalar/slice/absent: over's map replaces it.
			return deepCopy(ov), nil
		}
		return mergeMaps(bm, ov)
	case []any:
		bs, ok := base.([]any)
		if !ok {
			return deepCopy(ov), nil
		}
		return mergeSlices(bs, ov)
	default:
		// Scalar or anything else: over replaces base.
		return deepCopy(over), nil
	}
}

// mergeMaps merges the over map onto the base map, recursing per key.
func mergeMaps(base, over map[string]any) (map[string]any, error) {
	out := make(map[string]any, len(base)+len(over))
	for k, v := range base {
		out[k] = deepCopy(v)
	}
	for k, ov := range over {
		bv, present := base[k]
		if !present {
			// New key. Still unwrap a top-level !replace marker (taking its value).
			merged, err := mergeValue(nil, ov)
			if err != nil {
				return nil, fmt.Errorf("key %q: %w", k, err)
			}
			out[k] = merged
			continue
		}
		merged, err := mergeValue(bv, ov)
		if err != nil {
			return nil, fmt.Errorf("key %q: %w", k, err)
		}
		out[k] = merged
	}
	return out, nil
}

// mergeSlices applies the slice strategy: union+dedup for string slices,
// key-merge-by-name for slices of maps that carry a "name", replace otherwise.
func mergeSlices(base, over []any) (any, error) {
	if allStrings(base) && allStrings(over) {
		return unionStrings(base, over), nil
	}
	if structuredByName(base) && structuredByName(over) {
		return mergeByName(base, over)
	}
	// Heterogeneous, no-name structured, or any other shape: replace wholesale.
	return deepCopy(over), nil
}

// allStrings reports whether every element of s is a string (an empty slice
// qualifies, so unioning against/with an empty list still dedups).
func allStrings(s []any) bool {
	for _, v := range s {
		if _, ok := v.(string); !ok {
			return false
		}
	}
	return true
}

// unionStrings concatenates base then over, dropping duplicates, base order first.
func unionStrings(base, over []any) []any {
	out := make([]any, 0, len(base)+len(over))
	seen := make(map[string]bool, len(base)+len(over))
	for _, s := range append(append([]any{}, base...), over...) {
		str := s.(string)
		if seen[str] {
			continue
		}
		seen[str] = true
		out = append(out, str)
	}
	return out
}

// structuredByName reports whether s is a non-empty slice whose every element is
// a map carrying a non-empty string "name" key.
func structuredByName(s []any) bool {
	if len(s) == 0 {
		return false
	}
	for _, v := range s {
		m, ok := v.(map[string]any)
		if !ok {
			return false
		}
		name, ok := m["name"].(string)
		if !ok || name == "" {
			return false
		}
	}
	return true
}

// mergeByName key-merges two structured slices by their "name": a later element
// with the same name merges onto the base element (recursively); a new name
// appends. Base order is preserved; new names appear in over order at the end.
func mergeByName(base, over []any) (any, error) {
	out := make([]any, 0, len(base)+len(over))
	index := make(map[string]int, len(base)) // name -> position in out
	for _, v := range base {
		m := v.(map[string]any)
		name := m["name"].(string)
		index[name] = len(out)
		out = append(out, deepCopy(m))
	}
	for _, v := range over {
		m := v.(map[string]any)
		name := m["name"].(string)
		if pos, ok := index[name]; ok {
			merged, err := mergeMaps(out[pos].(map[string]any), m)
			if err != nil {
				return nil, fmt.Errorf("element name %q: %w", name, err)
			}
			out[pos] = merged
			continue
		}
		index[name] = len(out)
		out = append(out, deepCopy(m))
	}
	return out, nil
}

// deepCopy clones maps and slices so merge results never alias the inputs.
// Scalars are returned as-is (immutable). replaceMarker values are unwrapped
// (their tag has already been honored by the caller path) into their copied
// inner value.
func deepCopy(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[k] = deepCopy(val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = deepCopy(val)
		}
		return out
	case replaceMarker:
		return deepCopy(t.value)
	default:
		return v
	}
}
