// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package calliope_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/enerplanet/meme/internal/model"
	"github.com/enerplanet/meme/internal/scenarios"
	"github.com/enerplanet/meme/internal/target/calliope"
)

// emit06Dir emits j with the Calliope 0.6 target into a temp dir.
func emit06Dir(t *testing.T, j model.Job) string {
	t.Helper()
	dir := t.TempDir()
	if _, err := (calliope.Calliope06{}).Emit(&j, dir); err != nil {
		t.Fatalf("emit (0.6): %v", err)
	}
	return dir
}

// emit06 validates j for calliope-0.6, emits it and returns model.yaml.
func emit06(t *testing.T, j model.Job) string {
	t.Helper()
	if _, err := validateFor(&j, model.TargetCalliope06); err != nil {
		t.Fatalf("validate (0.6): %v", err)
	}
	return readFile(t, emit06Dir(t, j), "model.yaml")
}

func loadScenario(t *testing.T, file string) model.Job {
	t.Helper()
	j, err := scenarios.Load(file)
	if err != nil {
		t.Fatalf("load %s: %v", file, err)
	}
	return j
}

func wantAll(t *testing.T, doc string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(doc, w) {
			t.Errorf("missing %q in:\n%s", w, doc)
		}
	}
}

// The sample (trimmed the same way as for 0.7) emits the whole 0.6 document
// structure: model/run/techs/locations/links, a conversion_plus CHP, market
// techs, MILP units, and a version pin Calliope checks at load.
func TestCalliope06EmitSample(t *testing.T) {
	j := loadSampleFor(t, model.TargetCalliope06)
	doc := emit06(t, j)
	wantAll(t, doc,
		"calliope_version: 0.6.8",
		"timeseries_data_path: timeseries_data",
		"mode: plan",
		"objective_options:",
		"parent: supply",
		"parent: conversion_plus",
		"parent: transmission",
		"links:",
		"grid_n1_import",
		"grid_n1_export",
		"units_max:",
		"energy_cap_per_unit:",
		"locations:",
	)
	// 0.7-only spellings must not leak into the 0.6 dialect.
	for _, bad := range []string{"base_tech:", "data_tables:", "flow_cap_max", "config:", "nodes:"} {
		if strings.Contains(doc, bad) {
			t.Errorf("0.7 spelling %q leaked into the 0.6 model:\n%s", bad, doc)
		}
	}
}

// Demand profiles become negative file= resources with a datetime index, one
// column per node, under timeseries_data/.
func TestCalliope06MaterializesNegativeDemand(t *testing.T) {
	dir := emit06Dir(t, loadScenario(t, "calliope_plan.json"))
	doc := readFile(t, dir, "model.yaml")
	wantAll(t, doc, `resource: "file=load__resource.csv:n1"`)
	csv := readFile(t, dir, "timeseries_data/load__resource.csv")
	lines := strings.Split(strings.TrimSpace(csv), "\n")
	if lines[0] != ",n1" {
		t.Errorf("header = %q, want ,n1", lines[0])
	}
	if !strings.HasPrefix(lines[1], "2025-01-01 00:00:00,-80") {
		t.Errorf("first row = %q, want a timestamp and the negated demand", lines[1])
	}
	if len(lines) != 7 {
		t.Errorf("%d rows, want header + 6 timesteps", len(lines))
	}
}

// Multi-output conversion: the primary output sets energy_eff, every further
// output tier gets a carrier_ratio relative to the primary output.
func TestCalliope06ConversionPlus(t *testing.T) {
	doc := emit06(t, loadScenario(t, "calliope_conversion.json"))
	wantAll(t, doc,
		"carrier_in: gas",
		"carrier_out: electricity",
		"carrier_out_2: heat",
		"primary_carrier_out: electricity",
		"energy_eff: 0.4",
		"heat: 1.125", // 0.45 / 0.4
	)
}

// Trade: import is a priced supply capped at the limit; export is a flexible
// demand (negative, unforced resource) paid the export price.
func TestCalliope06Trade(t *testing.T) {
	doc := emit06(t, loadScenario(t, "calliope_trade.json"))
	imp := doc[strings.Index(doc, "market_import:"):]
	wantAll(t, imp, "parent: supply", "energy_cap_max: 500", "om_prod: 60", "emissions:", "om_prod: 0.35")
	exp := doc[strings.Index(doc, "market_export:"):]
	wantAll(t, exp, "parent: demand", "resource: -200", "force_resource: false", "om_con: -30")

	// An export without a limit has no resource bound to emit.
	j := loadScenario(t, "calliope_trade.json")
	tr := j.Model.Trade["market"]
	tr.Export.Limit = nil
	j.Model.Trade["market"] = tr
	if _, err := validateFor(&j, model.TargetCalliope06); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Errorf("export without limit should be rejected, got %v", err)
	}
}

// SPORES follows the 0.6 recipe: run.spores_options, a systemwide_cost_max
// group with an infinite monetary cap, and spores_score costs on every
// capacity-bearing tech.
func TestCalliope06Spores(t *testing.T) {
	doc := emit06(t, loadScenario(t, "calliope06_spores.json"))
	wantAll(t, doc,
		"mode: spores",
		"spores_number: 2",
		"slack: 0.1",
		"score_cost_class: spores_score",
		"slack_cost_group: systemwide_cost_max",
		"systemwide_cost_max:",
		"monetary: .inf",
		"spores_score:",
	)
	if strings.Count(doc, "spores_score:\n") != 2 { // pv + wind (the demand tech gets none)
		t.Errorf("every capacity-bearing tech needs a spores_score cost:\n%s", doc)
	}
}

// Portable constraints map onto group_constraints with the coefficient folded
// into the bound; multi-term constraints are rejected up front.
func TestCalliope06GroupConstraints(t *testing.T) {
	doc := emit06(t, loadScenario(t, "calliope06_group_constraints.json"))
	wantAll(t, doc,
		"group_constraints:",
		"max_pv:",
		"techs: [pv]",
		"energy_cap_max: 800",
		"min_gas_power:",
		"carrier_prod_min:",
		"electricity: 100",
		"co2_cap:",
		"cost_max:",
		"emissions: 1.0e+06",
	)

	j := loadScenario(t, "calliope06_group_constraints.json")
	// Negative coefficient flips the sense: -2 * cap(pv) <= -400  <=>  cap(pv) >= 200.
	j.Model.Constraints = []model.Constraint{{Name: "neg", Sense: "<=", Bound: -400,
		Terms: []model.Term{{Coefficient: -2, Variable: model.VarCapacity, Techs: []string{"pv"}}}}}
	wantAll(t, emit06(t, j), "neg:", "energy_cap_min: 200")

	// A consumption cap turns into a (negative) carrier_con_min.
	j.Model.Constraints = []model.Constraint{{Name: "gas_cap", Sense: "<=", Bound: 500,
		Terms: []model.Term{{Coefficient: 1, Variable: model.VarFlowIn, Techs: []string{"gasgen"}}}}}
	wantAll(t, emit06(t, j), "gas_cap:", "carrier_con_min:", "gas: -500")

	j.Model.Constraints = []model.Constraint{{Name: "two", Sense: "<=", Bound: 1,
		Terms: []model.Term{
			{Coefficient: 1, Variable: model.VarCapacity, Techs: []string{"pv"}},
			{Coefficient: -1, Variable: model.VarCapacity, Techs: []string{"gasgen"}},
		}}}
	if _, err := validateFor(&j, model.TargetCalliope06); err == nil || !strings.Contains(err.Error(), "single quantity") {
		t.Errorf("multi-term constraint should be rejected for 0.6, got %v", err)
	}
}

// min_emissions swaps the objective weights onto the emissions class.
func TestCalliope06MinEmissionsObjective(t *testing.T) {
	doc := emit06(t, loadScenario(t, "calliope06_min_emissions.json"))
	run := doc[strings.Index(doc, "run:"):strings.Index(doc, "techs:")]
	wantAll(t, run, "monetary: 0", "emissions: 1", "sense: minimize")

	// Without any emission source there is nothing to minimize.
	j := loadScenario(t, "calliope06_min_emissions.json")
	gas := j.Model.Carriers["gas"]
	gas.CO2Intensity = nil
	j.Model.Carriers["gas"] = gas
	if _, err := validateFor(&j, model.TargetCalliope06); err == nil || !strings.Contains(err.Error(), "emission source") {
		t.Errorf("min_emissions without sources should be rejected, got %v", err)
	}
}

// Typical days: k-means clustering with k = periods and a fixed random seed.
func TestCalliope06TypicalDays(t *testing.T) {
	doc := emit06(t, loadScenario(t, "calliope06_typical_days.json"))
	wantAll(t, doc, "function: apply_clustering", "clustering_func: kmeans", "k: 2", "random_seed: 23")
}

// 0.6-specific gates: time weights, asymmetric storage efficiency (warning),
// mixed cyclic storage (warning), differing storage carriers.
func TestCalliope06Gates(t *testing.T) {
	j := loadScenario(t, "calliope_storage.json")
	j.Model.Time.Weights = model.Num(2)
	if _, err := validateFor(&j, model.TargetCalliope06); err == nil || !strings.Contains(err.Error(), "time.weights") {
		t.Errorf("time.weights should be rejected for 0.6, got %v", err)
	}

	j = loadScenario(t, "calliope_storage.json")
	b := j.Model.Technologies["batt"]
	ce, de := 0.9, 0.8
	b.Storage.ChargeEff, b.Storage.DischargeEff = &ce, &de
	j.Model.Technologies["batt"] = b
	warns, err := validateFor(&j, model.TargetCalliope06)
	if err != nil {
		t.Fatalf("asymmetric efficiencies should only warn: %v", err)
	}
	if joined := strings.Join(warns, "\n"); !strings.Contains(joined, "sqrt") {
		t.Errorf("expected a sqrt warning, got %v", warns)
	}
	wantAll(t, readFile(t, emit06Dir(t, j), "model.yaml"), "energy_eff: 0.848528137423857")

	j = loadScenario(t, "calliope_storage.json")
	j.Model.Carriers["heat"] = model.Carrier{Unit: "MWh"}
	b = j.Model.Technologies["batt"]
	b.CarrierIn = model.StringList{"heat"}
	j.Model.Technologies["batt"] = b
	if _, err := validateFor(&j, model.TargetCalliope06); err == nil || !strings.Contains(err.Error(), "one carrier") {
		t.Errorf("storage with differing carriers should be rejected, got %v", err)
	}
}

// A native["calliope-0.6"] fragment deep-merges into the nested 0.6 mapping:
// it extends constraints instead of replacing the emitted block, and a
// native.calliope (0.7 syntax) block is ignored by the 0.6 target.
func TestCalliope06NativeDeepMerge(t *testing.T) {
	j := loadScenario(t, "calliope_plan.json")
	pv := j.Model.Technologies["pv"]
	pv.Native = &model.Native{
		Calliope06: json.RawMessage(`{"constraints": {"parasitic_eff": 0.98}, "essentials": {"color": "#ffcc00"}}`),
		Calliope:   json.RawMessage(`{"flow_out_parasitic_eff": 0.98}`),
	}
	j.Model.Technologies["pv"] = pv
	warns, err := validateFor(&j, model.TargetCalliope06)
	if err != nil {
		t.Fatal(err)
	}
	if joined := strings.Join(warns, "\n"); !strings.Contains(joined, "native.calliope block is ignored") {
		t.Errorf("expected a warning that native.calliope is ignored for calliope-0.6, got %v", warns)
	}
	doc := readFile(t, emit06Dir(t, j), "model.yaml")
	pvBlock := doc[strings.Index(doc, "  pv:"):strings.Index(doc, "locations:")]
	wantAll(t, pvBlock, "parasitic_eff: 0.98", "energy_cap_max: 1000", "lifetime: 25", `color: "#ffcc00"`)
	if strings.Contains(doc, "flow_out_parasitic_eff") {
		t.Errorf("0.7 native block leaked into the 0.6 model:\n%s", doc)
	}
}

// The run plan drives the version-specific CLI.
func TestCalliope06Plan(t *testing.T) {
	j := loadScenario(t, "calliope_plan.json")
	plan, err := (calliope.Calliope06{}).Plan(&j, "/x/input/model.yaml", planDirs("/x"))
	if err != nil {
		t.Fatal(err)
	}
	if plan.Command[0] != "calliope-0.6" || plan.Target != model.TargetCalliope06 {
		t.Errorf("plan = %+v, want the calliope-0.6 CLI", plan)
	}
}

// Parity with the 0.7 target (issue #7): a multi-node tech's node data reaches
// every node on 0.6 as well — one timeseries_data column per node, each
// referenced as file=<file>:<node>, and a per-node demand_profile override
// lands in its node's column.
func TestCalliope06MultiNodeNodeData(t *testing.T) {
	j := loadScenario(t, "calliope_transmission.json")
	for _, id := range []string{"gen", "load"} {
		tech := j.Model.Technologies[id]
		tech.Node = model.StringList{"n1", "n2"}
		j.Model.Technologies[id] = tech
	}
	setSeries(&j, "cf", 0, 0.2, 0.6, 0.9)
	setSeries(&j, "dem2", 7, 8, 9)
	gen := j.Model.Technologies["gen"]
	gen.Operation = &model.Operation{MaxPU: model.Series("cf")}
	j.Model.Technologies["gen"] = gen
	load := j.Model.Technologies["load"]
	load.NodeOverrides = map[string]model.NodeOverride{"n2": {DemandProfile: model.Series("dem2")}}
	j.Model.Technologies["load"] = load

	doc := emit06(t, j)
	wantAll(t, doc,
		`resource: "file=gen__resource.csv:n1"`, `resource: "file=gen__resource.csv:n2"`,
		`resource: "file=load__resource.csv:n1"`, `resource: "file=load__resource.csv:n2"`,
		"resource_unit: energy_per_cap")
	dir := emit06Dir(t, j)
	genCSV := strings.Split(strings.TrimSpace(readFile(t, dir, "timeseries_data/gen__resource.csv")), "\n")
	if genCSV[0] != ",n1,n2" || !strings.HasPrefix(genCSV[1], "2025-01-01 00:00:00,0,0") || !strings.HasPrefix(genCSV[2], "2025-01-01 01:00:00,0.2,0.2") {
		t.Errorf("availability not written per node: %q", genCSV[:3])
	}
	loadCSV := strings.Split(strings.TrimSpace(readFile(t, dir, "timeseries_data/load__resource.csv")), "\n")
	if loadCSV[0] != ",n1,n2" || !strings.HasPrefix(loadCSV[1], "2025-01-01 00:00:00,-100,-7") {
		t.Errorf("demand override not written per node (negative in 0.6): %q", loadCSV[:2])
	}
}

// Operate mode on 0.6 (issue #8): the import market and the line get an
// explicit energy_cap_equals — 0.6's documented fallback from energy_cap_max
// to a fixed size did not reach the market tech, which ran above its limit.
// An expandable line is installed at capacity.max with a warning.
func TestCalliope06OperateFixesImportAndLine(t *testing.T) {
	j := loadScenario(t, "calliope_operate_limits.json")
	doc := emit06(t, j)
	wantAll(t, block(doc, "market_import"), "energy_cap_max: 50", "energy_cap_equals: 50")
	wantAll(t, block(doc, "line"), "energy_cap_equals: 40")

	l := j.Model.Transmission["line"]
	l.Capacity = &model.Capacity{Expandable: true, Max: fp(40)}
	j.Model.Transmission["line"] = l
	warns, err := validateFor(&j, model.TargetCalliope06)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if joined := strings.Join(warns, "\n"); !strings.Contains(joined, `transmission "line"`) || !strings.Contains(joined, "capacity.max") {
		t.Errorf("expandable line in operate mode must warn, got %v", warns)
	}
	doc = readFile(t, emit06Dir(t, j), "model.yaml")
	wantAll(t, block(doc, "line"), "energy_cap_max: 40", "energy_cap_equals: 40")
}
