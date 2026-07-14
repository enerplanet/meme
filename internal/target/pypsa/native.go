// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package pypsa

import (
	"encoding/json"
	"fmt"

	"github.com/enerplanet/meme/internal/emit"
)

// mergeNativeInto applies a native JSON object ({attribute: value}) onto a
// string-keyed row, coercing scalars to their emitted string form. Used by the
// CSV/columnar PyPSA emitter to turn a native block into extra columns. A
// native value may override a base column (last write wins).
func mergeNativeInto(row map[string]string, raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return fmt.Errorf("native.pypsa must be a JSON object of attribute:value pairs: %w", err)
	}
	for k, v := range m {
		row[k] = emit.AnyToString(v)
	}
	return nil
}
