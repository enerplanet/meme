// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

// Package adoptnet0 is the AdOpT-NET0 target: capability profile, validation
// gates, the input_data tree emitter (database-tech mapping + node-keyed
// overrides), and the generated run.py driver (copy tech data -> apply
// overrides -> quick_solve).
package adoptnet0

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/enerplanet/meme/internal/emit"
	"github.com/enerplanet/meme/internal/model"
	"github.com/enerplanet/meme/internal/scripts"
	"github.com/enerplanet/meme/internal/target"
)

// AdOptNET0 implements target.Target.
type AdOptNET0 struct{}

func init() { target.Register(AdOptNET0{}) }

// Name implements target.Target.
func (AdOptNET0) Name() model.Target { return model.TargetAdOpt }

// Capabilities is the enforced feature profile. committable,
// piecewise_performance, linear_constraint and generic (multi-carrier)
// conversion are NOT claimed: adopt_net0 is database-driven and only physics
// techs (pv/wind/heat_pump) and storage map onto library entries; anything
// else needs node-level native and is otherwise rejected (see ValidateJob).
func (AdOptNET0) Capabilities() map[model.Feature]bool {
	return map[model.Feature]bool{
		model.FeatModePlan:           true,
		model.FeatModePareto:         true,
		model.FeatModeMonteCarlo:     true,
		model.FeatTimeAggregation:    true, // method "typical_days" only
		model.FeatImportExport:       true, // carrier import/export data
		model.FeatEmissionLimit:      true, // emission targets
		model.FeatNodeOverride:       true, // per-node technology data
		model.FeatPhysicsPerformance: true, // pvlib / wind / COP database techs
		model.FeatObjectiveEmissions: true, // objective "min_emissions"
	}
}

// ValidateJob holds the AdOpT-specific gates: database-tech mappability,
// unsupported transmission, method-level time aggregation, the time-index
// forms adopt_net0 cannot read (explicit labels, weights), fields without an
// input_data representation (fuel costs, annual output bounds, systemwide
// bounds, ...), and the series-shaped values this emitter renders as scalars
// only.
func (AdOptNET0) ValidateJob(j *model.Job) ([]string, error) {
	var warns []string
	m := &j.Model

	if ta := j.Experiment.TimeAggregation; ta != nil && ta.Method != "typical_days" {
		return nil, fmt.Errorf("time_aggregation method %q is not supported by adopt-net0; only \"typical_days\" is honored", ta.Method)
	}
	// adopt reads all CSVs on a regular pandas date_range; explicit labels and
	// per-timestep weights have no representation.
	if len(m.Time.Timesteps) > 0 {
		return nil, fmt.Errorf("time.timesteps (explicit labels) are not supported by adopt-net0 (a regular date_range from start/end/resolution is required)")
	}
	if m.Time.Weights != nil {
		return nil, fmt.Errorf("time.weights are not supported by adopt-net0")
	}
	if ud := j.Experiment.AllowUnmetDemand; ud != nil && ud.Enabled && ud.PenaltyPrice == nil {
		return nil, fmt.Errorf("allow_unmet_demand on adopt-net0 needs penalty_price (energybalance.violation is a price)")
	}

	// The emitter writes no Networks (adopt_net0 network JSONs are not mapped
	// yet) — reject rather than solve a model that silently lost its lines.
	if len(m.Transmission) > 0 {
		return nil, fmt.Errorf("transmission is not supported by target %q yet (adopt_net0 Networks are not mapped); model exchange via trade or nodes.<n>.native.adopt-net0", model.TargetAdOpt)
	}

	for _, id := range emit.Keys(m.Technologies) {
		t := m.Technologies[id]

		// Non-demand techs must map onto a database entry.
		if t.Role != model.RoleDemand {
			if _, ok := dbTech(t); !ok {
				return nil, fmt.Errorf("tech %q (%s) has no adopt_net0 database mapping (only pv/wind/heat_pump physics and storage map); model it via nodes.<node>.native.adopt-net0 naming a database technology", id, t.Role)
			}
		}

		if t.Operation != nil && t.Operation.MaxPU.IsSeries() {
			return nil, fmt.Errorf("tech %q: operation.max_pu as a time series is not supported for target %q (role %s)", id, model.TargetAdOpt, t.Role)
		}
		if t.Operation != nil && t.Operation.EqualsPU != nil {
			return nil, fmt.Errorf("tech %q: operation.equals_pu is not supported for adopt-net0 (fixed profiles need the generic-production route via native.adopt-net0)", id)
		}
		if t.AnnualOutputMin != nil || t.AnnualOutputMax != nil {
			return nil, fmt.Errorf("tech %q: annual_output_min/max are only supported for target pypsa (e_sum_min/e_sum_max)", id)
		}
		if t.DemandCurtailable {
			return nil, fmt.Errorf("tech %q: demand_curtailable is only supported for target calliope (sink_use_max)", id)
		}
		if t.BuildYear != nil {
			warns = append(warns, fmt.Sprintf("tech %q: build_year is informational for adopt-net0 and not emitted", id))
		}
		if t.Capacity != nil && (t.Capacity.SystemwideMin != nil || t.Capacity.SystemwideMax != nil) {
			return nil, fmt.Errorf("tech %q: capacity.systemwide_min/max are not supported for adopt-net0 (per-node sizes only)", id)
		}
		if c, ok := t.Costs[model.PrimaryCostClass]; ok {
			if c.VariableOM.IsSeries() {
				return nil, fmt.Errorf("tech %q: variable_om as a time series is only supported for target pypsa", id)
			}
			if c.FuelCost != nil {
				return nil, fmt.Errorf("tech %q: fuel_cost is not supported for adopt-net0 (price the input carrier via trade import instead)", id)
			}
			if c.InvestmentPerEnergyCapacity != nil {
				warns = append(warns, fmt.Sprintf("tech %q: investment_per_energy_capacity is only emitted for calliope (cost_storage_cap); adopt-net0 has no per-MWh storage cost slot", id))
			}
		}
		if t.Storage != nil {
			if t.Storage.Inflow != nil {
				return nil, fmt.Errorf("tech %q: storage.inflow is only supported for target pypsa (StorageUnit inflow)", id)
			}
			if t.Storage.SpillCost != nil {
				return nil, fmt.Errorf("tech %q: storage.spill_cost is only supported for target pypsa", id)
			}
			if t.Storage.DepthOfDischarge != nil {
				return nil, fmt.Errorf("tech %q: storage.depth_of_discharge is only supported for target calliope (storage_discharge_depth); adopt-net0 has no minimum-SOC slot", id)
			}
		}
	}

	// Trade renders as carrier CSV columns; a native.adopt-net0 block on a
	// trade entry has no file to merge into.
	for _, id := range emit.Keys(m.Trade) {
		if tr := m.Trade[id]; len(tr.Native.For(model.TargetAdOpt)) > 0 {
			warns = append(warns, fmt.Sprintf("trade %q: native.adopt-net0 has no merge anchor (trade emits as carrier CSV columns) and is ignored", id))
		}
	}
	return warns, nil
}

// Emit writes the runnable input_data tree.
func (AdOptNET0) Emit(j *model.Job, outDir string) (string, error) {
	return (Emitter{}).Emit(j, outDir)
}

// Plan writes run.py and adoptnet0_extract_contract.py into dirs.RunDir.
// The driver solves via ModelHub().quick_solve(), writes HDF5 results, then
// runs the extractor to produce dirs.OutDir/contract.json.
func (AdOptNET0) Plan(j *model.Job, entrypoint string, dirs target.RunDirs) (target.RunPlan, error) {
	if err := os.WriteFile(
		filepath.Join(dirs.RunDir, "adoptnet0_extract_contract.py"),
		[]byte(scripts.AdOptNET0ExtractContract),
		0o644,
	); err != nil {
		return target.RunPlan{}, err
	}
	script := fmt.Sprintf("BASE = %q\nCONTRACT = %q\n",
		entrypoint, filepath.Join(dirs.OutDir, "contract.json"),
	) + scripts.AdOptNET0Run
	if err := os.WriteFile(filepath.Join(dirs.RunDir, "run.py"), []byte(script), 0o644); err != nil {
		return target.RunPlan{}, err
	}
	return target.RunPlan{
		Target:     model.TargetAdOpt,
		Entrypoint: entrypoint,
		Command:    []string{"python", "run.py"},
		WorkDir:    dirs.RunDir,
	}, nil
}
