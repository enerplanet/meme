// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package calliope

import (
	"encoding/json"
	"fmt"

	"github.com/enerplanet/meme/internal/emit"
)

// mergeNativeIntoYAML merges a native JSON object into a Calliope yamlNode
// (tech- or node-level passthrough). Nested objects become nested yamlNodes;
// arrays and scalars pass through. Keys are applied in sorted order for
// deterministic output; a native key overrides an emitted one (last write wins).
func mergeNativeIntoYAML(n *yamlNode, raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return fmt.Errorf("native.calliope must be a JSON object: %w", err)
	}
	for _, k := range emit.SortedKeys(m) {
		n.set(k, jsonToYAML(m[k]))
	}
	return nil
}

// jsonToYAML converts a decoded JSON value into a yamlNode-writable value.
func jsonToYAML(v any) any {
	switch x := v.(type) {
	case map[string]any:
		sub := newYAML()
		for _, k := range emit.SortedKeys(x) {
			sub.set(k, jsonToYAML(x[k]))
		}
		return sub
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = jsonToYAML(e)
		}
		return out
	default:
		return x
	}
}

// splitModelNative splits a model-level native.calliope object into its `math`
// part (extra math) and the rest (model.yaml root keys).
func splitModelNative(raw json.RawMessage) (math, rest map[string]any, err error) {
	if len(raw) == 0 {
		return nil, nil, nil
	}
	var all map[string]any
	if err := json.Unmarshal(raw, &all); err != nil {
		return nil, nil, fmt.Errorf("must be a JSON object: %w", err)
	}
	if mv, ok := all["math"]; ok {
		mm, ok := mv.(map[string]any)
		if !ok {
			return nil, nil, fmt.Errorf("math must be an object")
		}
		math = mm
		delete(all, "math")
	}
	return math, all, nil
}

// mergeYAMLDeep merges a decoded JSON object into n: objects merge into an
// existing mapping of the same key, anything else replaces it. Keys are applied
// in sorted order for deterministic output.
func mergeYAMLDeep(n *yamlNode, m map[string]any) {
	for _, k := range emit.SortedKeys(m) {
		if sub, ok := m[k].(map[string]any); ok {
			if existing, ok := n.vals[k].(*yamlNode); ok {
				mergeYAMLDeep(existing, sub)
				continue
			}
		}
		n.set(k, jsonToYAML(m[k]))
	}
}
