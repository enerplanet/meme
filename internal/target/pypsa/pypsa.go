// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

// Package pypsa is the PyPSA target: capability profile, validation gates,
// CSV-folder emitter, time-series materialization, and the generated run.py
// driver (import -> sidecar constraints -> mode branch -> solve -> export).
package pypsa

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/enerplanet/meme/internal/emit"
	"github.com/enerplanet/meme/internal/model"
	"github.com/enerplanet/meme/internal/scripts"
	"github.com/enerplanet/meme/internal/target"
)

// PyPSA implements target.Target.
type PyPSA struct{}

func init() { target.Register(PyPSA{}) }

// Name implements target.Target.
func (PyPSA) Name() model.Target { return model.TargetPyPSA }

// Capabilities is the enforced feature profile. Every claimed feature is
// backed by a runnable scenario (see the capability-coverage test).
func (PyPSA) Capabilities() map[model.Feature]bool {
	return map[model.Feature]bool{
		model.FeatModePlan:         true,
		model.FeatModeOperate:      true, // fixed capacities + rolling-horizon dispatch
		model.FeatModeAlternatives: true, // MGA (optimize_mga)
		model.FeatModeStochastic:   true, // two-stage stochastic (set_scenarios)
		// power_flow (Lines/Transformers, KVL) is NOT claimed: no canonical field
		// emits a lines.csv — all transmission becomes transport Links. Reclaim
		// once the schema carries electrical parameters (r, x, s_nom).
		model.FeatMultiCarrierConversion: true, // multi-output Links
		model.FeatEmissionLimit:          true, // GlobalConstraint
		model.FeatImportExport:           true, // via generators/loads + marginal_cost
		model.FeatCommittable:            true, // committable generators/links
		model.FeatNodeOverride:           true, // one component instance per node
		model.FeatLinearConstraint:       true, // GlobalConstraint + run.py sidecar constraints
	}
}

// ValidateJob holds the PyPSA-specific gates: mode preconditions (operate and
// stochastic), scope rules for sidecar constraints, run-level switches with no
// PyPSA representation (copperplate, unmet-demand slack, positive-only
// emission accounting), component fields the Link/StorageUnit encodings cannot
// carry (tech emission factors, decommissioning, storage rate bounds, ...),
// and warnings for the fields the emitter drops by contract.
func (PyPSA) ValidateJob(j *model.Job) ([]string, error) {
	var warns []string
	m := &j.Model
	e := &j.Experiment

	// operate = rolling-horizon dispatch over fixed capacities; horizon-wide
	// constraints cannot hold across windows. stochastic needs the scenario
	// set with translatable overrides; sidecar constraints are not built for
	// the scenario-expanded model.
	switch e.EffectiveMode() {
	case model.ModeOperate:
		for _, id := range emit.Keys(m.Technologies) {
			t := m.Technologies[id]
			if t.Capacity != nil && t.Capacity.Expandable {
				return nil, fmt.Errorf("mode \"operate\" on pypsa dispatches fixed capacities, but tech %q is expandable", id)
			}
		}
		for _, id := range emit.Keys(m.Transmission) {
			l := m.Transmission[id]
			if l.Capacity != nil && l.Capacity.Expandable {
				return nil, fmt.Errorf("mode \"operate\" on pypsa dispatches fixed capacities, but transmission %q is expandable", id)
			}
		}
		if len(m.Constraints) > 0 || len(m.EmissionLimits) > 0 {
			return nil, fmt.Errorf("mode \"operate\" on pypsa (rolling horizon) cannot enforce horizon-wide constraints or emission limits")
		}
	case model.ModeStochastic:
		if len(e.Scenarios) == 0 {
			return nil, fmt.Errorf("mode \"stochastic\" needs experiment.scenarios (name, weight, overrides)")
		}
		for _, s := range e.Scenarios {
			if s.Name == "" {
				return nil, fmt.Errorf("stochastic scenario without a name")
			}
			if s.Weight < 0 {
				return nil, fmt.Errorf("stochastic scenario %q has a negative weight", s.Name)
			}
			// Every override must translate to a PyPSA attribute; an
			// untranslatable one would be silently dropped at run time.
			if _, err := ScenarioSets(m, s); err != nil {
				return nil, err
			}
		}
		if len(m.Constraints) > 0 {
			return nil, fmt.Errorf("mode \"stochastic\" on pypsa does not support portable linear constraints")
		}
	}

	// Constraints that don't project onto a GlobalConstraint are applied by
	// run.py with exact tech/node scoping — but emissions terms only exist as
	// carrier-wide GlobalConstraints, so a scoped emissions term is impossible.
	for i, c := range m.Constraints {
		if _, _, ok := globalType(c); ok {
			continue
		}
		for _, t := range c.Terms {
			if t.Variable == model.VarEmissions {
				return nil, fmt.Errorf("constraints[%d] %q: tech/node-scoped emissions terms are not supported for pypsa (emission accounting is carrier-wide)", i, c.Name)
			}
		}
	}

	// Run-level switches with no PyPSA representation.
	if e.Copperplate {
		return nil, fmt.Errorf("copperplate is not supported for pypsa (restructure the network via native.pypsa instead)")
	}
	if ud := e.AllowUnmetDemand; ud != nil && ud.Enabled {
		return nil, fmt.Errorf("allow_unmet_demand is not supported for pypsa (add slack generators via native.pypsa)")
	}
	if e.EmissionAccounting == "positive_only" {
		return nil, fmt.Errorf("emission_accounting \"positive_only\" is not supported for pypsa (carrier co2_emissions accounting is net by construction)")
	}

	// Component fields with no PyPSA representation are rejected rather than
	// silently reshaping the model.
	for _, id := range emit.Keys(m.Technologies) {
		t := m.Technologies[id]
		if t.EmissionFactor != nil {
			return nil, fmt.Errorf("tech %q: emission_factor is not supported for pypsa (set the fuel carrier's co2_intensity instead; emission accounting is carrier-wide)", id)
		}
		if t.DemandCurtailable {
			return nil, fmt.Errorf("tech %q: demand_curtailable is only supported for target calliope (sink_use_max)", id)
		}
		if (t.AnnualOutputMin != nil || t.AnnualOutputMax != nil) && t.Role != model.RoleSupply {
			return nil, fmt.Errorf("tech %q: annual_output_min/max map onto generator e_sum_min/e_sum_max and need role \"supply\"", id)
		}
		if t.Capacity != nil && t.Capacity.DecommissionMode() != "impossible" {
			return nil, fmt.Errorf("tech %q: capacity.decommission mode %q is not supported for pypsa (existing capacity stays fixed); adopt-net0 honors it", id, t.Capacity.DecommissionMode())
		}
		if op := t.Operation; op != nil {
			if op.MaxStartups != nil {
				return nil, fmt.Errorf("tech %q: operation.max_startups is only supported for adopt-net0", id)
			}
			if op.StandbyPower != nil {
				return nil, fmt.Errorf("tech %q: operation.standby_power is only supported for adopt-net0", id)
			}
		}
		if c, ok := t.Costs[model.PrimaryCostClass]; ok && c.FuelCost.IsSeries() {
			return nil, fmt.Errorf("tech %q: a time-series fuel_cost is not supported (it folds into marginal_cost as a scalar); use a series variable_om instead", id)
		}
	}

	// Systemwide capacity bounds ride the sidecar-constraint mechanism, which
	// the operate/stochastic runners cannot apply.
	if mode := e.EffectiveMode(); mode == model.ModeOperate || mode == model.ModeStochastic {
		for _, id := range emit.Keys(m.Technologies) {
			if c := m.Technologies[id].Capacity; c != nil && (c.SystemwideMin != nil || c.SystemwideMax != nil) {
				return nil, fmt.Errorf("tech %q: capacity.systemwide bounds are not applicable in mode %q on pypsa (sidecar constraints are unavailable)", id, mode)
			}
		}
	}

	// Storage fields with no StorageUnit representation are rejected rather
	// than silently reshaping the plant.
	for _, id := range emit.Keys(m.Technologies) {
		s := m.Technologies[id].Storage
		if s == nil {
			continue
		}
		if s.MaxChargeRate != nil || s.MaxDischargeRate != nil {
			return nil, fmt.Errorf("tech %q: storage.max_charge_rate/max_discharge_rate are not representable on a pypsa StorageUnit (p_nom bounds both directions); use max_hours or native.pypsa stores+links", id)
		}
		if s.DepthOfDischarge != nil {
			return nil, fmt.Errorf("tech %q: storage.depth_of_discharge is not representable on a pypsa StorageUnit; use native.pypsa (a Store with e_min_pu)", id)
		}
		if s.NoSimultaneousChargeDischarge {
			return nil, fmt.Errorf("tech %q: storage.no_simultaneous_charge_discharge is not representable for pypsa (StorageUnit dispatch is a single signed variable)", id)
		}
	}

	// Transmission extensions with no link representation.
	for _, id := range emit.Keys(m.Transmission) {
		l := m.Transmission[id]
		if l.LossPerDistance != nil && l.Distance == nil {
			return nil, fmt.Errorf("transmission %q: loss_per_distance needs distance to fold into the pypsa link efficiency", id)
		}
		if l.MinFlow != nil && l.Bidirectional {
			return nil, fmt.Errorf("transmission %q: min_flow on a bidirectional link is not representable for pypsa (p_min_pu already carries the reverse direction)", id)
		}
		if l.EnergyConsumption != nil && l.Bidirectional {
			return nil, fmt.Errorf("transmission %q: energy_consumption on a bidirectional link is not representable for pypsa (reversed flow would inject the auxiliary carrier)", id)
		}
		if l.EmissionFactor != nil {
			return nil, fmt.Errorf("transmission %q: emission_factor is only supported for calliope (co2 cost class); pypsa emission accounting is carrier-wide", id)
		}
	}

	// Known-dropped fields warn.
	for _, id := range emit.Keys(m.Technologies) {
		if c, ok := m.Technologies[id].Costs[model.PrimaryCostClass]; ok && c.InvestmentPerEnergyCapacity != nil {
			warns = append(warns, fmt.Sprintf("tech %q: investment_per_energy_capacity is only emitted for calliope (cost_storage_cap); pypsa has no per-MWh storage cost slot", id))
		}
	}
	for _, id := range emit.Keys(m.Trade) {
		tr := m.Trade[id]
		for _, s := range []struct {
			name string
			side *model.TradeSide
		}{{"import", tr.Import}, {"export", tr.Export}} {
			if s.side != nil && s.side.EmissionFactor != nil {
				warns = append(warns, fmt.Sprintf("trade %q: %s emission_factor is not emitted for pypsa (set the carrier's co2_intensity instead); calliope/adopt-net0 honor it", id, s.name))
			}
		}
	}
	if solverOptions(e.Solver) == nil && (e.Solver.TimeLimit != nil || e.Solver.MIPGap != nil || e.Solver.Threads != nil) {
		warns = append(warns, fmt.Sprintf("solver %q: time_limit/mip_gap/threads are not mapped for this solver on pypsa (mapped: highs, gurobi, cbc); use experiment.native", e.Solver.Name))
	}
	return warns, nil
}

// Emit writes the CSV import folder plus the materialized time-varying CSVs,
// so the emitted tree is directly runnable.
func (PyPSA) Emit(j *model.Job, outDir string) (string, error) {
	entrypoint, err := (Emitter{}).Emit(j, outDir)
	if err != nil {
		return "", err
	}
	if err := MaterializeTimeSeries(&j.Model, outDir); err != nil {
		return "", fmt.Errorf("materialize time series: %w", err)
	}
	return entrypoint, nil
}

// Plan writes the run.py driver into dirs.RunDir and returns the command.
func (PyPSA) Plan(j *model.Job, entrypoint string, dirs target.RunDirs) (target.RunPlan, error) {
	script := runPy(j, dirs.InputDir, dirs.OutDir)
	if err := os.WriteFile(filepath.Join(dirs.RunDir, "run.py"), []byte(script), 0o644); err != nil {
		return target.RunPlan{}, err
	}
	return target.RunPlan{
		Target:     model.TargetPyPSA,
		Entrypoint: entrypoint,
		Command:    []string{"python", "run.py"},
		WorkDir:    dirs.RunDir,
	}, nil
}

// runPy drives PyPSA: import the CSV folder, apply the _constraints.json
// sidecar as linopy constraints, branch on the experiment mode (plan, operate =
// rolling horizon, alternatives = MGA, stochastic = set_scenarios), export the
// solved network, and exit non-zero unless the solve is optimal. The static
// body is embedded from internal/scripts/pypsa_run.py; this prepends the run
// configuration.
func runPy(j *model.Job, inputDir, outDir string) string {
	e := &j.Experiment
	// First-class experiment blocks win over the legacy solver.options
	// namespaces ("mga", "operate").
	slack := optionFloat(e.Solver.Options, "mga", "slack", 0.05)
	if a := e.Alternatives; a != nil && a.Slack != nil {
		slack = *a.Slack
	}
	horizon := int(optionFloat(e.Solver.Options, "operate", "horizon", 24))
	if o := e.Operate; o != nil && o.Horizon != "" {
		horizon = int(emit.ParseHours(o.Horizon).Hours())
	}
	cfgMap := map[string]any{
		"input":     inputDir,
		"output":    outDir,
		"mode":      string(e.EffectiveMode()),
		"solver":    solverName(e.Solver.Name),
		"mga_slack": slack,
		"horizon":   horizon,
		"overlap":   int(optionFloat(e.Solver.Options, "operate", "overlap", 0)),
	}
	if so := solverOptions(e.Solver); so != nil {
		cfgMap["solver_options"] = so
	}
	cfg, _ := json.Marshal(cfgMap)
	return "CFG = " + string(cfg) + "\n" + scripts.PyPSARun
}

// solverOptions maps the canonical solver fields (time_limit seconds, mip_gap,
// threads) onto the selected solver's own option names; linopy forwards them
// verbatim. Unmapped solvers get no options (ValidateJob warns).
func solverOptions(s model.Solver) map[string]any {
	names := map[string][3]string{
		"highs":  {"time_limit", "mip_rel_gap", "threads"},
		"gurobi": {"TimeLimit", "MIPGap", "Threads"},
		"cbc":    {"seconds", "ratioGap", "threads"},
	}
	keys, ok := names[solverName(s.Name)]
	if !ok {
		return nil
	}
	opts := map[string]any{}
	if s.TimeLimit != nil {
		opts[keys[0]] = *s.TimeLimit
	}
	if s.MIPGap != nil {
		opts[keys[1]] = *s.MIPGap
	}
	if s.Threads != nil {
		opts[keys[2]] = *s.Threads
	}
	if len(opts) == 0 {
		return nil
	}
	return opts
}

// solverName maps the experiment solver onto a linopy solver name. HiGHS ships
// with PyPSA and is the default.
func solverName(name string) string {
	switch name {
	case "", "highs":
		return "highs"
	default:
		return name
	}
}

// optionFloat digs experiment.solver.options.<group>.<key> out of the untyped
// options map, with a default.
func optionFloat(opts map[string]any, group, key string, def float64) float64 {
	g, _ := opts[group].(map[string]any)
	if v, ok := g[key].(float64); ok {
		return v
	}
	return def
}
