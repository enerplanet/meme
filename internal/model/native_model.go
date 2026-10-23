// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package model

import "encoding/json"

// Native carries target-specific configuration that has no portable canonical
// representation (PyPSA power-flow attributes, Calliope custom math, AdOpT
// solver heuristics, ...). Each block is a partial of that target's *own* input
// schema and is merged verbatim into the emitted output for that target and
// ignored for the others.
//
// Using a native block makes the payload target-locked: it will emit correctly
// only for the target whose block is populated. ValidateFor surfaces this as a
// warning so the caller knows portability was traded away deliberately.
//
// Framework versions with different input dialects carry separate blocks: a
// native.calliope fragment is Calliope 0.7 syntax and is never applied to the
// calliope-0.6 target, which reads native["calliope-0.6"] (0.6 syntax:
// essentials/constraints/costs) instead.
type Native struct {
	PyPSA      json.RawMessage `json:"pypsa,omitempty"`
	Calliope   json.RawMessage `json:"calliope,omitempty"`
	Calliope06 json.RawMessage `json:"calliope-0.6,omitempty"`
	AdOpt      json.RawMessage `json:"adopt-net0,omitempty"`
}

// For returns the raw block for a target (nil-safe).
func (n *Native) For(t Target) json.RawMessage {
	if n == nil {
		return nil
	}
	switch t {
	case TargetPyPSA:
		return n.PyPSA
	case TargetCalliope:
		return n.Calliope
	case TargetCalliope06:
		return n.Calliope06
	case TargetAdOpt:
		return n.AdOpt
	}
	return nil
}

// otherTargets lists targets other than sel that carry a (soon-to-be-ignored)
// native block.
func (n *Native) otherTargets(sel Target) []Target {
	var out []Target
	for _, t := range []Target{TargetPyPSA, TargetCalliope, TargetCalliope06, TargetAdOpt} {
		if t != sel && len(n.For(t)) > 0 {
			out = append(out, t)
		}
	}
	return out
}
