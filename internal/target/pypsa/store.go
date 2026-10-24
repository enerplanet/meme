// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package pypsa

import (
	"github.com/enerplanet/meme/internal/emit"
	"github.com/enerplanet/meme/internal/model"
)

// storeSpec is one Store topology in the _storage.json sidecar: the names of
// the Store and its two Links, and the structural constraints run.py applies
// to the optimization model — equal charger and discharger power when the
// power capacity is a decision, and the rate bounds of link power against
// store energy (ADR-0011).
type storeSpec struct {
	Name          string   `json:"name"`
	Store         string   `json:"store"`
	Charge        string   `json:"charge"`
	Discharge     string   `json:"discharge"`
	EqualPower    bool     `json:"equal_power"`
	ChargeRate    *float64 `json:"charge_rate,omitempty"`
	DischargeRate *float64 `json:"discharge_rate,omitempty"`
}

// storageCarrier is the carrier a storage technology connects to.
func storageCarrier(t model.Technology) string {
	if c := t.CarrierOut.First(); c != "" {
		return c
	}
	return t.CarrierIn.First()
}

// storeBusID names the bus a Store sits on, between its charger and its
// discharger: the grid bus of the node and carrier, suffixed by the component.
func storeBusID(node, carrier, name string) string { return BusID(node, carrier) + "::" + name }

// storeTopology renders an energy-sized storage as a Store plus two Links.
// Energy sizing, its bounds, its cost (annualized investment_per_energy_
// capacity), the depth of discharge (e_min_pu), the initial state of a fixed
// capacity (e_initial), the cyclic flag and the standing loss sit on the
// Store; power sizing with its bounds on both Links, the power investment,
// fixed O&M and variable cost on the discharger; the efficiencies on their
// sides. The spec carries what the CSV tables cannot say.
func storeTopology(m *model.Model, name, node string, t model.Technology) (store, charge, discharge map[string]string, spec storeSpec) {
	s := t.Storage
	carrier := storageCarrier(t)
	grid := BusID(node, carrier)
	bus := storeBusID(node, carrier, name)
	ec := s.EnergyCapacity

	store = map[string]string{
		"name": name, "bus": bus, "carrier": carrier,
		"e_nom": capNom(ec), "e_nom_extendable": capExt(ec), "e_nom_max": capMax(ec),
		"e_cyclic": emit.BoolPy(s.Cyclic), "standing_loss": emit.OptFloat(s.SelfDischarge, "0"),
		"capital_cost": emit.Ftoa(energyCapitalCost(m, t)), "marginal_cost": "0",
	}
	setModule(store, ec, "e_nom_mod")
	if ec.Expandable {
		if min := ec.EffectiveMin(); min != nil {
			store["e_nom_min"] = emit.Ftoa(*min)
		}
	} else if s.InitialSOC != nil {
		store["e_initial"] = emit.Ftoa(*s.InitialSOC * ec.Existing)
	}
	if s.DepthOfDischarge != nil {
		store["e_min_pu"] = emit.Ftoa(*s.DepthOfDischarge)
	}

	power := func(linkName, bus0, bus1 string, eff *float64, capital float64, marginalCost string) map[string]string {
		row := map[string]string{
			"name": linkName, "bus0": bus0, "bus1": bus1,
			"p_nom": capNom(t.Capacity), "p_nom_extendable": capExt(t.Capacity), "p_nom_max": capMax(t.Capacity),
			"efficiency": emit.OptFloat(eff, "1"), "capital_cost": emit.Ftoa(capital), "marginal_cost": marginalCost,
		}
		setPNomMin(row, t.Capacity)
		setModule(row, t.Capacity, "p_nom_mod")
		if !t.IsActive() {
			row["active"] = "False"
		}
		return row
	}
	charge = power(name+"_charge", grid, bus, s.ChargeEff, 0, "0")
	discharge = power(name+"_discharge", bus, grid, s.DischargeEff, pypsaCapitalCost(m, t), marginal(t))
	if !t.IsActive() {
		store["active"] = "False"
	}

	spec = storeSpec{
		Name: name, Store: name, Charge: name + "_charge", Discharge: name + "_discharge",
		EqualPower:    t.Capacity != nil && t.Capacity.Expandable,
		ChargeRate:    s.MaxChargeRate,
		DischargeRate: s.MaxDischargeRate,
	}
	return store, charge, discharge, spec
}

// energyCapitalCost is the annualized investment per MWh of a storage's
// energy capacity, on the same basis as the technology's other costs.
func energyCapitalCost(m *model.Model, t model.Technology) float64 {
	c, ok := t.Costs[model.PrimaryCostClass]
	if !ok || c.InvestmentPerEnergyCapacity == nil {
		return 0
	}
	if t.CostBasis == model.CostAnnualized {
		return *c.InvestmentPerEnergyCapacity
	}
	life, rate := 0.0, 0.0
	if t.Lifetime != nil {
		life = *t.Lifetime
	}
	if r := m.EffectiveRate(t); r != nil {
		rate = *r
	}
	return emit.AnnualizedCapex(*c.InvestmentPerEnergyCapacity, rate, life)
}
