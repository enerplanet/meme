// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package pypsa

import "github.com/enerplanet/meme/internal/model"

// pypsaGlobalType maps a single-variable constraint to a PyPSA GlobalConstraint
// type, if one fits. ok=false means the constraint must be handled generically
// (the run.py sidecar applies it as linopy constraints with full tech/node
// scoping). A GlobalConstraint always sums over EVERY component of a carrier,
// so any tech- or node-scoped term must NOT be projected — it would silently
// widen the constraint's scope.
func globalType(c model.Constraint) (gcType, carrierAttr string, ok bool) {
	if len(c.Terms) == 0 {
		return "", "", false
	}
	kind := c.Terms[0].Variable
	for _, t := range c.Terms {
		if t.Variable != kind || t.Coefficient != 1 {
			return "", "", false // mixed variables or weights -> generic
		}
		if len(t.Techs) > 0 || len(t.Nodes) > 0 {
			return "", "", false // scoped terms -> generic (scope-exact)
		}
	}
	switch kind {
	case model.VarEmissions:
		attr := "co2_emissions"
		if len(c.Terms[0].Carriers) == 1 {
			attr = c.Terms[0].Carriers[0]
		}
		return "primary_energy", attr, true
	case model.VarFlowOut:
		if len(c.Terms[0].Carriers) == 1 {
			return "operational_limit", c.Terms[0].Carriers[0], true
		}
	case model.VarCapacity:
		if len(c.Terms[0].Carriers) == 1 {
			return "tech_capacity_expansion_limit", c.Terms[0].Carriers[0], true
		}
	}
	return "", "", false
}
