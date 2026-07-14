// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package pypsa

import (
	"github.com/enerplanet/meme/internal/emit"
	"github.com/enerplanet/meme/internal/model"
)

// annualizedInvestment resolves a technology's per-capacity investment for a
// cost class into an annualized figure, honoring CostBasis and the global
// discount rate (which overrides the component interest rate).
func annualizedInvestment(m *model.Model, t model.Technology, class string) float64 {
	c, ok := t.Costs[class]
	if !ok || c.InvestmentPerCapacity == nil {
		return 0
	}
	if t.CostBasis == model.CostAnnualized {
		return *c.InvestmentPerCapacity
	}
	life, rate := 0.0, 0.0
	if t.Lifetime != nil {
		life = *t.Lifetime
	}
	if r := m.EffectiveRate(t); r != nil {
		rate = *r
	}
	return emit.AnnualizedCapex(*c.InvestmentPerCapacity, rate, life)
}

// BusID is the synthetic PyPSA bus name for a (node, carrier) pair. The PyPSA
// data model fuses location and carrier into a single bus, so the emitter
// materializes one bus per (node, carrier) actually referenced.
func BusID(node, carrier string) string { return node + "::" + carrier }

// busGrid collects every (node, carrier) pair used by any technology or
// transmission arc — the set of PyPSA buses to create.
func busGrid(m *model.Model) map[[2]string]bool {
	grid := map[[2]string]bool{}
	mark := func(node, carrier string) {
		if node != "" && carrier != "" {
			grid[[2]string{node, carrier}] = true
		}
	}
	for _, t := range m.Technologies {
		for _, n := range t.Node {
			for _, c := range t.CarrierIn {
				mark(n, c)
			}
			for _, c := range t.CarrierOut {
				mark(n, c)
			}
			// Multi-carrier conversion declares its carriers through flows
			// (not carrier_in/out); mark those buses too, or the emitted link
			// would reference an undefined bus. Use At(n) so per-node flow
			// overrides are honored, matching what the emitters emit.
			for _, f := range t.At(n).Flows {
				mark(n, f.Carrier)
			}
		}
	}
	for _, l := range m.Transmission {
		mark(l.From, l.Carrier)
		mark(l.To, l.Carrier)
		if l.EnergyConsumption != nil {
			mark(l.From, l.EnergyConsumption.Carrier)
		}
	}
	return grid
}
