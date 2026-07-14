// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package model_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/enerplanet/meme/internal/model"
)

func fp(v float64) *float64 { return &v }

func baseModel() model.Model {
	return model.Model{
		Metadata:     model.Metadata{Name: "t"},
		Time:         model.TimeConfig{Start: "2030-01-01", End: "2030-01-02", Resolution: "1H"},
		Carriers:     map[string]model.Carrier{"el": {}, "gas": {}, "heat": {}},
		Nodes:        map[string]model.Node{"n1": {}, "n2": {}},
		Technologies: map[string]model.Technology{},
	}
}

func wantValidateErr(t *testing.T, m *model.Model, substr string) {
	t.Helper()
	err := m.Validate()
	if err == nil || !strings.Contains(err.Error(), substr) {
		t.Fatalf("want error containing %q, got: %v", substr, err)
	}
}

func TestFlowRatioRules(t *testing.T) {
	mk := func(flows []model.Flow) model.Model {
		m := baseModel()
		m.Technologies["chp"] = model.Technology{
			Role: model.RoleConversion, Node: model.StringList{"n1"}, Flows: flows,
		}
		return m
	}

	// A non-reference flow without a ratio would silently zero out the port.
	m := mk([]model.Flow{
		{Carrier: "gas", Direction: model.FlowIn, Reference: true},
		{Carrier: "el", Direction: model.FlowOut}, // ratio missing
	})
	wantValidateErr(t, &m, "needs a positive ratio")

	// The reference must be an input flow.
	m = mk([]model.Flow{
		{Carrier: "gas", Direction: model.FlowIn, Ratio: 1},
		{Carrier: "el", Direction: model.FlowOut, Reference: true},
	})
	wantValidateErr(t, &m, "reference must be an input flow")

	// A well-formed CHP passes (reference omits its ratio).
	m = mk([]model.Flow{
		{Carrier: "gas", Direction: model.FlowIn, Reference: true},
		{Carrier: "el", Direction: model.FlowOut, Ratio: 0.4},
		{Carrier: "heat", Direction: model.FlowOut, Ratio: 0.45},
	})
	if err := m.Validate(); err != nil {
		t.Fatalf("valid flows rejected: %v", err)
	}
}

func TestRangeChecks(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*model.Model)
		want string
	}{
		{"charge_eff above 1", func(m *model.Model) {
			m.Technologies["s"] = model.Technology{Role: model.RoleStorage, Node: model.StringList{"n1"},
				Storage: &model.Storage{ChargeEff: fp(1.2)}}
		}, "charge_eff must be within (0,1]"},
		{"max_hours with rate bound", func(m *model.Model) {
			m.Technologies["s"] = model.Technology{Role: model.RoleStorage, Node: model.StringList{"n1"},
				Storage: &model.Storage{MaxHours: fp(4), MaxChargeRate: fp(0.25)}}
		}, "cannot be combined"},
		{"depth of discharge out of range", func(m *model.Model) {
			m.Technologies["s"] = model.Technology{Role: model.RoleStorage, Node: model.StringList{"n1"},
				Storage: &model.Storage{DepthOfDischarge: fp(1.5)}}
		}, "depth_of_discharge must be within [0,1]"},
		{"capacity min above max", func(m *model.Model) {
			m.Technologies["g"] = model.Technology{Role: model.RoleSupply, Node: model.StringList{"n1"},
				CarrierOut: model.StringList{"el"}, Capacity: &model.Capacity{Min: fp(10), Max: fp(5)}}
		}, "exceeds capacity.max"},
		{"negative lifetime", func(m *model.Model) {
			m.Technologies["g"] = model.Technology{Role: model.RoleSupply, Node: model.StringList{"n1"},
				CarrierOut: model.StringList{"el"}, Lifetime: fp(-1)}
		}, "lifetime must be positive"},
		{"self-loop transmission", func(m *model.Model) {
			m.Transmission = map[string]model.Transmission{"l": {Carrier: "el", From: "n1", To: "n1"}}
		}, "from and to are the same node"},
		{"total distance loss", func(m *model.Model) {
			m.Transmission = map[string]model.Transmission{"l": {Carrier: "el", From: "n1", To: "n2",
				LossPerDistance: fp(0.01), Distance: fp(200)}}
		}, "would lose its whole flow"},
		{"trade without sides", func(m *model.Model) {
			m.Trade = map[string]model.Trade{"tr": {Node: "n1", Carrier: "el"}}
		}, "neither import nor export"},
		{"negative trade limit", func(m *model.Model) {
			m.Trade = map[string]model.Trade{"tr": {Node: "n1", Carrier: "el",
				Import: &model.TradeSide{Limit: model.Num(-5)}}}
		}, "limit must not be negative"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := baseModel()
			tc.mut(&m)
			wantValidateErr(t, &m, tc.want)
		})
	}
}

// User-defined series must not squat on the interner's reserved prefix.
func TestReservedInlinePrefix(t *testing.T) {
	m := baseModel()
	m.Timeseries = map[string]model.TimeSeries{"_inline:x": {Source: "inline", Values: []float64{1}}}
	if err := m.InternInlineSeries(); err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("want reserved-prefix rejection, got: %v", err)
	}
}

func TestExperimentValidate(t *testing.T) {
	cases := []struct {
		name string
		exp  model.Experiment
		want string
	}{
		{"bad scoring algorithm", model.Experiment{Alternatives: &model.AlternativesOptions{ScoringAlgorithm: "genetic"}},
			"scoring_algorithm"},
		{"bad monte carlo class", model.Experiment{MonteCarlo: &model.MonteCarloOptions{On: []string{"fuel_price"}}},
			"monte_carlo.on"},
		{"non-positive sd", model.Experiment{MonteCarlo: &model.MonteCarloOptions{StandardDeviation: fp(0)}},
			"standard_deviation must be positive"},
		{"non-positive time limit", model.Experiment{Solver: model.Solver{TimeLimit: fp(0)}},
			"time_limit must be positive"},
		{"sweep without values", model.Experiment{Sweep: []model.SweepAxis{{Parameter: "x"}}},
			"has no values"},
		{"scenario without name", model.Experiment{Scenarios: []model.Scenario{{Weight: 1}}},
			"has no name"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.exp.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want error containing %q, got: %v", tc.want, err)
			}
		})
	}
	ok := model.Experiment{
		Mode:         model.ModeAlternatives,
		Alternatives: &model.AlternativesOptions{Number: 5, Slack: fp(0.1), ScoringAlgorithm: "random"},
		Solver:       model.Solver{Name: "gurobi", TimeLimit: fp(3600), MIPGap: fp(0.01)},
	}
	if err := ok.Validate(); err != nil {
		t.Fatalf("valid experiment rejected: %v", err)
	}
}

func TestOptionWarnings(t *testing.T) {
	e := model.Experiment{
		Mode:   model.ModePlan,
		Pareto: &model.ParetoOptions{Points: 7},
		Solver: model.Solver{Options: map[string]any{"spores": map[string]any{"number": 2}}},
	}
	w := e.OptionWarnings()
	if len(w) != 2 {
		t.Fatalf("want 2 warnings (deprecated namespace + mismatched block), got %d: %v", len(w), w)
	}
	if !strings.Contains(w[0], "deprecated") || !strings.Contains(w[1], "mode is \"plan\"") {
		t.Fatalf("unexpected warnings: %v", w)
	}
}

func TestInlineValueJSON(t *testing.T) {
	var v model.Value
	if err := json.Unmarshal([]byte("[0.1, 0.4]"), &v); err != nil {
		t.Fatalf("unmarshal inline array: %v", err)
	}
	if !v.IsInline() || len(v.Inline) != 2 {
		t.Fatalf("inline slot not set: %+v", v)
	}
	b, err := json.Marshal(v)
	if err != nil || string(b) != "[0.1,0.4]" {
		t.Fatalf("roundtrip mismatch: %s, %v", b, err)
	}
	if err := json.Unmarshal([]byte("[]"), &v); err == nil {
		t.Fatal("empty inline array must be rejected")
	}
}

func TestInternInlineSeries(t *testing.T) {
	m := baseModel()
	m.Technologies["d"] = model.Technology{
		Role: model.RoleDemand, Node: model.StringList{"n1"}, CarrierIn: model.StringList{"el"},
		DemandProfile: &model.Value{Inline: []float64{1, 2, 3}},
	}
	m.Trade = map[string]model.Trade{"tr": {Node: "n1", Carrier: "el",
		Import: &model.TradeSide{Limit: &model.Value{Inline: []float64{5, 5, 0}}}}}

	if err := m.InternInlineSeries(); err != nil {
		t.Fatalf("intern: %v", err)
	}

	d := m.Technologies["d"]
	if !d.DemandProfile.IsSeries() {
		t.Fatalf("demand profile not interned: %+v", d.DemandProfile)
	}
	ts, ok := m.Timeseries[d.DemandProfile.SeriesID()]
	if !ok || len(ts.Values) != 3 {
		t.Fatalf("interned series missing: %v", m.Timeseries)
	}
	if lim := m.Trade["tr"].Import.Limit; !lim.IsSeries() {
		t.Fatalf("trade limit not interned: %+v", lim)
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("interned model invalid: %v", err)
	}

	// Idempotent: a second pass accepts its own output (the interned entries
	// are referenced by the rewritten Values) and adds nothing.
	before := len(m.Timeseries)
	if err := m.InternInlineSeries(); err != nil {
		t.Fatalf("second intern pass rejected its own output: %v", err)
	}
	if len(m.Timeseries) != before {
		t.Fatalf("interning is not idempotent: %d -> %d series", before, len(m.Timeseries))
	}
}

// --- second tranche: strictness + remaining extensions -----------------------

func TestStrictModelBasics(t *testing.T) {
	m := baseModel()
	m.Metadata.Name = ""
	wantValidateErr(t, &m, "metadata.name is required")

	m = baseModel()
	m.Time.Start = ""
	wantValidateErr(t, &m, "time.start is required")

	m = baseModel()
	m.Time.End = "yesterday"
	wantValidateErr(t, &m, "not an ISO 8601 timestamp")
}

func TestSecondTrancheRanges(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*model.Model)
		want string
	}{
		{"both fixed-om conventions", func(m *model.Model) {
			m.Technologies["g"] = model.Technology{Role: model.RoleSupply, Node: model.StringList{"n1"},
				CarrierOut: model.StringList{"el"},
				Costs:      map[string]model.CostClass{"monetary": {FixedOM: fp(5), FixedOMFraction: fp(0.02)}}}
		}, "mutually exclusive"},
		{"fraction needs overnight basis", func(m *model.Model) {
			m.Technologies["g"] = model.Technology{Role: model.RoleSupply, Node: model.StringList{"n1"},
				CarrierOut: model.StringList{"el"}, CostBasis: model.CostAnnualized,
				Costs: map[string]model.CostClass{"monetary": {FixedOMFraction: fp(0.02)}}}
		}, "needs the overnight cost basis"},
		{"units without per_unit", func(m *model.Model) {
			one := 1
			m.Technologies["g"] = model.Technology{Role: model.RoleSupply, Node: model.StringList{"n1"},
				CarrierOut: model.StringList{"el"}, Capacity: &model.Capacity{UnitsMax: &one}}
		}, "need capacity.per_unit"},
		{"bad decommission mode", func(m *model.Model) {
			m.Technologies["g"] = model.Technology{Role: model.RoleSupply, Node: model.StringList{"n1"},
				CarrierOut: model.StringList{"el"},
				Capacity:   &model.Capacity{Decommission: &model.Decommission{Mode: "someday"}}}
		}, "decommission.mode"},
		{"systemwide min above max", func(m *model.Model) {
			m.Technologies["g"] = model.Technology{Role: model.RoleSupply, Node: model.StringList{"n1"},
				CarrierOut: model.StringList{"el"},
				Capacity:   &model.Capacity{SystemwideMin: fp(10), SystemwideMax: fp(5)}}
		}, "systemwide_min"},
		{"standby power out of range", func(m *model.Model) {
			m.Technologies["g"] = model.Technology{Role: model.RoleSupply, Node: model.StringList{"n1"},
				CarrierOut: model.StringList{"el"}, Operation: &model.Operation{StandbyPower: fp(1.5)}}
		}, "standby_power"},
		{"annual output min above max", func(m *model.Model) {
			m.Technologies["g"] = model.Technology{Role: model.RoleSupply, Node: model.StringList{"n1"},
				CarrierOut: model.StringList{"el"}, AnnualOutputMin: fp(10), AnnualOutputMax: fp(5)}
		}, "annual_output_min"},
		{"demand_curtailable on supply", func(m *model.Model) {
			m.Technologies["g"] = model.Technology{Role: model.RoleSupply, Node: model.StringList{"n1"},
				CarrierOut: model.StringList{"el"}, DemandCurtailable: true}
		}, "demand_curtailable"},
		{"period without name", func(m *model.Model) {
			m.Periods = []model.Period{{Year: intp(2030)}}
		}, "has no name"},
		{"negative discount rate", func(m *model.Model) {
			m.DiscountRate = fp(-0.05)
		}, "discount_rate"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := baseModel()
			tc.mut(&m)
			wantValidateErr(t, &m, tc.want)
		})
	}

	e := model.Experiment{TimeAggregation: &model.TimeAggregation{Method: "cluster"}}
	if err := e.Validate(); err == nil || !strings.Contains(err.Error(), "cluster_series") {
		t.Fatalf("want cluster_series requirement, got: %v", err)
	}
	e = model.Experiment{EmissionAccounting: "gross"}
	if err := e.Validate(); err == nil || !strings.Contains(err.Error(), "emission_accounting") {
		t.Fatalf("want emission_accounting rejection, got: %v", err)
	}
}

func intp(v int) *int { return &v }

func TestPeriodJSON(t *testing.T) {
	var m model.Model
	payload := `{"metadata":{"name":"x"},"time":{"start":"2030-01-01","end":"2030-01-02"},
		"carriers":{"el":{}},"nodes":{"n":{}},"technologies":{},
		"periods":["p1",{"name":"p2","year":2035,"length_years":5}]}`
	if err := json.Unmarshal([]byte(payload), &m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(m.Periods) != 2 || m.Periods[0].Name != "p1" || m.Periods[1].Year == nil || *m.Periods[1].Year != 2035 {
		t.Fatalf("periods decoded wrong: %+v", m.Periods)
	}
	b, err := json.Marshal(m.Periods)
	if err != nil || string(b) != `["p1",{"name":"p2","year":2035,"length_years":5}]` {
		t.Fatalf("periods round-trip: %s, %v", b, err)
	}
}

func TestEffectiveCapacityBounds(t *testing.T) {
	four := 4
	c := &model.Capacity{PerUnit: fp(250), UnitsMax: &four}
	if max := c.EffectiveMax(); max == nil || *max != 1000 {
		t.Fatalf("EffectiveMax = %v, want 1000", max)
	}
	c.Max = fp(600) // explicit bound wins
	if max := c.EffectiveMax(); max == nil || *max != 600 {
		t.Fatalf("EffectiveMax = %v, want 600", max)
	}
}
