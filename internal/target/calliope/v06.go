// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package calliope

import (
	"fmt"
	"path/filepath"

	"github.com/enerplanet/meme/internal/emit"
	"github.com/enerplanet/meme/internal/model"
	"github.com/enerplanet/meme/internal/target"
)

// calliope06Version is the Calliope 0.6 release the calliope-0.6 target's
// emitter is validated against (pinned in
// environment/requirements-calliope-0.6.txt). It is also written into the
// model definition (model.calliope_version), which Calliope checks at load.
const calliope06Version = "0.6.8"

// Calliope06 implements target.Target for Calliope 0.6: the legacy model
// definition dialect (model/run/techs/locations/links, `file=` timeseries
// references, group_constraints, conversion_plus carrier tiers), driven
// through the `calliope-0.6` CLI. It is a version Variant of the default
// "calliope" target, so it runs only when named explicitly.
type Calliope06 struct{}

func init() { target.Register(Calliope06{}) }

// Name implements target.Target.
func (Calliope06) Name() model.Target { return model.TargetCalliope06 }

// Version implements target.Target.
func (Calliope06) Version() string { return calliope06Version }

// VariantOf implements target.Variant: "all" runs the 0.7 target, not both.
func (Calliope06) VariantOf() model.Target { return model.TargetCalliope }

// Capabilities is the enforced feature profile. Every claimed feature is
// backed by a runnable scenario (see the capability-coverage test). Compared
// with Calliope 0.7: built-in k-means clustering (typical_days) and a native
// emissions-minimizing objective are gained; indexed parameters, piecewise
// performance and custom math (0.7-only concepts) are not available, and
// portable linear constraints are limited to the single-term shapes
// group_constraints can express (ValidateJob rejects the rest).
func (Calliope06) Capabilities() map[model.Feature]bool {
	return map[model.Feature]bool{
		model.FeatModePlan:               true,
		model.FeatModeOperate:            true, // run.mode operate + run.operation window/horizon
		model.FeatModeAlternatives:       true, // run.mode spores + run.spores_options
		model.FeatTimeAggregation:        true, // resample and typical_days (k-means clustering)
		model.FeatMultiCarrierConversion: true, // conversion_plus carrier tiers + carrier_ratios
		model.FeatEmissionLimit:          true, // group_constraints cost_max on the emissions class
		model.FeatImportExport:           true, // trade -> supply/demand market techs
		model.FeatCommittable:            true, // units_max + energy_cap_per_unit + energy_cap_min_use (MILP)
		model.FeatArea:                   true, // resource_area_max + locations.available_area
		model.FeatSource:                 true, // resource / resource_unit / resource_cap_max (supply_plus)
		model.FeatNodeOverride:           true, // locations.<loc>.techs.<tech> constraints/costs
		model.FeatLinearConstraint:       true, // single-term constraint IR -> group_constraints
		model.FeatObjectiveEmissions:     true, // run.objective_options.cost_class weights
	}
}

// ValidateJob holds the Calliope-0.6-specific gates. Everything the 0.6
// emitter would silently drop is rejected (error) or surfaced (warning):
// the single-cost-class/single-term limits of group_constraints, the
// model-wide storage switches (cyclic_storage, one energy_eff for both
// directions), the carrier-tier limits of conversion_plus, the run-level
// switches Calliope lacks, and the series-shaped values this emitter renders
// as scalars only.
func (Calliope06) ValidateJob(j *model.Job) ([]string, error) {
	var warns []string
	m := &j.Model
	e := &j.Experiment
	mode := e.EffectiveMode()

	if ta := e.TimeAggregation; ta != nil {
		switch ta.Method {
		case "resample":
			if ta.Resolution == "" {
				return nil, fmt.Errorf("time_aggregation method \"resample\" needs a resolution (e.g. \"3H\")")
			}
		case "typical_days":
			if ta.Periods <= 0 {
				return nil, fmt.Errorf("time_aggregation method \"typical_days\" needs periods (the number of representative days, k-means k) on calliope-0.6")
			}
			if len(ta.KeepFullResolutionFor) > 0 {
				warns = append(warns, "time_aggregation.keep_full_resolution_for is not supported by calliope-0.6 k-means clustering and is ignored")
			}
		default:
			return nil, fmt.Errorf("time_aggregation method %q is not supported by calliope-0.6 (resample or typical_days)", ta.Method)
		}
	}
	if e.Copperplate {
		return nil, fmt.Errorf("copperplate is not supported for calliope-0.6 (drop the transmission techs instead)")
	}
	if e.EmissionAccounting == "positive_only" {
		return nil, fmt.Errorf("emission_accounting \"positive_only\" is not supported for calliope-0.6 (the emissions cost class sums net)")
	}
	if ud := e.AllowUnmetDemand; ud != nil && ud.Enabled && ud.PenaltyPrice != nil {
		warns = append(warns, "allow_unmet_demand.penalty_price is approximated on calliope-0.6: ensure_feasibility prices unmet demand at bigM, not the given penalty")
	}
	if e.CustomMath != nil {
		warns = append(warns, "experiment.custom_math has no effect on calliope-0.6 (no custom math; constraints and emission_limits map to group_constraints)")
	}
	if r := e.Reporting; r != nil && len(r.ShadowPrices) > 0 {
		warns = append(warns, "reporting.shadow_prices is not supported by calliope-0.6 and is ignored")
	}
	if a := e.Alternatives; a != nil && a.ScoringAlgorithm != "" {
		warns = append(warns, fmt.Sprintf("alternatives.scoring_algorithm %q is not supported by calliope-0.6 SPORES (integer scoring only) and is ignored", a.ScoringAlgorithm))
	}
	if m.Time.Weights != nil {
		return nil, fmt.Errorf("time.weights is not supported for calliope-0.6 (timestep weights derive from the time resolution); pypsa honors it")
	}
	if e.Objective == model.ObjectiveMinEmissions && !hasEmissionSource06(m) {
		return nil, fmt.Errorf("objective \"min_emissions\" on calliope-0.6 needs an emission source (a carrier co2_intensity, a tech emission_factor or a trade emission_factor) to weight")
	}
	if !allNodesHaveCoords(m) {
		var missing []string
		for _, id := range emit.Keys(m.Nodes) {
			if m.Nodes[id].Coords == nil {
				missing = append(missing, id)
			}
		}
		if len(missing) < len(m.Nodes) {
			warns = append(warns, fmt.Sprintf("location coordinates are emitted only when every node has them (Calliope requires all or none); missing on %v, so coordinates are omitted", missing))
		}
	}
	for _, id := range emit.Keys(m.Transmission) {
		if m.Transmission[id].EnergyConsumption != nil {
			return nil, fmt.Errorf("transmission %q: energy_consumption is only supported for pypsa (multi-port link)", id)
		}
	}

	var cyclic, nonCyclic int
	for _, id := range emit.Keys(m.Technologies) {
		t := m.Technologies[id]

		if t.Operation != nil && t.Operation.Committable {
			op := t.Operation
			if op.StartUpCost != nil || op.ShutDownCost != nil || op.MinUptime != nil || op.MinDowntime != nil {
				return nil, fmt.Errorf("tech %q: start-up/shut-down costs and min up/down times are not representable in Calliope 0.6 MILP constraints; drop them or supply native[\"calliope-0.6\"]", id)
			}
			if _, ok := unitSize(t.Capacity); !ok {
				return nil, fmt.Errorf("tech %q: committable on calliope-0.6 needs capacity.per_unit or a capacity bound (existing/max) to size the integer units", id)
			}
		}
		if len(t.Flows) > 0 {
			ins, outs := 0, 0
			for _, f := range t.Flows {
				if f.Direction == model.FlowIn {
					ins++
				} else {
					outs++
				}
			}
			if ins > 1 {
				return nil, fmt.Errorf("tech %q: more than one input carrier in flows is not supported for calliope-0.6 (conversion_plus input tiers are not emitted); use target calliope", id)
			}
			if outs > 3 {
				return nil, fmt.Errorf("tech %q: calliope-0.6 conversion_plus has three output carrier tiers; %d output flows cannot be represented", id, outs)
			}
		}
		if t.Operation != nil && t.Operation.MaxPU.IsSeries() && t.Role != model.RoleSupply {
			return nil, fmt.Errorf("tech %q: operation.max_pu as a time series is not supported for target %q (role %s)", id, model.TargetCalliope06, t.Role)
		}
		if t.Operation != nil && t.Operation.EqualsPU.IsSeries() && t.Role != model.RoleSupply {
			return nil, fmt.Errorf("tech %q: operation.equals_pu as a time series is not supported for target %q (role %s)", id, model.TargetCalliope06, t.Role)
		}
		if op := t.Operation; op != nil {
			if op.MaxStartups != nil {
				return nil, fmt.Errorf("tech %q: operation.max_startups is only supported for adopt-net0", id)
			}
			if op.StandbyPower != nil {
				return nil, fmt.Errorf("tech %q: operation.standby_power is only supported for adopt-net0", id)
			}
		}
		if (t.AnnualOutputMin != nil || t.AnnualOutputMax != nil) && t.CarrierOut.First() == "" {
			return nil, fmt.Errorf("tech %q: annual_output_min/max on calliope-0.6 become carrier_prod group constraints and need a carrier_out", id)
		}
		if t.BuildYear != nil {
			warns = append(warns, fmt.Sprintf("tech %q: build_year is informational for calliope-0.6 and not emitted", id))
		}
		if t.Capacity != nil && t.Capacity.DecommissionMode() == "only_complete" {
			return nil, fmt.Errorf("tech %q: capacity.decommission mode \"only_complete\" is only supported for adopt-net0 (calliope-0.6 honors impossible/continuous)", id)
		}
		if c, ok := t.Costs[model.PrimaryCostClass]; ok {
			if c.VariableOM.IsSeries() {
				return nil, fmt.Errorf("tech %q: variable_om as a time series is only supported for target pypsa", id)
			}
			if c.FuelCost.IsSeries() {
				return nil, fmt.Errorf("tech %q: fuel_cost as a time series is not supported for calliope-0.6 (scalar only)", id)
			}
		}
		if t.Role == model.RoleStorage {
			if in, out := t.CarrierIn.First(), t.CarrierOut.First(); in != "" && out != "" && in != out {
				return nil, fmt.Errorf("tech %q: calliope-0.6 storage has one carrier; carrier_in %q and carrier_out %q differ", id, in, out)
			}
		}
		if s := t.Storage; s != nil {
			if s.Inflow != nil {
				return nil, fmt.Errorf("tech %q: storage.inflow is only supported for target pypsa (StorageUnit inflow)", id)
			}
			if s.SpillCost != nil {
				return nil, fmt.Errorf("tech %q: storage.spill_cost is only supported for target pypsa", id)
			}
			if cr, dr := s.MaxChargeRate, s.MaxDischargeRate; cr != nil && dr != nil && *cr != *dr {
				return nil, fmt.Errorf("tech %q: calliope-0.6 has one energy_cap for both directions; max_charge_rate and max_discharge_rate must be equal (or set only one)", id)
			}
			if ce, de := s.ChargeEff, s.DischargeEff; ce != nil && de != nil && *ce != *de {
				warns = append(warns, fmt.Sprintf("tech %q: calliope-0.6 applies one energy_eff to charging and discharging; emitting sqrt(charge_eff * discharge_eff) = %s so the round-trip efficiency is preserved", id, emit.Ftoa(storageEff06(s))))
			}
			if s.Cyclic {
				cyclic++
			} else {
				nonCyclic++
			}
		}
		if mode == model.ModeOperate && t.Role != model.RoleDemand {
			v, ok := fixedCap(t.Capacity)
			if !ok {
				return nil, fmt.Errorf("mode \"operate\" on calliope-0.6 needs a fixed capacity for every tech, but tech %q has neither capacity.existing nor capacity.max", id)
			}
			if t.Capacity.Existing <= 0 {
				warns = append(warns, fmt.Sprintf("tech %q: operate mode sizes nothing; capacity.max %s is installed as the fixed capacity (set capacity.existing to make it explicit)", id, emit.Ftoa(v)))
			}
		}
		if t.Area != nil && t.Area.PerCapacity == nil && (t.Source == nil || t.Source.Unit != "per_area") {
			warns = append(warns, fmt.Sprintf("tech %q: area.max only binds on calliope-0.6 when the resource is per area (source.unit \"per_area\") or area.per_capacity is set", id))
		}
	}
	if mode == model.ModeOperate {
		for _, id := range emit.Keys(m.Transmission) {
			l := m.Transmission[id]
			if v, ok := fixedCap(l.Capacity); ok && l.Capacity.Existing <= 0 {
				warns = append(warns, fmt.Sprintf("transmission %q: operate mode sizes nothing; capacity.max %s is installed as the fixed capacity (set capacity.existing to make it explicit)", id, emit.Ftoa(v)))
			}
		}
	}
	if cyclic > 0 && nonCyclic > 0 {
		warns = append(warns, "storage.cyclic is a model-wide switch on calliope-0.6 (run.cyclic_storage); techs disagree, so cyclic storage is disabled for all of them")
	}

	for _, id := range emit.Keys(m.Trade) {
		tr := m.Trade[id]
		if (tr.Import != nil && tr.Import.Price.IsSeries()) || (tr.Export != nil && tr.Export.Price.IsSeries()) {
			return nil, fmt.Errorf("trade %q: a time-series price is not supported for target calliope-0.6 (scalar only)", id)
		}
		if (tr.Import != nil && tr.Import.Limit.IsSeries()) || (tr.Export != nil && tr.Export.Limit.IsSeries()) {
			return nil, fmt.Errorf("trade %q: a time-series limit is not supported for target calliope-0.6 (scalar only); pypsa and adopt-net0 honor it", id)
		}
		if tr.Export != nil {
			if _, ok := tr.Export.Limit.Reduce(); !ok {
				return nil, fmt.Errorf("trade %q: export on calliope-0.6 needs a limit (the market demand tech's resource bound)", id)
			}
		}
	}

	// Portable constraints must fit a group constraint; the shared translator
	// reports exactly what does not.
	for i, c := range m.Constraints {
		if _, err := groupConstraint06(c, i, m); err != nil {
			return nil, err
		}
	}

	if e.Solver.Name == "highs" {
		warns = append(warns, "solver \"highs\" is not usable with Calliope; emitting cbc instead")
	}
	if s := e.Solver; calliopeSolverOptions(s) == nil && (s.TimeLimit != nil || s.MIPGap != nil || s.Threads != nil) {
		warns = append(warns, fmt.Sprintf("solver %q: time_limit/mip_gap/threads are not mapped for this solver on calliope-0.6 (mapped: gurobi, cbc); use run.solver_options via native[\"calliope-0.6\"]", s.Name))
	}
	return warns, nil
}

// hasEmissionSource06 reports whether anything in the model would populate the
// emissions cost class (so an emissions objective has something to weight).
func hasEmissionSource06(m *model.Model) bool {
	for _, c := range m.Carriers {
		if c.CO2Intensity != nil && *c.CO2Intensity != 0 {
			return true
		}
	}
	for _, t := range m.Technologies {
		if t.EmissionFactor != nil && *t.EmissionFactor != 0 {
			return true
		}
	}
	for _, tr := range m.Trade {
		if (tr.Import != nil && tr.Import.EmissionFactor != nil && *tr.Import.EmissionFactor != 0) ||
			(tr.Export != nil && tr.Export.EmissionFactor != nil && *tr.Export.EmissionFactor != 0) {
			return true
		}
	}
	for _, l := range m.Transmission {
		if l.EmissionFactor != nil && *l.EmissionFactor != 0 {
			return true
		}
	}
	return false
}

// Emit writes model.yaml and the timeseries_data/ CSVs it references.
func (Calliope06) Emit(j *model.Job, outDir string) (string, error) {
	return (Emitter06{}).Emit(j, outDir)
}

// Plan drives the Calliope 0.6 CLI (exposed as `calliope-0.6` in the
// environment image), saving netCDF + CSV results into the output directory.
func (Calliope06) Plan(j *model.Job, entrypoint string, dirs target.RunDirs) (target.RunPlan, error) {
	return target.RunPlan{
		Target:     model.TargetCalliope06,
		Entrypoint: entrypoint,
		Command: []string{"calliope-0.6", "run", entrypoint,
			"--save_netcdf", filepath.Join(dirs.OutDir, "results.nc"),
			"--save_csv", filepath.Join(dirs.OutDir, "csv")},
		WorkDir: dirs.RunDir,
	}, nil
}
