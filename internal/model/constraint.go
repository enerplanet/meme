// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package model

// ---------------------------------------------------------------------------
// Per-node override merge.
// ---------------------------------------------------------------------------

// at returns the effective technology for a given node: the base technology with
// any per-node override fields applied. Emitters call this when expanding a tech
// to one instance per node, so all override logic lives here rather than in each
// emitter.
func (t Technology) At(node string) Technology {
	ov, ok := t.NodeOverrides[node]
	if !ok {
		return t
	}
	e := t // shallow copy; pointer/map fields are replaced wholesale below
	if ov.CarrierIn != nil {
		e.CarrierIn = ov.CarrierIn
	}
	if ov.CarrierOut != nil {
		e.CarrierOut = ov.CarrierOut
	}
	if ov.Efficiency != nil {
		e.Efficiency = ov.Efficiency
	}
	if ov.Capacity != nil {
		e.Capacity = ov.Capacity
	}
	if ov.Operation != nil {
		e.Operation = ov.Operation
	}
	if ov.Storage != nil {
		e.Storage = ov.Storage
	}
	if ov.DemandProfile != nil {
		e.DemandProfile = ov.DemandProfile
	}
	if ov.Area != nil {
		e.Area = ov.Area
	}
	if ov.Source != nil {
		e.Source = ov.Source
	}
	if ov.Native != nil {
		e.Native = ov.Native
	}
	if len(ov.Costs) > 0 {
		merged := make(map[string]CostClass, len(t.Costs)+len(ov.Costs))
		for k, v := range t.Costs {
			merged[k] = v
		}
		for k, v := range ov.Costs {
			merged[k] = v // override cost class replaces base of same key
		}
		e.Costs = merged
	}
	return e
}

// ---------------------------------------------------------------------------
// Portable linear-constraint IR.
//
// A Constraint is  sum_i ( coefficient_i * variable_i[scope_i] )  sense  bound.
// Variables reference canonical quantities (capacities, flows, emissions) scoped
// to sets of techs/carriers/nodes. This is the target-neutral representation of
// global/share constraints and the linear slice of custom math; each emitter
// renders it natively (PyPSA GlobalConstraint / extra_functionality, Calliope
// custom math, AdOpT constraints). Nonlinear constraints stay in Native.
// ---------------------------------------------------------------------------

// Constraint is one portable linear constraint: the sum of its Terms compared
// against Bound under Sense.
type Constraint struct {
	Name   string  `json:"name"`
	Terms  []Term  `json:"terms"`
	Sense  string  `json:"sense"` // "<=" | ">=" | "=="
	Bound  float64 `json:"bound"`
	Period string  `json:"period,omitempty"`
}

// Term contributes Coefficient times a canonical Variable summed over the
// scoped components; an empty scope slice selects every component in scope.
type Term struct {
	Coefficient float64  `json:"coefficient"`
	Variable    Variable `json:"variable"`
	Techs       []string `json:"techs,omitempty"`    // scope: tech ids (empty = all in scope)
	Carriers    []string `json:"carriers,omitempty"` // scope: carriers
	Nodes       []string `json:"nodes,omitempty"`    // scope: nodes
}

// Variable names the canonical optimization quantity a Term sums.
type Variable string

const (
	VarCapacity   Variable = "capacity"    // installed capacity (flow_cap / p_nom)
	VarFlowOut    Variable = "flow_out"    // dispatched output
	VarFlowIn     Variable = "flow_in"     // consumed input
	VarStorageCap Variable = "storage_cap" // storage energy capacity
	VarEmissions  Variable = "emissions"   // emitted quantity of a carrier
)

// Valid reports whether v is one of the defined variables.
func (v Variable) Valid() bool {
	switch v {
	case VarCapacity, VarFlowOut, VarFlowIn, VarStorageCap, VarEmissions:
		return true
	}
	return false
}
