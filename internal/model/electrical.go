// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package model

// The electrical fields describe the AC network a power flow (mode
// "power_flow", ADR-0008) solves: a voltage level per node, an impedance and a
// rating per arc, and a fixed dispatch per technology. They are power-flow
// inputs: an optimization never reads them (ValidateFor names them as unused
// and keeps emitting transport links), and a target without the power_flow
// capability rejects the mode.

// NodeElectrical is the AC bus a node hosts: its nominal voltage in kV. Every
// node a power-flow arc touches needs one; a line connects nodes of equal
// voltage, a transformer connects different ones.
type NodeElectrical struct {
	VNom float64 `json:"v_nom"` // nominal voltage, kV
}

// ElectricalKind is the kind of AC branch a transmission arc is.
type ElectricalKind string

const (
	ElectricalLine        ElectricalKind = "line"        // series impedance in Ω over the arc
	ElectricalTransformer ElectricalKind = "transformer" // impedance per unit of the rating
)

// Valid reports whether k is a defined branch kind.
func (k ElectricalKind) Valid() bool {
	return k == ElectricalLine || k == ElectricalTransformer
}

// TransmissionElectrical makes an arc an AC branch. For a line R and X are the
// total series resistance and reactance over the arc in Ω; for a transformer
// they are per unit of SNom. SNom is the apparent-power rating in MVA and
// defaults to the arc's installed capacity (an active-power flow treats MW
// and MVA alike); NumParallel counts parallel circuits (default 1); TapRatio
// applies to a transformer only (default 1). PyPSA Line / Transformer.
type TransmissionElectrical struct {
	Kind        ElectricalKind `json:"kind"`
	R           float64        `json:"r"`
	X           float64        `json:"x"`
	SNom        *float64       `json:"s_nom,omitempty"`
	NumParallel *int           `json:"num_parallel,omitempty"`
	TapRatio    *float64       `json:"tap_ratio,omitempty"`
}

// Rating resolves the branch rating used for the loading output: SNom, else
// the arc's installed capacity; ok is false when neither is positive.
func (e *TransmissionElectrical) Rating(c *Capacity) (float64, bool) {
	if e == nil {
		return 0, false
	}
	if e.SNom != nil && *e.SNom > 0 {
		return *e.SNom, true
	}
	if c != nil && c.Existing > 0 {
		return c.Existing, true
	}
	return 0, false
}

// Circuits resolves NumParallel with its default of one circuit.
func (e *TransmissionElectrical) Circuits() int {
	if e == nil || e.NumParallel == nil {
		return 1
	}
	return *e.NumParallel
}

// Tap resolves TapRatio with its default of 1.
func (e *TransmissionElectrical) Tap() float64 {
	if e == nil || e.TapRatio == nil {
		return 1
	}
	return *e.TapRatio
}

// PowerFlowOptions configures mode "power_flow": the algorithm, the balancing
// generator and the convergence tolerance. Slack names the supply technology
// (id, or id@node for a technology placed at several nodes) whose generator
// takes up the balance; it is required, so the result is reproducible rather
// than left to the framework's pick.
type PowerFlowOptions struct {
	Method          string   `json:"method,omitempty"`           // newton_raphson (default) | linear
	Slack           string   `json:"slack,omitempty"`            // balancing supply technology (id or id@node)
	DistributeSlack bool     `json:"distribute_slack,omitempty"` // share the balance over all generators in proportion to their dispatch
	Tolerance       *float64 `json:"tolerance,omitempty"`        // Newton-Raphson convergence tolerance (pu); default 1e-6
}

// Power-flow methods: the full AC solution (Newton-Raphson, seeded by a linear
// solve) or the linear (DC) approximation alone.
const (
	PowerFlowNewtonRaphson = "newton_raphson"
	PowerFlowLinear        = "linear"
)

// DefaultPowerFlowTolerance is the Newton-Raphson tolerance applied when the
// options leave it unset (PyPSA's own default).
const DefaultPowerFlowTolerance = 1e-6

// SlackOrEmpty returns the slack spelling, tolerating a nil block.
func (o *PowerFlowOptions) SlackOrEmpty() string {
	if o == nil {
		return ""
	}
	return o.Slack
}

// EffectiveMethod returns the method with the default applied.
func (o *PowerFlowOptions) EffectiveMethod() string {
	if o == nil || o.Method == "" {
		return PowerFlowNewtonRaphson
	}
	return o.Method
}

// EffectiveTolerance returns the tolerance with the default applied.
func (o *PowerFlowOptions) EffectiveTolerance() float64 {
	if o == nil || o.Tolerance == nil {
		return DefaultPowerFlowTolerance
	}
	return *o.Tolerance
}
