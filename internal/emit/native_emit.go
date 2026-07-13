// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package emit

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
)

// MergeNativeMap deep-merges a native JSON object into a map[string]any
// (used by the AdOpT emitter's nested JSON files). Nested objects merge
// recursively; scalars and arrays overwrite. A native key can override an
// emitted one (last write wins).
func MergeNativeMap(dst map[string]any, raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return fmt.Errorf("native.adopt-net0 must be a JSON object: %w", err)
	}
	deepMerge(dst, m)
	return nil
}

func deepMerge(dst, src map[string]any) {
	for k, v := range src {
		if sub, ok := v.(map[string]any); ok {
			if existing, ok := dst[k].(map[string]any); ok {
				deepMerge(existing, sub)
				continue
			}
		}
		dst[k] = v
	}
}

// SortedKeys returns the keys of m in sorted order, for deterministic output.
func SortedKeys(m map[string]any) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// AnyToString renders a decoded JSON value as a CSV cell: booleans in Python
// spelling (PyPSA convention), numbers in shortest form, nil as the empty
// string, and everything else re-marshalled as JSON.
func AnyToString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case bool:
		if x {
			return "True" // PyPSA reads Python-style booleans
		}
		return "False"
	case float64:
		return strconv.FormatFloat(x, 'g', -1, 64)
	case json.Number:
		return x.String()
	default:
		b, _ := json.Marshal(x)
		return string(b)
	}
}
