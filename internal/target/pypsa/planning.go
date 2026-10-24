// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package pypsa

import (
	"sort"

	"github.com/enerplanet/meme/internal/emit"
	"github.com/enerplanet/meme/internal/model"
)

// unmetCarrier is the carrier of the slack generators allow_unmet_demand adds,
// so their output is attributed separately from any real supply.
const unmetCarrier = "unmet"

// setModule writes PyPSA's integer module (p_nom_mod, e_nom_mod, s_nom_mod)
// for an expandable component with a per-unit size (ADR-0011): the built
// capacity is then a whole number of modules.
func setModule(row map[string]string, c *model.Capacity, column string) {
	if c != nil && c.Expandable && c.PerUnit != nil && *c.PerUnit > 0 {
		row[column] = emit.Ftoa(*c.PerUnit)
	}
}

// retirementRows applies continuous retirement (ADR-0011) to a component row:
// the existing part becomes extendable between zero and its existing
// capacity at its fixed O&M alone, so it retires what it does not earn; an
// expandable technology gets a second, new-build part above the existing
// capacity that carries the investment. A component without retirement
// comes back unchanged.
func retirementRows(m *model.Model, t model.Technology, row map[string]string) []map[string]string {
	c := t.Capacity
	if c == nil || c.DecommissionMode() != "continuous" {
		return []map[string]string{row}
	}
	fixedOM := pypsaCapitalCost(m, t) - annualizedInvestment(m, t, model.PrimaryCostClass)
	existing := row
	existing["p_nom_extendable"] = "True"
	existing["p_nom_max"] = emit.Ftoa(c.Existing)
	delete(existing, "p_nom_min")
	delete(existing, "p_nom_mod")
	existing["capital_cost"] = emit.Ftoa(fixedOM)
	if !c.Expandable {
		return []map[string]string{existing}
	}
	fresh := make(map[string]string, len(row)+2)
	for k, v := range row {
		fresh[k] = v
	}
	fresh["name"] = row["name"] + "_new"
	fresh["p_nom"] = "0"
	fresh["p_nom_extendable"] = "True"
	fresh["p_nom_max"] = "inf"
	if max := c.EffectiveMax(); max != nil {
		fresh["p_nom_max"] = emit.Ftoa(maxf(*max-c.Existing, 0))
	}
	delete(fresh, "p_nom_min")
	if min := c.EffectiveMin(); min != nil && *min > c.Existing {
		fresh["p_nom_min"] = emit.Ftoa(*min - c.Existing)
	}
	fresh["capital_cost"] = emit.Ftoa(pypsaCapitalCost(m, t))
	setModule(fresh, c, "p_nom_mod")
	return []map[string]string{existing, fresh}
}

// retirementNames lists the emitted component names of a technology instance:
// the name itself, plus its new-build part under continuous retirement when
// the technology is expandable (the series files need both columns).
func retirementNames(name string, t model.Technology) []string {
	if c := t.Capacity; c != nil && c.DecommissionMode() == "continuous" && c.Expandable {
		return []string{name, name + "_new"}
	}
	return []string{name}
}

func maxf(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

// copperHub is one carrier's copperplate: the hub bus and the nodes it joins.
type copperHub struct {
	carrier string
	bus     string
	nodes   []string
}

// copperplateHubs builds one hub per carrier that more than one node hosts
// when experiment.copperplate is set (ADR-0011): a free, unlimited,
// bidirectional link from every node's bus to the hub makes the carrier one
// balance, as a copperplate does.
func copperplateHubs(j *model.Job, pairs [][2]string) []copperHub {
	if !j.Experiment.Copperplate {
		return nil
	}
	byCarrier := map[string][]string{}
	for _, p := range pairs {
		byCarrier[p[1]] = append(byCarrier[p[1]], p[0])
	}
	var out []copperHub
	for _, carrier := range emit.Keys(byCarrier) {
		nodes := byCarrier[carrier]
		if len(nodes) < 2 {
			continue
		}
		sort.Strings(nodes)
		out = append(out, copperHub{carrier: carrier, bus: "copperplate::" + carrier, nodes: nodes})
	}
	return out
}
