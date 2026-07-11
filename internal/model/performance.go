// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package model

import "fmt"

// physicsModels is the set of recognized physics-based performance models. Each
// maps to an AdOpT-NET0 technology type; for other targets a precomputed profile
// (Performance.Precomputed) must be supplied.
var physicsModels = map[string]bool{
	"pv":         true, // pvlib photovoltaic
	"wind":       true, // wind power curve
	"heat_pump":  true, // temperature-dependent COP
	"open_hydro": true, // run-of-river / inflow-driven hydro
}

// Kind returns the effective performance type, defaulting to constant.
func (p *Performance) Kind() PerfType {
	if p == nil || p.Type == "" {
		return PerfConstant
	}
	return p.Type
}

// Validate checks the internal consistency of a performance model.
func (p *Performance) Validate() error {
	if p == nil {
		return nil
	}
	switch p.Kind() {
	case PerfConstant:
		return nil
	case PerfPiecewise:
		if len(p.Breakpoints) < 2 {
			return fmt.Errorf("piecewise performance needs at least 2 breakpoints")
		}
		prev := -1.0
		for i, b := range p.Breakpoints {
			if b.Load < 0 || b.Load > 1 {
				return fmt.Errorf("breakpoint %d: load %.3f must be within [0,1]", i, b.Load)
			}
			if b.Load <= prev {
				return fmt.Errorf("breakpoints must be strictly increasing in load (breakpoint %d)", i)
			}
			if b.Efficiency <= 0 {
				return fmt.Errorf("breakpoint %d: efficiency must be positive", i)
			}
			prev = b.Load
		}
		if p.MinLoad != nil && (*p.MinLoad < 0 || *p.MinLoad > 1) {
			return fmt.Errorf("min_load %.3f must be within [0,1]", *p.MinLoad)
		}
		return nil
	case PerfPhysics:
		if !physicsModels[p.Model] {
			return fmt.Errorf("unknown physics model %q (known: pv, wind, heat_pump, open_hydro)", p.Model)
		}
		return nil
	default:
		return fmt.Errorf("unknown performance type %q", p.Type)
	}
}

// perfEntry is one record in the PyPSA _performance.json sidecar: a technology
// whose performance is not a plain efficiency and therefore needs the
// orchestrator to precompute a profile (pvlib, wind curve, COP) or linearize a
// piecewise curve before/within the PyPSA optimization.
type PerfEntry struct {
	Tech        string      `json:"tech"`
	Node        string      `json:"node"`
	Performance Performance `json:"performance"`
}
