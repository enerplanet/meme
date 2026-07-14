// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package calliope_test

import (
	"strings"
	"testing"

	"github.com/enerplanet/meme/internal/model"
)

func fp(v float64) *float64 { return &v }

func wantContains(t *testing.T, doc string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(doc, w) {
			t.Errorf("emitted model.yaml missing %q:\n%s", w, doc)
		}
	}
}

// First-class experiment.operate beats the emitter defaults (12h/24h).
func TestOperateFirstClass(t *testing.T) {
	j := loadSampleFor(t, model.TargetCalliope)
	j.Experiment.Mode = model.ModeOperate
	j.Experiment.Operate = &model.OperateOptions{Window: "6H", Horizon: "12H"}
	wantContains(t, emitCalliope(t, j), "window: 6h", "horizon: 12h")
}

// First-class experiment.alternatives feeds config.solve.spores plus the
// spores_slack data definition.
func TestAlternativesFirstClass(t *testing.T) {
	j := loadSampleFor(t, model.TargetCalliope)
	j.Experiment.Mode = model.ModeAlternatives
	j.Experiment.Alternatives = &model.AlternativesOptions{
		Number: 7, Slack: fp(0.2), ScoringAlgorithm: "random",
	}
	doc := emitCalliope(t, j)
	wantContains(t, doc, "number: 7", "scoring_algorithm: random", "spores_slack: 0.2")
}

// Canonical solver fields map onto the emitted solver's own option names.
func TestSolverOptionsMapping(t *testing.T) {
	j := loadSampleFor(t, model.TargetCalliope)
	j.Experiment.Solver = model.Solver{Name: "gurobi", TimeLimit: fp(3600), MIPGap: fp(0.01)}
	wantContains(t, emitCalliope(t, j), "solver_options:", "TimeLimit: 3600", "MIPGap: 0.01")

	// Unmapped solver: options dropped with a warning, not silently.
	j = loadSampleFor(t, model.TargetCalliope)
	j.Experiment.Solver = model.Solver{Name: "glpk", TimeLimit: fp(3600)}
	warns, err := validateFor(&j, model.TargetCalliope)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	found := false
	for _, w := range warns {
		if strings.Contains(w, "not mapped for this solver on calliope") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected an unmapped-solver warning, got %v", warns)
	}
}

func TestStorageExtras(t *testing.T) {
	j := loadSampleFor(t, model.TargetCalliope)
	b := j.Model.Technologies["battery"]
	b.Storage.MaxHours = nil // rates and the fixed ratio are mutually exclusive
	b.Storage.MaxChargeRate = fp(0.25)
	b.Storage.DepthOfDischarge = fp(0.1)
	b.Storage.NoSimultaneousChargeDischarge = true
	j.Model.Technologies["battery"] = b
	if _, err := validateFor(&j, model.TargetCalliope); err != nil {
		t.Fatalf("validate: %v", err)
	}
	wantContains(t, emitCalliope(t, j),
		"flow_cap_per_storage_cap_max: 0.25",
		"storage_discharge_depth: 0.1",
		"force_async_flow: true")
}

func TestAsymmetricStorageRatesRejected(t *testing.T) {
	j := loadSampleFor(t, model.TargetCalliope)
	b := j.Model.Technologies["battery"]
	b.Storage.MaxHours = nil
	b.Storage.MaxChargeRate = fp(0.25)
	b.Storage.MaxDischargeRate = fp(0.5)
	j.Model.Technologies["battery"] = b
	if _, err := validateFor(&j, model.TargetCalliope); err == nil ||
		!strings.Contains(err.Error(), "must be equal") {
		t.Fatalf("want asymmetric-rate rejection, got: %v", err)
	}
}

func TestTransmissionExtras(t *testing.T) {
	j := loadSampleFor(t, model.TargetCalliope)
	l := j.Model.Transmission["line_n1_n2"]
	l.Distance = fp(100)
	l.LossPerDistance = fp(0.0001)
	l.MinFlow = fp(0.1)
	j.Model.Transmission["line_n1_n2"] = l
	if _, err := validateFor(&j, model.TargetCalliope); err != nil {
		t.Fatalf("validate: %v", err)
	}
	wantContains(t, emitCalliope(t, j),
		"flow_out_eff_per_distance: 0.9999",
		"flow_out_min_relative: 0.1")
}

func TestTradeSeriesLimitRejected(t *testing.T) {
	j := loadSampleFor(t, model.TargetCalliope)
	tr := j.Model.Trade["grid_n1"]
	tr.Import.Limit = model.Series("load_n1")
	j.Model.Trade["grid_n1"] = tr
	if _, err := validateFor(&j, model.TargetCalliope); err == nil ||
		!strings.Contains(err.Error(), "time-series limit") {
		t.Fatalf("want series-limit rejection, got: %v", err)
	}
}

// Export emission factors ride the co2 cost class on the export market tech.
func TestExportEmissionFactor(t *testing.T) {
	j := loadSampleFor(t, model.TargetCalliope)
	tr := j.Model.Trade["grid_n1"]
	tr.Export.EmissionFactor = fp(0.3)
	j.Model.Trade["grid_n1"] = tr
	doc := emitCalliope(t, j)
	if !strings.Contains(doc, "grid_n1_export") || !strings.Contains(doc, "0.3") {
		t.Errorf("export emission factor not emitted:\n%s", doc)
	}
}

// --- second tranche -----------------------------------------------------------

func TestCalliopeSecondTranche(t *testing.T) {
	j := loadSampleFor(t, model.TargetCalliope)
	// Curtailable demand -> sink_use_max data table.
	d := j.Model.Technologies["elec_demand"]
	d.DemandCurtailable = true
	j.Model.Technologies["elec_demand"] = d
	// Fixed dispatch -> source_use_equals; systemwide bound; inactive tech.
	pv := j.Model.Technologies["pv"]
	pv.Operation = &model.Operation{EqualsPU: model.Num(0.4)}
	pv.Capacity.SystemwideMax = fp(700)
	pv.EmissionFactor = fp(0.05)
	j.Model.Technologies["pv"] = pv
	no := false
	ccgt := j.Model.Technologies["ccgt"]
	ccgt.Active = &no
	j.Model.Technologies["ccgt"] = ccgt
	// Global discount rate + weights + feasibility + reporting.
	j.Model.DiscountRate = fp(0.09)
	j.Model.Time.Weights = model.Num(2)
	j.Experiment.AllowUnmetDemand = &model.AllowUnmetDemand{Enabled: true}
	j.Experiment.Reporting = &model.Reporting{SaveLogs: "logs", ShadowPrices: []string{"system_balance"}}
	if _, err := validateFor(&j, model.TargetCalliope); err != nil {
		t.Fatalf("validate: %v", err)
	}
	doc := emitCalliope(t, j)
	wantContains(t, doc,
		"sink_use_max",
		"source_use_equals: 0.4",
		"flow_cap_max_systemwide: 700",
		"active: false",
		"cost_interest_rate:",
		"data: 0.09",
		"timestep_weights: 2",
		"ensure_feasibility: true",
		"save_logs: logs",
		"shadow_prices:",
		"cost_flow_out:")
	if !strings.Contains(doc, "co2") {
		t.Errorf("tech emission factor not carried as a co2 cost:\n%s", doc)
	}
}

// Decommission "continuous" keeps existing capacity as an upper bound only.
func TestCalliopeDecommissionContinuous(t *testing.T) {
	j := loadSampleFor(t, model.TargetCalliope)
	pv := j.Model.Technologies["pv"]
	pv.Capacity = &model.Capacity{Existing: 300, Expandable: false,
		Decommission: &model.Decommission{Mode: "continuous"}}
	pv.NodeOverrides = nil
	j.Model.Technologies["pv"] = pv
	doc := emitCalliope(t, j)
	if !strings.Contains(doc, "flow_cap_max: 300") {
		t.Errorf("existing capacity not bounded:\n%s", doc)
	}
	// pv's own tech block must not pin flow_cap_min to the existing size.
	techBlock := doc[strings.Index(doc, "  pv:"):]
	if end := strings.Index(techBlock[2:], "\n  "); end > 0 {
		techBlock = techBlock[:end+2]
	}
	if strings.Contains(techBlock, "flow_cap_min: 300") {
		t.Errorf("continuous decommission must not force flow_cap_min:\n%s", techBlock)
	}
}

// Transmission per-distance investment cost lands as cost_flow_cap_per_distance.
func TestCalliopeTransmissionDistanceCost(t *testing.T) {
	j := loadSampleFor(t, model.TargetCalliope)
	l := j.Model.Transmission["line_n1_n2"]
	l.Costs = map[string]model.CostClass{"monetary": {InvestmentPerCapacityDistance: fp(900)}}
	j.Model.Transmission["line_n1_n2"] = l
	if _, err := validateFor(&j, model.TargetCalliope); err != nil {
		t.Fatalf("validate: %v", err)
	}
	wantContains(t, emitCalliope(t, j), "cost_flow_cap_per_distance:", "data: 900")
}

// Fuel cost merges with the carrier co2 intensity into one cost_flow_in param.
func TestCalliopeFuelPlusCO2(t *testing.T) {
	j := loadSampleFor(t, model.TargetCalliope)
	chp := j.Model.Technologies["chp"]
	chp.Costs = map[string]model.CostClass{"monetary": {FuelCost: model.Num(25)}}
	j.Model.Technologies["chp"] = chp
	if _, err := validateFor(&j, model.TargetCalliope); err != nil {
		t.Fatalf("validate: %v", err)
	}
	doc := emitCalliope(t, j)
	// gas has co2_intensity in the sample, so cost_flow_in carries both classes.
	if !strings.Contains(doc, "cost_flow_in:") || !strings.Contains(doc, "[monetary, co2]") {
		t.Errorf("merged cost_flow_in missing:\n%s", doc)
	}
}

// Transmission emissions ride the co2 cost class on the link flow.
func TestCalliopeTransmissionEmissionFactor(t *testing.T) {
	j := loadSampleFor(t, model.TargetCalliope)
	l := j.Model.Transmission["line_n1_n2"]
	l.EmissionFactor = fp(0.001)
	j.Model.Transmission["line_n1_n2"] = l
	if _, err := validateFor(&j, model.TargetCalliope); err != nil {
		t.Fatalf("validate: %v", err)
	}
	doc := emitCalliope(t, j)
	block := doc[strings.Index(doc, "line_n1_n2:"):]
	if !strings.Contains(block, "cost_flow_out:") || !strings.Contains(block, "index: co2") {
		t.Errorf("transmission emission factor not emitted as co2 cost:\n%s", block[:400])
	}
}
