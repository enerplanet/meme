// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package model

// PowerFlow is the isolated, derived power-flow network an external caller
// (EnerPlanET) attaches to a PyPSA job as the top-level "power_flow" key. It
// is a fixed injection + electrical network: the dispatch was already solved
// (Calliope), the pass built the buses/lines/transformers and the per-snapshot
// p_set series, and MEME's pypsa target re-emits them as a solvable network
// and runs a real power flow (the `power_flow` run.py mode).
//
// Electrical params are LITERAL here (r_ohm/x_ohm total per line, s_nom_mva
// rating) — the caller resolved the cable type already, so MEME never needs a
// type catalogue. This is the key shape that lets a PyPSA run produce voltage /
// loading / convergence that the transport-Link model alone cannot.
type PowerFlow struct {
	Buses        []PowerFlowBus         `json:"buses,omitempty"`
	Lines        []PowerFlowLine        `json:"lines,omitempty"`
	Transformers []PowerFlowTransformer `json:"transformers,omitempty"`
	Generators   []PowerFlowGenerator   `json:"generators,omitempty"`
	Loads        []PowerFlowLoad        `json:"loads,omitempty"`
}

// PowerFlowBus is one electrical bus with its nominal voltage in kV.
type PowerFlowBus struct {
	Name   string  `json:"name"`
	VNomKV float64 `json:"v_nom_kv"`
}

// PowerFlowLine is one line. ROhm/XOhm are the TOTAL series impedance (Ohm);
// SNomMVA is its apparent-power rating (MVA). The caller already resolved the
// cable type into these literal values.
type PowerFlowLine struct {
	Name        string  `json:"name"`
	Bus0        string  `json:"bus0"`
	Bus1        string  `json:"bus1"`
	LengthKm    float64 `json:"length_km"`
	NumParallel float64 `json:"num_parallel"`
	ROhm        float64 `json:"r_ohm"`
	XOhm        float64 `json:"x_ohm"`
	SNomMVA     float64 `json:"s_nom_mva"`
}

// PowerFlowTransformer is one MV/LV (or HV/MV) transformer with its rating.
type PowerFlowTransformer struct {
	Name        string  `json:"name"`
	Bus0        string  `json:"bus0"`
	Bus1        string  `json:"bus1"`
	SNomMVA     float64 `json:"s_nom_mva"`
	NumParallel float64 `json:"num_parallel"`
}

// PowerFlowGenerator is a fixed supply injection. PSetMW is the per-snapshot
// dispatch (index-aligned with the model's snapshots).
type PowerFlowGenerator struct {
	Name    string    `json:"name"`
	Bus     string    `json:"bus"`
	Control string    `json:"control"` // "Slack" | "PQ" — the legacy slack convention
	PSetMW  []float64 `json:"p_set_mw"`
}

// PowerFlowLoad is a fixed demand withdrawal. PSetMW is per-snapshot.
type PowerFlowLoad struct {
	Name   string    `json:"name"`
	Bus    string    `json:"bus"`
	PSetMW []float64 `json:"p_set_mw"`
}
