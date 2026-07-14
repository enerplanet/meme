// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package calliope

import "github.com/enerplanet/meme/internal/model"

// unitSize resolves the integer-unit size for a committable tech: an explicit
// per-unit size, else the fixed base, else the upper bound.
func unitSize(c *model.Capacity) (float64, bool) {
	if c == nil {
		return 0, false
	}
	if c.PerUnit != nil && *c.PerUnit > 0 {
		return *c.PerUnit, true
	}
	if c.Existing > 0 {
		return c.Existing, true
	}
	if c.Max != nil && *c.Max > 0 {
		return *c.Max, true
	}
	return 0, false
}
