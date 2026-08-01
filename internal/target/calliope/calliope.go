// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

// Package calliope is the Calliope 0.7 target: capability profile, validation
// gates, the model.yaml + data-table emitter (with ratio-pinning / piecewise /
// MILP extra math), and the CLI run plan.
package calliope

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

// Calliope implements target.Target.
type Calliope struct{}

func init() { target.Register(Calliope{}) }

// Name implements target.Target.
func (Calliope) Name() model.Target { return model.TargetCalliope }

// Capabilities is the enforced feature profile. Every claimed feature is
// backed by a runnable scenario (see the capability-coverage test).
func (Calliope) Capabilities() map[model.Feature]bool {
	return map[model.Feature]bool{
		model.FeatModePlan:               true,
		model.FeatModeOperate:            true,
		model.FeatModeAlternatives:       true,
		model.FeatTimeAggregation:        true, // method "resample" only (0.7 dropped clustering)
		model.FeatMultiCarrierConversion: true, // carrier lists + ratio-pinning extra math
		model.FeatEmissionLimit:          true, // cost classes / custom math
		model.FeatImportExport:           true, // trade -> supply/demand market techs
		model.FeatCommittable:            true, // MILP math: integer units + min stable load
		model.FeatArea:                   true, // area_use constraints
		model.FeatSource:                 true, // source_use / source_cap
		model.FeatNodeOverride:           true, // node-level parameter overrides
		model.FeatLinearConstraint:       true, // custom math
		model.FeatIndexedParams:          true, // arbitrarily-indexed parameters
		model.FeatPiecewisePerformance:   true, // piecewise_constraints math (fixed capacity)
		model.FeatCustomMath:             true, // user-defined math
	}
}

// ValidateJob holds the Calliope-specific gates: the MILP unit-commitment
// subset, piecewise preconditions, method-level time aggregation, symmetric
// storage rate bounds (one flow_cap serves both directions), the run-level
// switches Calliope lacks (copperplate, positive-only accounting), and the
// series-shaped values this emitter renders as scalars only.
func (Calliope) ValidateJob(j *model.Job) ([]string, error) {
	var warns []string
	m := &j.Model

	// Calliope 0.7 removed built-in clustering; only plain resampling maps.
	if ta := j.Experiment.TimeAggregation; ta != nil {
		if ta.Method != "resample" {
			return nil, fmt.Errorf("time_aggregation method %q is not supported by calliope (0.7 removed built-in clustering); only \"resample\" is honored", ta.Method)
		}
		if ta.Resolution == "" {
			return nil, fmt.Errorf("time_aggregation method \"resample\" needs a resolution (e.g. \"3H\")")
		}
	}
	if j.Experiment.Copperplate {
		return nil, fmt.Errorf("copperplate is not supported for calliope (drop the transmission techs instead)")
	}
	if j.Experiment.EmissionAccounting == "positive_only" {
		return nil, fmt.Errorf("emission_accounting \"positive_only\" is not supported for calliope (the co2 cost class sums net)")
	}
	if ud := j.Experiment.AllowUnmetDemand; ud != nil && ud.Enabled && ud.PenaltyPrice != nil {
		warns = append(warns, "allow_unmet_demand.penalty_price is approximated on calliope: ensure_feasibility prices unmet demand at bigM, not the given penalty")
	}
	if m.Time.Weights.IsSeries() {
		return nil, fmt.Errorf("time.weights as a series is not supported for calliope (scalar only); pypsa honors it")
	}
	for _, id := range emit.Keys(m.Transmission) {
		if m.Transmission[id].EnergyConsumption != nil {
			return nil, fmt.Errorf("transmission %q: energy_consumption is only supported for pypsa (multi-port link)", id)
		}
	}

	for _, id := range emit.Keys(m.Technologies) {
		t := m.Technologies[id]

		// Committable is the MILP subset: integer units + min stable load.
		// Start/stop costs and up/down times have no 0.7 math.
		if t.Operation != nil && t.Operation.Committable {
			op := t.Operation
			if op.StartUpCost != nil || op.ShutDownCost != nil || op.MinUptime != nil || op.MinDowntime != nil {
				return nil, fmt.Errorf("tech %q: start-up/shut-down costs and min up/down times are not representable in Calliope 0.7 MILP math; drop them or supply native.calliope math", id)
			}
			if _, ok := unitSize(t.Capacity); !ok {
				return nil, fmt.Errorf("tech %q: committable on calliope needs capacity.per_unit or a capacity bound (existing/max) to size the integer units", id)
			}
		}

		// Piecewise performance is a flow_in->flow_out curve with constant
		// breakpoints, so it needs a conversion tech of fixed size.
		if t.Performance.Kind() == model.PerfPiecewise {
			if t.Role != model.RoleConversion {
				return nil, fmt.Errorf("tech %q: piecewise performance on calliope is emitted as a flow_in->flow_out curve and needs role \"conversion\"", id)
			}
			if t.Capacity == nil || t.Capacity.Expandable || t.Capacity.Existing <= 0 {
				return nil, fmt.Errorf("tech %q: piecewise performance on calliope needs a fixed capacity (capacity.existing, not expandable) — the curve breakpoints are constants", id)
			}
			if len(t.Flows) > 0 {
				return nil, fmt.Errorf("tech %q: piecewise performance cannot be combined with multi-port flows on calliope (the curve and the ratio pinning would over-constrain the balance)", id)
			}
		}

		// Series-shaped values in slots this emitter renders as scalars.
		if t.Operation != nil && t.Operation.MaxPU.IsSeries() && t.Role != model.RoleSupply {
			return nil, fmt.Errorf("tech %q: operation.max_pu as a time series is not supported for target %q (role %s)", id, model.TargetCalliope, t.Role)
		}
		if t.Operation != nil && t.Operation.EqualsPU.IsSeries() && t.Role != model.RoleSupply {
			return nil, fmt.Errorf("tech %q: operation.equals_pu as a time series is not supported for target %q (role %s)", id, model.TargetCalliope, t.Role)
		}
		if op := t.Operation; op != nil {
			if op.MaxStartups != nil {
				return nil, fmt.Errorf("tech %q: operation.max_startups is only supported for adopt-net0", id)
			}
			if op.StandbyPower != nil {
				return nil, fmt.Errorf("tech %q: operation.standby_power is only supported for adopt-net0", id)
			}
		}
		if t.AnnualOutputMin != nil || t.AnnualOutputMax != nil {
			return nil, fmt.Errorf("tech %q: annual_output_min/max are only supported for target pypsa (e_sum_min/e_sum_max)", id)
		}
		if t.BuildYear != nil {
			warns = append(warns, fmt.Sprintf("tech %q: build_year is informational for calliope and not emitted", id))
		}
		if t.Capacity != nil && t.Capacity.DecommissionMode() == "only_complete" {
			return nil, fmt.Errorf("tech %q: capacity.decommission mode \"only_complete\" is only supported for adopt-net0 (calliope honors impossible/continuous)", id)
		}
		if c, ok := t.Costs[model.PrimaryCostClass]; ok {
			if c.VariableOM.IsSeries() {
				return nil, fmt.Errorf("tech %q: variable_om as a time series is only supported for target pypsa", id)
			}
			if c.FuelCost.IsSeries() {
				return nil, fmt.Errorf("tech %q: fuel_cost as a time series is not supported for calliope (scalar only)", id)
			}
		}
		if t.Storage != nil {
			if t.Storage.Inflow != nil {
				return nil, fmt.Errorf("tech %q: storage.inflow is only supported for target pypsa (StorageUnit inflow)", id)
			}
			if t.Storage.SpillCost != nil {
				return nil, fmt.Errorf("tech %q: storage.spill_cost is only supported for target pypsa", id)
			}
			// Calliope has a single flow_cap for charging and discharging, so
			// asymmetric rate bounds cannot be represented faithfully.
			if cr, dr := t.Storage.MaxChargeRate, t.Storage.MaxDischargeRate; cr != nil && dr != nil && *cr != *dr {
				return nil, fmt.Errorf("tech %q: calliope has one flow_cap for both directions; max_charge_rate and max_discharge_rate must be equal (or set only one)", id)
			}
		}
	}

	// Trade prices and limits render as scalar parameters on the market techs.
	for _, id := range emit.Keys(m.Trade) {
		tr := m.Trade[id]
		if (tr.Import != nil && tr.Import.Price.IsSeries()) || (tr.Export != nil && tr.Export.Price.IsSeries()) {
			return nil, fmt.Errorf("trade %q: a time-series price is not supported for target calliope (scalar only)", id)
		}
		if (tr.Import != nil && tr.Import.Limit.IsSeries()) || (tr.Export != nil && tr.Export.Limit.IsSeries()) {
			return nil, fmt.Errorf("trade %q: a time-series limit is not supported for target calliope (scalar only); pypsa and adopt-net0 honor it", id)
		}
	}

	// HiGHS cannot drive Calliope 0.7 (a pyomo/persistent-solver
	// incompatibility); the emitter substitutes cbc.
	if j.Experiment.Solver.Name == "highs" {
		warns = append(warns, "solver \"highs\" is not usable with Calliope; emitting cbc instead")
	}
	if s := j.Experiment.Solver; calliopeSolverOptions(s) == nil && (s.TimeLimit != nil || s.MIPGap != nil || s.Threads != nil) {
		warns = append(warns, fmt.Sprintf("solver %q: time_limit/mip_gap/threads are not mapped for this solver on calliope (mapped: gurobi, cbc); use config.solve.solver_options via native.calliope", s.Name))
	}
	return warns, nil
}

// Emit writes model.yaml, the data tables, and the extra math file.
func (Calliope) Emit(j *model.Job, outDir string) (string, error) {
	return (Emitter{}).Emit(j, outDir)
}

// Plan drives Calliope via a generated run.py that solves the CLI (saving
// netCDF + CSV into the output directory) and then extracts TEMPO's frozen
// result contract into output/contract.json. extract_contract.py is written
// alongside the driver and imported by it.
func (Calliope) Plan(j *model.Job, entrypoint string, dirs target.RunDirs) (target.RunPlan, error) {
	cfg, err := json.Marshal(map[string]string{
		"entrypoint": entrypoint,
		"netcdf":     filepath.Join(dirs.OutDir, "results.nc"),
		"csv":        filepath.Join(dirs.OutDir, "csv"),
		"contract":   filepath.Join(dirs.OutDir, "contract.json"),
	})
	if err != nil {
		return target.RunPlan{}, err
	}
	if err := os.WriteFile(filepath.Join(dirs.RunDir, "extract_contract.py"),
		[]byte(scripts.CalliopeExtractContract), 0o644); err != nil {
		return target.RunPlan{}, err
	}
	driver := "CFG = " + string(cfg) + "\n" + scripts.CalliopeRun
	if err := os.WriteFile(filepath.Join(dirs.RunDir, "run.py"),
		[]byte(driver), 0o644); err != nil {
		return target.RunPlan{}, err
	}
	return target.RunPlan{
		Target:     model.TargetCalliope,
		Entrypoint: entrypoint,
		Command:    []string{"python", "run.py"},
		WorkDir:    dirs.RunDir,
	}, nil
}
