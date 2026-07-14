// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package target_test

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/enerplanet/meme/internal/model"
	"github.com/enerplanet/meme/internal/scenarios"
	"github.com/enerplanet/meme/internal/target"
	_ "github.com/enerplanet/meme/internal/target/all"
)

// emitScenario loads a scenario payload, validates it for the target, emits it,
// and returns every emitted file's contents concatenated (so a check can look
// for a marker regardless of which file it lands in).
func emitScenario(t *testing.T, file string, tgt model.Target) string {
	t.Helper()
	j, err := scenarios.Load(file)
	if err != nil {
		t.Fatalf("load %s: %v", file, err)
	}
	if _, err := target.ValidateFor(&j, tgt); err != nil {
		t.Fatalf("%s: validate for %s: %v", file, tgt, err)
	}
	impl, err := target.For(tgt)
	if err != nil {
		t.Fatalf("target %s: %v", tgt, err)
	}
	dir := t.TempDir()
	if _, err := impl.Emit(&j, dir); err != nil {
		t.Fatalf("%s: emit for %s: %v", file, tgt, err)
	}
	var sb strings.Builder
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if c, e := os.ReadFile(p); e == nil {
				sb.Write(c)
				sb.WriteByte('\n')
			}
		}
		return nil
	})
	return sb.String()
}

// TestScenarios exercises each framework's use cases: every scenario must
// validate + emit cleanly, and its emitted tree must contain the expected
// framework-native markers.
func TestScenarios(t *testing.T) {
	cases := []struct {
		file   string
		target model.Target
		want   []string
	}{
		// --- PyPSA ---
		{"pypsa_generators.json", model.TargetPyPSA, []string{"p_nom_min", "capital_cost", "marginal_cost"}},
		{"pypsa_conversion.json", model.TargetPyPSA, []string{"bus2", "efficiency2"}},
		{"pypsa_storage.json", model.TargetPyPSA, []string{"efficiency_store", "standing_loss", "state_of_charge_initial", "cyclic_state_of_charge"}},
		{"pypsa_transmission.json", model.TargetPyPSA, []string{"n1::electricity", "n2::electricity"}},
		{"pypsa_trade.json", model.TargetPyPSA, []string{"market_import", "market_export"}},
		{"pypsa_committable.json", model.TargetPyPSA, []string{"committable", "start_up_cost", "ramp_limit_up"}},
		{"pypsa_emission.json", model.TargetPyPSA, []string{"primary_energy", "co2_emissions"}},
		{"pypsa_constraints.json", model.TargetPyPSA, []string{"max_pv"}},
		{"pypsa_node_override.json", model.TargetPyPSA, []string{"pv@n1", "pv@n2", "500", "1000"}},
		{"pypsa_physics_precomputed.json", model.TargetPyPSA, []string{"physics", "pv_cf", "performance"}},
		{"pypsa_operate.json", model.TargetPyPSA, []string{"gen", "300"}},
		{"pypsa_alternatives.json", model.TargetPyPSA, []string{"p_nom_extendable", "True"}},
		{"pypsa_stochastic.json", model.TargetPyPSA, []string{"fuel_high", "marginal_cost", "90"}},
		{"pypsa_constraints_sidecar.json", model.TargetPyPSA, []string{"force_pv", "capacity"}},
		{"pypsa_trade_price_series.json", model.TargetPyPSA, []string{"market_import"}},

		// --- Calliope ---
		{"calliope_plan.json", model.TargetCalliope, []string{"mode: base", "base_tech: supply", "solver: cbc"}},
		{"calliope_spores.json", model.TargetCalliope, []string{"mode: spores", "number: 5"}},
		{"calliope_operate.json", model.TargetCalliope, []string{"mode: operate"}},
		{"calliope_custommath.json", model.TargetCalliope, []string{"additional_math.yaml", "extra_math", "max_pv", "cost_flow_in"}},
		{"calliope_storage.json", model.TargetCalliope, []string{"base_tech: storage", "storage_cap_max", "storage_initial", "cost_storage_cap"}},
		{"calliope_source_native.json", model.TargetCalliope, []string{"source_use_max", "area_use_max"}},
		{"calliope_conversion.json", model.TargetCalliope, []string{"base_tech: conversion", "carrier_in: gas", "meme_ratio_chp_out_heat", "NOT [chp] in techs"}},
		{"calliope_transmission.json", model.TargetCalliope, []string{"base_tech: transmission", "link_from: n1", "link_to: n2"}},
		{"calliope_node_override.json", model.TargetCalliope, []string{"cost_flow_cap", "300000"}},
		{"calliope_trade.json", model.TargetCalliope, []string{"market_import", "market_export", "cost_flow_in", "-30"}},
		{"calliope_committable.json", model.TargetCalliope, []string{"cap_method: integer", "flow_cap_per_unit: 100", "extra_math: [milp]"}},
		{"calliope_resample.json", model.TargetCalliope, []string{"resample:", "timesteps: 3h"}},
		{"calliope_piecewise.json", model.TargetCalliope, []string{"piecewise_x", "piecewise_constraints", "breakpoints"}},

		// --- AdOpT-NET0 ---
		{"adopt_demand_trade.json", model.TargetAdOpt, []string{";Demand;", "glpk"}},
		{"adopt_storage.json", model.TargetAdOpt, []string{"Storage_Battery", "OPEX_fixed", "glpk"}},
		{"adopt_pv_climate.json", model.TargetAdOpt, []string{"Photovoltaic", ";ghi;", "800"}},
		{"adopt_emission.json", model.TargetAdOpt, []string{"costs_emissionlimit"}},
		{"adopt_wind.json", model.TargetAdOpt, []string{"WindTurbine_Onshore_1500", ";ws10;", "glpk"}},
		{"adopt_pareto.json", model.TargetAdOpt, []string{"pareto", "Storage_Battery", "glpk"}},
		{"adopt_monte_carlo.json", model.TargetAdOpt, []string{"monte_carlo", "Storage_Battery", "glpk"}},
		{"adopt_typicaldays.json", model.TargetAdOpt, []string{"typicaldays", "Storage_Battery", "glpk"}},
		{"adopt_node_override.json", model.TargetAdOpt, []string{"Storage_Battery", "size_max", "40"}},
		{"adopt_min_emissions.json", model.TargetAdOpt, []string{"emissions_net", "Photovoltaic"}},
	}

	for _, c := range cases {
		c := c
		t.Run(c.file, func(t *testing.T) {
			out := emitScenario(t, c.file, c.target)
			for _, w := range c.want {
				if !strings.Contains(out, w) {
					t.Errorf("%s (%s): emitted tree missing %q", c.file, c.target, w)
				}
			}
		})
	}
}

// TestScenariosCapabilityGating spot-checks that use cases only valid on one
// target are rejected on the others (the capability matrix does its job).
func TestScenariosCapabilityGating(t *testing.T) {
	load := func(file string) model.Job {
		j, err := scenarios.Load(file)
		if err != nil {
			t.Fatalf("load %s: %v", file, err)
		}
		return j
	}
	// operate & alternatives are supported by PyPSA (rolling horizon / MGA) and
	// Calliope, but not by AdOpT.
	for _, f := range []string{"calliope_operate.json", "calliope_spores.json"} {
		j := load(f)
		if _, err := target.ValidateFor(&j, model.TargetAdOpt); err == nil {
			t.Errorf("%s should be rejected for AdOpT (unsupported mode)", f)
		}
	}
	// pareto & monte_carlo are AdOpT-only run modes; time_aggregation is not a
	// PyPSA capability. Each must reject on the targets that can't honor it.
	for _, f := range []string{"adopt_pareto.json", "adopt_monte_carlo.json"} {
		j := load(f)
		for _, tgt := range []model.Target{model.TargetPyPSA, model.TargetCalliope} {
			if _, err := target.ValidateFor(&j, tgt); err == nil {
				t.Errorf("%s should be rejected for %s (AdOpT-only mode)", f, tgt)
			}
		}
	}
	td := load("adopt_typicaldays.json")
	if _, err := target.ValidateFor(&td, model.TargetPyPSA); err == nil {
		t.Errorf("adopt_typicaldays.json should be rejected for PyPSA (no time_aggregation)")
	}
	// typical_days clustering was removed in Calliope 0.7; only resample maps.
	if _, err := target.ValidateFor(&td, model.TargetCalliope); err == nil {
		t.Errorf("adopt_typicaldays.json should be rejected for Calliope (resample only)")
	}
}

// TestAdoptRunModeConfig proves the AdOpT run-mode scenarios are actually
// runnable: the canonical mode must land in ConfigModel.json's optimization
// block (adopt_net0 branches its single quick_solve() on these fields, so a mode
// that never reaches the config would silently solve a plain cost-optimal plan).
func TestAdoptRunModeConfig(t *testing.T) {
	// optimization returns the parsed optimization block of a scenario's emitted
	// ConfigModel.json.
	optimization := func(file string) map[string]any {
		j, err := scenarios.Load(file)
		if err != nil {
			t.Fatalf("load %s: %v", file, err)
		}
		if _, err := target.ValidateFor(&j, model.TargetAdOpt); err != nil {
			t.Fatalf("%s: validate: %v", file, err)
		}
		impl, _ := target.For(model.TargetAdOpt)
		root, err := impl.Emit(&j, t.TempDir())
		if err != nil {
			t.Fatalf("%s: emit: %v", file, err)
		}
		raw, err := os.ReadFile(filepath.Join(root, "ConfigModel.json"))
		if err != nil {
			t.Fatalf("%s: read ConfigModel.json: %v", file, err)
		}
		var cfg map[string]any
		if err := json.Unmarshal(raw, &cfg); err != nil {
			t.Fatalf("%s: decode ConfigModel.json: %v", file, err)
		}
		opt, ok := cfg["optimization"].(map[string]any)
		if !ok {
			t.Fatalf("%s: optimization block missing", file)
		}
		return opt
	}
	// value unwraps adopt's {"value": X} field wrapper.
	value := func(m map[string]any, key string) any {
		w, _ := m[key].(map[string]any)
		return w["value"]
	}

	// pareto -> objective "pareto", honoring the pareto.points option.
	opt := optimization("adopt_pareto.json")
	if got := value(opt, "objective"); got != "pareto" {
		t.Errorf("pareto: objective = %v, want pareto", got)
	}
	if got := value(opt, "pareto_points"); got != float64(6) {
		t.Errorf("pareto: pareto_points = %v, want 6 (from options)", got)
	}

	// monte_carlo -> monte_carlo.N > 0, honoring the monte_carlo.N option.
	opt = optimization("adopt_monte_carlo.json")
	mc, _ := opt["monte_carlo"].(map[string]any)
	if got := value(mc, "N"); got != float64(25) {
		t.Errorf("monte_carlo: N = %v, want 25 (from options)", got)
	}

	// typical_days time_aggregation -> typicaldays.N = requested periods.
	opt = optimization("adopt_typicaldays.json")
	td, _ := opt["typicaldays"].(map[string]any)
	if got := value(td, "N"); got != float64(7) {
		t.Errorf("typicaldays: N = %v, want 7 (from time_aggregation.periods)", got)
	}
}
