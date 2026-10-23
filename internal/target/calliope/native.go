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

// mergeNativeDeepIntoYAML is the recursive variant used by the Calliope 0.6
// emitter, whose tech/location mappings nest (essentials / constraints /
// costs): a native object is merged key by key into an existing nested
// mapping instead of replacing it wholesale, so `{"constraints":
// {"parasitic_eff": 0.98}}` extends the emitted constraints rather than
// wiping them. Scalars and arrays still replace; a native key with no
// emitted counterpart is added.
func mergeNativeDeepIntoYAML(n *yamlNode, raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return fmt.Errorf("native[\"calliope-0.6\"] must be a JSON object: %w", err)
	}
	deepMergeMap(n, m)
	return nil
}

func deepMergeMap(n *yamlNode, m map[string]any) {
	for _, k := range emit.SortedKeys(m) {
		incoming, isMap := m[k].(map[string]any)
		if existing, ok := n.vals[k].(*yamlNode); ok && existing != nil && isMap {
			deepMergeMap(existing, incoming)
			continue
		}
		n.set(k, jsonToYAML(m[k]))
	}
}
