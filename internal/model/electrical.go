// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package model

import "math"

// The electrical fields describe the AC network a power flow (mode
// "power_flow", ADR-0008) solves: a voltage level per node, an impedance and a
// rating per arc, and a fixed dispatch per technology. They are power-flow
// inputs: an optimization never reads them (ValidateFor names them as unused
// and keeps emitting transport links), and a target without the power_flow
// capability rejects the mode.

// NodeElectrical is the AC bus a node hosts: its nominal voltage in kV and an
// optional shunt. Every node a power-flow arc touches needs one; a line
// connects nodes of equal voltage, a transformer connects different ones.
type NodeElectrical struct {
	VNom  float64 `json:"v_nom"`           // nominal voltage, kV
	Shunt *Shunt  `json:"shunt,omitempty"` // compensation at the bus (PyPSA ShuntImpedance)
}

// Shunt is a fixed shunt admittance at a bus in siemens: G the conductance, B
// the susceptance (positive = capacitive, supplying reactive power).
type Shunt struct {
	G float64 `json:"g,omitempty"`
	B float64 `json:"b,omitempty"`
}

// ElectricalControl is how a generator takes part in a power flow: PQ holds
// its active and reactive set-points, PV holds its active set-point and its
// bus voltage. The slack is named in the experiment, not here.
type ElectricalControl string

const (
	ControlPQ ElectricalControl = "pq"
	ControlPV ElectricalControl = "pv"
)

// TechElectrical is a technology's reactive-power and voltage behaviour in a
// power flow (ADR-0009). Q comes either as ReactiveDispatch (MVAr per
// timestep, PyPSA q_set) or derived from the active set-point at PowerFactor
// in the component's own direction — a load consumes, a generator supplies —
// with Leading flipping the sign; the two are mutually exclusive. Control and
// VoltageSetpoint belong to supply technologies: a pv generator holds its bus
// at VoltageSetpoint (pu), and the slack may carry a set-point too. Conversion
// technologies have no reactive fields (a PyPSA Link transfers active power).
type TechElectrical struct {
	ReactiveDispatch *Value            `json:"reactive_dispatch,omitempty"`
	PowerFactor      *float64          `json:"power_factor,omitempty"`
	Leading          bool              `json:"leading,omitempty"`
	Control          ElectricalControl `json:"control,omitempty"`
	VoltageSetpoint  *float64          `json:"voltage_setpoint,omitempty"`
}

// ReactiveFactor is Q per unit of P implied by the power factor: tan(acos pf),
// negated for a leading power factor; zero without a power factor.
func (e *TechElectrical) ReactiveFactor() float64 {
	if e == nil || e.PowerFactor == nil {
		return 0
	}
	f := math.Tan(math.Acos(*e.PowerFactor))
	if e.Leading {
		f = -f
	}
	return f
}

// IsPV reports whether the generator holds its bus voltage.
func (e *TechElectrical) IsPV() bool { return e != nil && e.Control == ControlPV }

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
// total series resistance and reactance over the arc in Ω — or, instead,
// RPerLength and XPerLength in Ω/km multiplied by the arc's distance; for a
// transformer R and X are per unit of SNom. SNom is the apparent-power rating
// in MVA and defaults to the arc's installed capacity (an active-power flow
// treats MW and MVA alike); NumParallel counts parallel circuits (default 1);
// B and G are a line's shunt susceptance and conductance in siemens; TapRatio
// and PhaseShift (degrees) apply to a transformer only. PyPSA Line /
// Transformer.
type TransmissionElectrical struct {
	Kind        ElectricalKind `json:"kind"`
	R           float64        `json:"r,omitempty"`
	X           float64        `json:"x,omitempty"`
	RPerLength  *float64       `json:"r_per_length,omitempty"`
	XPerLength  *float64       `json:"x_per_length,omitempty"`
	SNom        *float64       `json:"s_nom,omitempty"`
	NumParallel *int           `json:"num_parallel,omitempty"`
	B           *float64       `json:"b,omitempty"`
	G           *float64       `json:"g,omitempty"`
	TapRatio    *float64       `json:"tap_ratio,omitempty"`
	PhaseShift  *float64       `json:"phase_shift,omitempty"`
}

// PerLength reports whether the impedance is given per km.
func (e *TransmissionElectrical) PerLength() bool {
	return e != nil && (e.RPerLength != nil || e.XPerLength != nil)
}

// Impedance resolves the branch's series impedance: the literal R and X, or
// the per-length values over the arc's distance (rounded to 12 decimals so
// 0.12 Ω/km over 10 km emits as 1.2, not float noise).
func (e *TransmissionElectrical) Impedance(distance *float64) (r, x float64) {
	if !e.PerLength() {
		return e.R, e.X
	}
	d := 0.0
	if distance != nil {
		d = *distance
	}
	if e.RPerLength != nil {
		r = math.Round(*e.RPerLength*d*1e12) / 1e12
	}
	if e.XPerLength != nil {
		x = math.Round(*e.XPerLength*d*1e12) / 1e12
	}
	return r, x
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
