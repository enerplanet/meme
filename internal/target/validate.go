// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package target

import (
	"fmt"
	"sort"
	"strings"

	"github.com/enerplanet/meme/internal/emit"
	"github.com/enerplanet/meme/internal/model"
)

// ValidateFor runs the full validation pipeline for one target: inline-series
// interning, structural model and experiment validation, deprecation/mismatch
// warnings for the run spec, the generic capability-driven gates below, and
// finally the target's own ValidateJob hook. Warnings are non-fatal; a
// non-nil error means the payload must be rejected (nothing a payload asks
// for may be silently dropped).
func ValidateFor(j *model.Job, name model.Target) (warnings []string, err error) {
	t, err := For(name)
	if err != nil {
		return nil, err
	}
	name = t.Name() // an alias resolves to the registered name
	// Inline per-timestep arrays become registry series before anything reads
	// them, so validation and the emitters see exactly one series shape.
	if e := j.Model.InternInlineSeries(); e != nil {
		return nil, e
	}
	if e := j.Model.Validate(); e != nil {
		return nil, e
	}
	if e := j.Experiment.Validate(); e != nil {
		return nil, e
	}
	// Inline series shorter than the model horizon are padded by repeating
	// their last value at emit time — legal, but rarely what the user meant.
	warnings = append(warnings, emit.SeriesLengthMismatches(&j.Model)...)
	warnings = append(warnings, j.Experiment.OptionWarnings()...)
	genWarns, err := validateGeneric(t, j)
	warnings = append(warnings, genWarns...)
	if err != nil {
		return warnings, err
	}
	hookWarns, err := t.ValidateJob(j)
	warnings = append(warnings, hookWarns...)
	if err != nil {
		return warnings, err
	}
	warnings = append(warnings, model.NativeWarnings(name, j, targetStrings())...)
	return warnings, nil
}

func targetStrings() []string {
	ns := Names()
	out := make([]string, len(ns))
	for i, n := range ns {
		out[i] = string(n)
	}
	return out
}

// modeFeature maps a run mode to its capability key.
func modeFeature(m model.Mode) model.Feature {
	switch m {
	case model.ModeOperate:
		return model.FeatModeOperate
	case model.ModeAlternatives:
		return model.FeatModeAlternatives
	case model.ModePareto:
		return model.FeatModePareto
	case model.ModeMonteCarlo:
		return model.FeatModeMonteCarlo
	case model.ModeStochastic:
		return model.FeatModeStochastic
	case model.ModePowerFlow:
		return model.FeatPowerFlow
	default:
		return model.FeatModePlan
	}
}

// powerFlowInputs lists the technologies (dispatch or electrical block, base
// field or override), arcs (electrical parameters or a fixed transfer) and
// nodes (electrical block) that carry inputs only a power flow reads. Under
// the linear-power-flow network model the impedances and voltage levels ARE
// read, so only fixed transfers and shunts count there.
func powerFlowInputs(m *model.Model, lopf bool) (techs, arcs, nodes []string) {
	for _, id := range sortedTechIDs(m) {
		t := m.Technologies[id]
		has := t.Dispatch != nil || t.Electrical != nil
		for _, ov := range t.NodeOverrides {
			if ov.Dispatch != nil || ov.Electrical != nil {
				has = true
			}
		}
		if has {
			techs = append(techs, id)
		}
	}
	for _, id := range sortedTransmissionIDs(m) {
		l := m.Transmission[id]
		if l.Dispatch != nil || (!lopf && l.Electrical != nil) {
			arcs = append(arcs, id)
		}
	}
	for _, id := range sortedKeys(m.Nodes) {
		n := m.Nodes[id]
		if n.Electrical != nil && (!lopf || n.Electrical.Shunt != nil) {
			nodes = append(nodes, id)
		}
	}
	return techs, arcs, nodes
}

// validateGeneric holds every gate that is purely capability-driven — no
// target names appear here; a new target gets all of this for free from its
// Capabilities profile.
func validateGeneric(t Target, j *model.Job) ([]string, error) {
	var warns []string
	caps := t.Capabilities()
	m := &j.Model
	e := &j.Experiment

	mode := e.EffectiveMode()
	if !mode.Valid() {
		return nil, fmt.Errorf("experiment.mode %q is invalid", e.Mode)
	}
	if !caps[modeFeature(mode)] {
		return nil, fmt.Errorf("mode %q is not supported by target %q", mode, t.Name())
	}

	// The voltage law inside an optimization (ADR-0010) is a run specification
	// a transport-only target cannot honor: solving the same payload as a
	// transport model would be a different problem, so it is rejected like a
	// mode. The outages of a security-constrained solve must be branches of
	// that network.
	lopf := e.EffectiveNetworkModel() == model.NetworkLinearPowerFlow
	if lopf && !caps[model.FeatLinearPowerFlow] {
		return nil, fmt.Errorf("network model %q is not supported by target %q", e.Network.Model, t.Name())
	}
	if e.Network != nil && e.Network.Security != nil {
		for _, id := range e.Network.Security.Outages {
			l, ok := m.Transmission[id]
			if !ok {
				return nil, fmt.Errorf("experiment.network.security.outages: %q is not a transmission arc", id)
			}
			if l.Electrical == nil {
				return nil, fmt.Errorf("experiment.network.security.outages: %q is not an electrical arc (only a branch of the voltage-law network can be taken out)", id)
			}
		}
	}

	// Power-flow inputs (ADR-0008, ADR-0009): dispatch, reactive power,
	// voltage control, shunts, impedances and fixed transfers mean something
	// only where a power flow runs. Everywhere else — Calliope and AdOpT,
	// which have no such concept, and PyPSA's optimization modes, where the
	// solver decides dispatch — they are ignored and named in one warning, so
	// the same document serves the planning call and the power-flow call and
	// nothing is dropped silently. Inside a power flow nothing may be a
	// decision.
	if mode != model.ModePowerFlow {
		techs, arcs, nodes := powerFlowInputs(m, lopf)
		if len(techs)+len(arcs)+len(nodes) > 0 {
			var parts []string
			if len(techs) > 0 {
				parts = append(parts, fmt.Sprintf("dispatch/electrical on technologies %v", techs))
			}
			if len(arcs) > 0 {
				if lopf {
					parts = append(parts, fmt.Sprintf("dispatch on transmission %v", arcs))
				} else {
					parts = append(parts, fmt.Sprintf("electrical/dispatch on transmission %v (emitted as transport links)", arcs))
				}
			}
			if len(nodes) > 0 {
				if lopf {
					parts = append(parts, fmt.Sprintf("shunts on nodes %v", nodes))
				} else {
					parts = append(parts, fmt.Sprintf("electrical on nodes %v", nodes))
				}
			}
			warns = append(warns, fmt.Sprintf("power-flow inputs are only used by mode power_flow on a target with the power_flow capability; in mode %q on target %q they are ignored: %s. Pin a dispatch in an optimization with operation.equals_pu instead", mode, t.Name(), strings.Join(parts, ", ")))
		}
	} else {
		for _, id := range sortedTechIDs(m) {
			if c := m.Technologies[id].Capacity; c != nil && c.Expandable {
				return nil, fmt.Errorf("mode %q has no decision variables, but tech %q is expandable", mode, id)
			}
			for _, nid := range sortedKeys(m.Technologies[id].NodeOverrides) {
				if c := m.Technologies[id].NodeOverrides[nid].Capacity; c != nil && c.Expandable {
					return nil, fmt.Errorf("mode %q has no decision variables, but tech %q is expandable at node %q", mode, id, nid)
				}
			}
		}
		for _, id := range sortedTransmissionIDs(m) {
			if c := m.Transmission[id].Capacity; c != nil && c.Expandable {
				return nil, fmt.Errorf("mode %q has no decision variables, but transmission %q is expandable", mode, id)
			}
		}
		for _, d := range []struct {
			used bool
			what string
		}{
			{len(m.Trade) > 0, "trade (import/export)"},
			{len(m.Constraints) > 0, "constraints"},
			{len(m.EmissionLimits) > 0, "emission_limits"},
			{len(e.Scenarios) > 0, "scenarios"},
		} {
			if d.used {
				return nil, fmt.Errorf("mode %q has no decision variables, but the payload carries %s", mode, d.what)
			}
		}
	}

	obj := e.Objective
	if obj == "" {
		obj = model.ObjectiveMinCost
	}
	if !obj.Valid() {
		return nil, fmt.Errorf("experiment.objective %q is invalid", e.Objective)
	}
	if obj == model.ObjectiveMinEmissions && !caps[model.FeatObjectiveEmissions] {
		return nil, fmt.Errorf("objective %q is not supported by target %q (the runner would silently minimize cost)", obj, t.Name())
	}
	if !e.Foresight.Valid() {
		return nil, fmt.Errorf("experiment.foresight %q is invalid", e.Foresight)
	}
	if e.TimeAggregation != nil && !caps[model.FeatTimeAggregation] {
		return nil, fmt.Errorf("time_aggregation is not supported by target %q", t.Name())
	}
	// No target materializes file-backed series yet: every emitter reads the
	// inline values of the registry. Reject up front instead of failing at
	// emit time (the schema accepts the form, so this is the only gate).
	for _, id := range sortedKeys(m.Timeseries) {
		if m.Timeseries[id].Source == "file" {
			return nil, fmt.Errorf("timeseries %q: source \"file\" is not supported by any target yet; inline the values (source \"inline\")", id)
		}
	}

	// Problem-shaping feature gates: reject a payload that uses a field the
	// selected target cannot honor.
	var usesFlows, usesCommit, usesArea, usesSource bool
	committable := func(op *model.Operation) bool { return op != nil && op.Committable }
	for _, tech := range m.Technologies {
		if len(tech.Flows) > 0 {
			usesFlows = true
		}
		if committable(tech.Operation) {
			usesCommit = true
		}
		if tech.Area != nil {
			usesArea = true
		}
		if tech.Source != nil {
			usesSource = true
		}
		// Technology.At merges override Operation/Area/Source wholesale, so an
		// override-only usage must trip the same gates as a base-field usage.
		for _, ov := range tech.NodeOverrides {
			if committable(ov.Operation) {
				usesCommit = true
			}
			if ov.Area != nil {
				usesArea = true
			}
			if ov.Source != nil {
				usesSource = true
			}
		}
	}
	gates := []struct {
		used bool
		feat model.Feature
		what string
	}{
		{usesFlows, model.FeatMultiCarrierConversion, "multi-port conversion (flows)"},
		{len(m.EmissionLimits) > 0, model.FeatEmissionLimit, "emission_limits"},
		{len(m.Trade) > 0, model.FeatImportExport, "trade (import/export)"},
		{usesCommit, model.FeatCommittable, "committable operation"},
		{usesArea, model.FeatArea, "area constraints"},
		{usesSource, model.FeatSource, "source constraints"},
		{len(m.Constraints) > 0, model.FeatLinearConstraint, "linear constraints"},
	}
	for _, g := range gates {
		if g.used && !caps[g.feat] {
			return nil, fmt.Errorf("%s is not supported by target %q", g.what, t.Name())
		}
	}

	// An indexed parameter with more than one entry cannot be projected onto a
	// flat per-component table.
	if where, ok := model.FirstMultiIndexed(m); ok && !caps[model.FeatIndexedParams] {
		return nil, fmt.Errorf("%s is a multi-valued indexed parameter, which target %q cannot represent (use a scalar, a time series, or native.%s)", where, t.Name(), t.Name())
	}

	// Performance emittability, capability-driven: piecewise needs the native
	// piecewise feature; physics is native where claimed, otherwise a
	// precomputed profile must be supplied.
	checkPerf := func(where string, p *model.Performance) error {
		switch p.Kind() {
		case model.PerfConstant:
			return nil
		case model.PerfPiecewise:
			if !caps[model.FeatPiecewisePerformance] {
				return fmt.Errorf("%s: piecewise part-load performance is not supported by target %q (no native piecewise-efficiency representation)", where, t.Name())
			}
		case model.PerfPhysics:
			if !caps[model.FeatPhysicsPerformance] && p.Precomputed == nil {
				return fmt.Errorf("%s: physics model %q requires a precomputed profile for target %q (run the model externally and set performance.precomputed)", where, p.Model, t.Name())
			}
		}
		return nil
	}
	for _, id := range sortedTechIDs(m) {
		tech := m.Technologies[id]
		if err := checkPerf("tech "+id, tech.Performance); err != nil {
			return nil, err
		}
		for nid, ov := range tech.NodeOverrides {
			if ov.Performance != nil {
				if err := checkPerf(fmt.Sprintf("tech %s override %s", id, nid), ov.Performance); err != nil {
					return nil, err
				}
			}
		}

		// A conversion with multiple carriers but no `flows` is emitter-lossy
		// (first carrier only) — warn and point at the fix.
		if tech.Role == model.RoleConversion && len(tech.Flows) == 0 && (len(tech.CarrierIn) > 1 || len(tech.CarrierOut) > 1) {
			warns = append(warns, fmt.Sprintf(
				"tech %q lists multiple carriers without `flows`; the emitter uses only the first — use `flows` for a true multi-port conversion", id))
		}

		// Costs are only emitted from the PrimaryCostClass ("monetary").
		if len(tech.Costs) > 0 {
			if _, ok := tech.Costs[model.PrimaryCostClass]; !ok {
				warns = append(warns, fmt.Sprintf(
					"tech %q defines costs under a class other than %q; emitters read only the %q class, so these costs are ignored",
					id, model.PrimaryCostClass, model.PrimaryCostClass))
			}
		}

		// Series-shaped values in slots every target renders as scalars.
		if tech.Operation != nil && tech.Operation.MinPU.IsSeries() {
			return nil, fmt.Errorf("tech %q: operation.min_pu as a time series is not supported (scalar only)", id)
		}
		if tech.Operation != nil && (tech.Operation.RampUp.IsSeries() || tech.Operation.RampDown.IsSeries()) {
			return nil, fmt.Errorf("tech %q: ramp limits as time series are not supported (scalar only)", id)
		}
		if tech.Efficiency.IsSeries() {
			return nil, fmt.Errorf("tech %q: efficiency as a time series is not supported (scalar only)", id)
		}
	}

	// Transmission efficiency renders as a scalar on every target.
	for _, id := range sortedTransmissionIDs(m) {
		if m.Transmission[id].Efficiency.IsSeries() {
			return nil, fmt.Errorf("transmission %q: efficiency as a time series is not supported (scalar only)", id)
		}
	}

	// Transmission costs: the per-distance investment is emitted for calliope
	// and, with the arc's lifetime and interest rate, PyPSA prices the
	// investment and fixed O&M on its Links and Lines; the remaining cost
	// fields still have no emitter elsewhere.
	for _, id := range sortedTransmissionIDs(m) {
		for _, class := range sortedKeys(m.Transmission[id].Costs) {
			c := m.Transmission[id].Costs[class]
			other := c.InvestmentPerCapacity != nil || c.InvestmentPerEnergyCapacity != nil ||
				c.FixedOM != nil || c.FixedOMFraction != nil || c.VariableOM != nil || c.FuelCost != nil || c.Purchase != nil
			if other && t.Name() != model.TargetPyPSA {
				warns = append(warns, fmt.Sprintf("transmission %q: costs other than investment_per_capacity_distance are not emitted for target %q; capacity/efficiency/distance are honored", id, t.Name()))
				break
			}
		}
	}

	// Run-output locations are managed by the service layer.
	if r := e.Reporting; r != nil && r.OutputDir != "" {
		warns = append(warns, "reporting.output_dir is managed by the service layer and ignored; case_name/save_logs/shadow_prices are honored where the target supports them")
	}
	return warns, nil
}

func sortedTechIDs(m *model.Model) []string {
	return sortedKeys(m.Technologies)
}

func sortedTransmissionIDs(m *model.Model) []string {
	return sortedKeys(m.Transmission)
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
