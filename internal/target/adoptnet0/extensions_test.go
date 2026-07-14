// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package adoptnet0_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/enerplanet/meme/internal/model"
	"github.com/enerplanet/meme/internal/target/adoptnet0"
)

func fp(v float64) *float64 { return &v }
func ip(v int) *int         { return &v }

func emitAdopt(t *testing.T, j model.Job) string {
	t.Helper()
	dir := t.TempDir()
	root, err := (adoptnet0.AdOptNET0{}).Emit(&j, dir)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	return root
}

// dig walks a decoded JSON tree by keys.
func dig(t *testing.T, m map[string]any, keys ...string) any {
	t.Helper()
	var cur any = m
	for _, k := range keys {
		obj, ok := cur.(map[string]any)
		if !ok {
			t.Fatalf("dig %v: %T is not an object", keys, cur)
		}
		cur, ok = obj[k]
		if !ok {
			t.Fatalf("dig %v: key %q missing", keys, k)
		}
	}
	return cur
}

// First-class experiment blocks land in ConfigModel.json (legacy
// solver.options namespaces lose).
func TestTypedRunOptions(t *testing.T) {
	j := loadSampleFor(t, model.TargetAdOpt)
	j.Experiment.Mode = model.ModeMonteCarlo
	j.Experiment.MonteCarlo = &model.MonteCarloOptions{
		Samples: 25, StandardDeviation: fp(0.3), On: []string{"import_price"},
	}
	j.Experiment.Solver = model.Solver{
		Name: "glpk", TimeLimit: fp(7200), MIPGap: fp(0.01), Threads: ip(8),
		Options: map[string]any{"monte_carlo": map[string]any{"N": 3.0}}, // legacy loses
	}
	root := emitAdopt(t, j)
	cfg := readJSON(t, filepath.Join(root, "ConfigModel.json"))

	if got := dig(t, cfg, "optimization", "monte_carlo", "N", "value"); got != 25.0 {
		t.Errorf("monte_carlo.N = %v, want 25", got)
	}
	if got := dig(t, cfg, "optimization", "monte_carlo", "sd", "value"); got != 0.3 {
		t.Errorf("monte_carlo.sd = %v, want 0.3", got)
	}
	on := dig(t, cfg, "optimization", "monte_carlo", "on_what", "value").([]any)
	if len(on) != 1 || on[0] != "Import" {
		t.Errorf("monte_carlo.on_what = %v, want [Import]", on)
	}
	if got := dig(t, cfg, "solveroptions", "mipgap", "value"); got != 0.01 {
		t.Errorf("mipgap = %v, want 0.01", got)
	}
	if got := dig(t, cfg, "solveroptions", "timelim", "value"); got != 2.0 {
		t.Errorf("timelim = %v hours, want 2 (7200 s)", got)
	}
	if got := dig(t, cfg, "solveroptions", "threads", "value"); got != 8.0 {
		t.Errorf("threads = %v, want 8", got)
	}
}

func TestParetoPointsFirstClass(t *testing.T) {
	j := loadSampleFor(t, model.TargetAdOpt)
	j.Experiment.Mode = model.ModePareto
	j.Experiment.Pareto = &model.ParetoOptions{Points: 9}
	root := emitAdopt(t, j)
	cfg := readJSON(t, filepath.Join(root, "ConfigModel.json"))
	if got := dig(t, cfg, "optimization", "pareto_points", "value"); got != 9.0 {
		t.Errorf("pareto_points = %v, want 9", got)
	}
}

// Export emission factors reach the carrier CSV (import 0.35 from the sample,
// export 0.3 set here).
func TestExportEmissionFactorColumn(t *testing.T) {
	j := loadSampleFor(t, model.TargetAdOpt)
	tr := j.Model.Trade["grid_n1"]
	tr.Export.EmissionFactor = fp(0.3)
	j.Model.Trade["grid_n1"] = tr
	root := emitAdopt(t, j)
	csv := readFileAbs(t, filepath.Join(root, "period1", "node_data", "n1", "carrier_data", "electricity.csv"))
	if !strings.Contains(csv, ";0.35;0.3;") {
		t.Errorf("electricity.csv missing import/export emission factors:\n%s", csv)
	}
}

// A series-valued import limit materializes per timestep in the carrier CSV.
func TestTradeSeriesLimitColumn(t *testing.T) {
	j := loadSampleFor(t, model.TargetAdOpt)
	j.Model.Timeseries["grid_cap"] = model.TimeSeries{Source: "inline", Values: []float64{100, 50}}
	tr := j.Model.Trade["grid_n1"]
	tr.Import.Limit = model.Series("grid_cap")
	j.Model.Trade["grid_n1"] = tr
	if _, err := validateFor(&j, model.TargetAdOpt); err != nil {
		t.Fatalf("validate: %v", err)
	}
	root := emitAdopt(t, j)
	csv := readFileAbs(t, filepath.Join(root, "period1", "node_data", "n1", "carrier_data", "electricity.csv"))
	if !strings.Contains(csv, ";100;") || !strings.Contains(csv, ";50;") {
		t.Errorf("import limit series not materialized per timestep:\n%s", csv)
	}
}

// Storage rate bounds and the async switch patch the database tech's
// Flexibility/Performance blocks via the overrides sidecar.
func TestStorageFlexibilityOverrides(t *testing.T) {
	j := loadSampleFor(t, model.TargetAdOpt)
	b := j.Model.Technologies["battery"]
	b.Storage.MaxHours = nil
	b.Storage.MaxChargeRate = fp(0.25)
	b.Storage.MaxDischargeRate = fp(0.5)
	b.Storage.NoSimultaneousChargeDischarge = true
	j.Model.Technologies["battery"] = b
	if _, err := validateFor(&j, model.TargetAdOpt); err != nil {
		t.Fatalf("validate: %v", err)
	}
	root := emitAdopt(t, j)
	ov := readJSON(t, filepath.Join(root, "_meme_overrides.json"))
	node := dig(t, ov, "n1").(map[string]any)
	var found bool
	for _, patch := range node {
		p := patch.(map[string]any)
		flex, ok := p["Flexibility"].(map[string]any)
		if !ok {
			continue
		}
		found = true
		if flex["charge_rate"] != 0.25 || flex["discharge_rate"] != 0.5 {
			t.Errorf("Flexibility patch = %v", flex)
		}
		perf, ok := p["Performance"].(map[string]any)
		if !ok || perf["allow_only_one_direction"] != 1.0 {
			t.Errorf("Performance patch = %v", p["Performance"])
		}
	}
	if !found {
		t.Fatalf("no Flexibility override written: %v", node)
	}
}

func TestDepthOfDischargeRejected(t *testing.T) {
	j := loadSampleFor(t, model.TargetAdOpt)
	b := j.Model.Technologies["battery"]
	b.Storage.DepthOfDischarge = fp(0.1)
	j.Model.Technologies["battery"] = b
	if _, err := validateFor(&j, model.TargetAdOpt); err == nil ||
		!strings.Contains(err.Error(), "depth_of_discharge") {
		t.Fatalf("want depth_of_discharge rejection, got: %v", err)
	}
}

// --- second tranche -----------------------------------------------------------

// Run-level switches land in ConfigModel.json.
func TestAdoptRunSwitches(t *testing.T) {
	j := loadSampleFor(t, model.TargetAdOpt)
	j.Experiment.Objective = model.ObjectiveMinEmissions
	j.Experiment.EmissionAccounting = "positive_only"
	j.Experiment.Copperplate = true
	j.Experiment.AllowUnmetDemand = &model.AllowUnmetDemand{Enabled: true, PenaltyPrice: fp(3000)}
	j.Experiment.Reporting = &model.Reporting{CaseName: "case7"}
	j.Model.DiscountRate = fp(0.06)
	j.Model.EmissionLimits = nil // min_emissions objective, not a cap
	if _, err := validateFor(&j, model.TargetAdOpt); err != nil {
		t.Fatalf("validate: %v", err)
	}
	cfg := readJSON(t, filepath.Join(emitAdopt(t, j), "ConfigModel.json"))
	if got := dig(t, cfg, "optimization", "objective", "value"); got != "emissions_pos" {
		t.Errorf("objective = %v, want emissions_pos", got)
	}
	if got := dig(t, cfg, "energybalance", "copperplate", "value"); got != 1.0 {
		t.Errorf("copperplate = %v, want 1", got)
	}
	if got := dig(t, cfg, "energybalance", "violation", "value"); got != 3000.0 {
		t.Errorf("violation = %v, want 3000", got)
	}
	if got := dig(t, cfg, "reporting", "case_name", "value"); got != "case7" {
		t.Errorf("case_name = %v, want case7", got)
	}
	if got := dig(t, cfg, "economic", "global_discountrate", "value"); got != 0.06 {
		t.Errorf("global_discountrate = %v, want 0.06", got)
	}
}

// Technology-level extensions patch the database tech via the overrides sidecar.
func TestAdoptPerformancePatches(t *testing.T) {
	j := loadSampleFor(t, model.TargetAdOpt)
	pv := j.Model.Technologies["pv"]
	pv.EmissionFactor = fp(-0.1)
	pv.Operation = &model.Operation{MaxStartups: fp(5), StandbyPower: fp(0.02)}
	pv.Capacity.Decommission = &model.Decommission{Mode: "continuous", Cost: fp(12)}
	pv.Costs = map[string]model.CostClass{"monetary": {FixedOMFraction: fp(0.04)}}
	j.Model.Technologies["pv"] = pv
	if _, err := validateFor(&j, model.TargetAdOpt); err != nil {
		t.Fatalf("validate: %v", err)
	}
	root := emitAdopt(t, j)
	// max_startups/standby_power are dead letters unless dynamics is on.
	cfg := readJSON(t, filepath.Join(root, "ConfigModel.json"))
	if got := dig(t, cfg, "performance", "dynamics", "value"); got != 1.0 {
		t.Errorf("performance.dynamics = %v, want 1 (max_startups/standby_power present)", got)
	}
	ov := readJSON(t, filepath.Join(root, "_meme_overrides.json"))
	// Check node n1: pv's n2 node-override replaces capacity/costs wholesale,
	// so only the n1 instance carries the mutations above.
	node := dig(t, ov, "n1").(map[string]any)
	found := false
	for _, patchAny := range node {
		p := patchAny.(map[string]any)
		perf, ok := p["Performance"].(map[string]any)
		if !ok || perf["emission_factor"] != -0.1 {
			continue
		}
		found = true
		if perf["max_startups"] != 5.0 || perf["standby_power"] != 0.02 {
			t.Errorf("Performance patch = %v", perf)
		}
		if p["decommission"] != "continuous" {
			t.Errorf("decommission = %v", p["decommission"])
		}
		econ := p["Economics"].(map[string]any)
		if econ["OPEX_fixed"] != 0.04 || econ["decommission_cost"] != 12.0 {
			t.Errorf("Economics patch = %v", econ)
		}
	}
	if !found {
		t.Fatalf("no Performance patch for n1 written: %v", node)
	}
}

func TestAdoptSecondTrancheGates(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*model.Job)
		want string
	}{
		{"fuel cost", func(j *model.Job) {
			pv := j.Model.Technologies["pv"]
			pv.Costs = map[string]model.CostClass{"monetary": {FuelCost: model.Num(20)}}
			j.Model.Technologies["pv"] = pv
		}, "fuel_cost"},
		{"explicit timesteps", func(j *model.Job) {
			j.Model.Time.Timesteps = []string{"a", "b"}
		}, "timesteps"},
		{"weights", func(j *model.Job) {
			j.Model.Time.Weights = model.Num(2)
		}, "weights"},
		{"unmet demand without price", func(j *model.Job) {
			j.Experiment.AllowUnmetDemand = &model.AllowUnmetDemand{Enabled: true}
		}, "penalty_price"},
		{"systemwide bound", func(j *model.Job) {
			pv := j.Model.Technologies["pv"]
			pv.Capacity.SystemwideMax = fp(700)
			j.Model.Technologies["pv"] = pv
		}, "systemwide"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			j := loadSampleFor(t, model.TargetAdOpt)
			tc.mut(&j)
			if _, err := validateFor(&j, model.TargetAdOpt); err == nil ||
				!strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want rejection containing %q, got: %v", tc.want, err)
			}
		})
	}
}
