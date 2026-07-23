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

// --- third tranche: closing the remaining Validate branches ------------------

func TestStructuralReferenceChecks(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*model.Model)
		want string
	}{
		{"no nodes", func(m *model.Model) {
			m.Nodes = nil
		}, "model has no nodes"},
		{"no carriers", func(m *model.Model) {
			m.Carriers = nil
		}, "model has no carriers"},
		{"invalid role", func(m *model.Model) {
			m.Technologies["g"] = model.Technology{Role: "magic", Node: model.StringList{"n1"}}
		}, `role "magic" is invalid`},
		{"invalid cost basis", func(m *model.Model) {
			m.Technologies["g"] = model.Technology{Role: model.RoleSupply, Node: model.StringList{"n1"},
				CarrierOut: model.StringList{"el"}, CostBasis: "levelized"}
		}, `cost_basis "levelized" is invalid`},
		{"no node specified", func(m *model.Model) {
			m.Technologies["g"] = model.Technology{Role: model.RoleSupply, CarrierOut: model.StringList{"el"}}
		}, "no node specified"},
		{"undefined node", func(m *model.Model) {
			m.Technologies["g"] = model.Technology{Role: model.RoleSupply, Node: model.StringList{"ghost"},
				CarrierOut: model.StringList{"el"}}
		}, `node "ghost" not defined`},
		{"undefined carrier", func(m *model.Model) {
			m.Technologies["g"] = model.Technology{Role: model.RoleSupply, Node: model.StringList{"n1"},
				CarrierOut: model.StringList{"unobtainium"}}
		}, `carrier "unobtainium" not defined`},
		{"storage role without storage block", func(m *model.Model) {
			m.Technologies["s"] = model.Technology{Role: model.RoleStorage, Node: model.StringList{"n1"},
				CarrierOut: model.StringList{"el"}}
		}, "role is storage but no storage block"},
		{"demand role without profile", func(m *model.Model) {
			m.Technologies["d"] = model.Technology{Role: model.RoleDemand, Node: model.StringList{"n1"},
				CarrierIn: model.StringList{"el"}}
		}, "role is demand but no demand_profile"},
		{"unresolved efficiency series", func(m *model.Model) {
			m.Technologies["g"] = model.Technology{Role: model.RoleSupply, Node: model.StringList{"n1"},
				CarrierOut: model.StringList{"el"}, Efficiency: model.Series("missing_eff")}
		}, `time-series "missing_eff" not defined`},
		{"unresolved demand profile series", func(m *model.Model) {
			m.Technologies["d"] = model.Technology{Role: model.RoleDemand, Node: model.StringList{"n1"},
				CarrierIn: model.StringList{"el"}, DemandProfile: model.Series("missing_dp")}
		}, `time-series "missing_dp" not defined`},
		{"unresolved max_pu series", func(m *model.Model) {
			m.Technologies["g"] = model.Technology{Role: model.RoleSupply, Node: model.StringList{"n1"},
				CarrierOut: model.StringList{"el"}, Operation: &model.Operation{MaxPU: model.Series("missing_max")}}
		}, `time-series "missing_max" not defined`},
		{"unresolved min_pu series", func(m *model.Model) {
			m.Technologies["g"] = model.Technology{Role: model.RoleSupply, Node: model.StringList{"n1"},
				CarrierOut: model.StringList{"el"}, Operation: &model.Operation{MinPU: model.Series("missing_min")}}
		}, `time-series "missing_min" not defined`},
		{"unresolved inflow series", func(m *model.Model) {
			m.Technologies["s"] = model.Technology{Role: model.RoleStorage, Node: model.StringList{"n1"},
				CarrierOut: model.StringList{"el"}, Storage: &model.Storage{Inflow: model.Series("missing_inflow")}}
		}, `inflow series "missing_inflow" not defined`},
		{"overnight investment missing lifetime", func(m *model.Model) {
			m.Technologies["g"] = model.Technology{Role: model.RoleSupply, Node: model.StringList{"n1"},
				CarrierOut: model.StringList{"el"},
				Costs:      map[string]model.CostClass{"monetary": {InvestmentPerCapacity: fp(1000)}}}
		}, "needs lifetime and interest_rate"},
		{"overnight investment missing interest rate", func(m *model.Model) {
			m.Technologies["g"] = model.Technology{Role: model.RoleSupply, Node: model.StringList{"n1"},
				CarrierOut: model.StringList{"el"}, CostBasis: model.CostOvernight, Lifetime: fp(20),
				Costs: map[string]model.CostClass{"monetary": {InvestmentPerCapacity: fp(1000)}}}
		}, "needs lifetime and interest_rate"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := baseModel()
			tc.mut(&m)
			wantValidateErr(t, &m, tc.want)
		})
	}
}

func TestFlowStructureChecks(t *testing.T) {
	mk := func(flows []model.Flow) model.Model {
		m := baseModel()
		m.Technologies["chp"] = model.Technology{
			Role: model.RoleConversion, Node: model.StringList{"n1"}, Flows: flows,
		}
		return m
	}

	m := mk([]model.Flow{
		{Carrier: "unobtainium", Direction: model.FlowIn, Reference: true},
	})
	wantValidateErr(t, &m, `flow carrier "unobtainium" not defined`)

	m = mk([]model.Flow{
		{Carrier: "gas", Direction: "sideways", Ratio: 1},
	})
	wantValidateErr(t, &m, `must be "in" or "out"`)

	m = mk([]model.Flow{
		{Carrier: "el", Direction: model.FlowOut, Ratio: 0.4},
		{Carrier: "heat", Direction: model.FlowOut, Ratio: 0.5},
	})
	wantValidateErr(t, &m, "flows need at least one input")

	m = mk([]model.Flow{
		{Carrier: "gas", Direction: model.FlowIn, Reference: true},
		{Carrier: "heat", Direction: model.FlowIn, Reference: true},
		{Carrier: "el", Direction: model.FlowOut, Ratio: 0.4},
	})
	wantValidateErr(t, &m, "at most one flow may be the reference input")
}

func TestNodeOverrideStructure(t *testing.T) {
	mkTech := func(ov map[string]model.NodeOverride) model.Technology {
		return model.Technology{Role: model.RoleSupply, Node: model.StringList{"n1"},
			CarrierOut: model.StringList{"el"}, NodeOverrides: ov}
	}
	cases := []struct {
		name string
		mut  func(*model.Model)
		want string
	}{
		{"override node not in tech's list", func(m *model.Model) {
			m.Technologies["g"] = mkTech(map[string]model.NodeOverride{"n2": {}})
		}, `node_override "n2" is not in the tech's node list`},
		{"override node undefined", func(m *model.Model) {
			m.Technologies["g"] = mkTech(map[string]model.NodeOverride{"ghost": {}})
		}, `node_override "ghost" is not a defined node`},
		{"override performance invalid", func(m *model.Model) {
			m.Technologies["g"] = mkTech(map[string]model.NodeOverride{"n1": {
				Performance: &model.Performance{Type: model.PerfPiecewise}}})
		}, "at least 2 breakpoints"},
		{"override series undefined", func(m *model.Model) {
			m.Technologies["g"] = mkTech(map[string]model.NodeOverride{"n1": {
				Efficiency: model.Series("missing_ov")}})
		}, `override "n1": time-series "missing_ov" not defined`},
		{"override capacity range", func(m *model.Model) {
			m.Technologies["g"] = mkTech(map[string]model.NodeOverride{"n1": {
				Capacity: &model.Capacity{Min: fp(10), Max: fp(5)}}})
		}, `override "n1": capacity.min`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := baseModel()
			tc.mut(&m)
			wantValidateErr(t, &m, tc.want)
		})
	}
}

func TestNodeChecks(t *testing.T) {
	m := baseModel()
	m.Nodes["n1"] = model.Node{AvailableArea: fp(-10)}
	wantValidateErr(t, &m, "available_area must not be negative")

	m = baseModel()
	m.Nodes["n1"] = model.Node{Climate: map[string]model.Value{"ghi": *model.Series("missing_ghi")}}
	wantValidateErr(t, &m, `climate series "missing_ghi" (column ghi) not defined`)
}

func TestTransmissionChecks(t *testing.T) {
	mk := func(mut func(*model.Transmission)) model.Model {
		m := baseModel()
		l := model.Transmission{Carrier: "el", From: "n1", To: "n2"}
		mut(&l)
		m.Transmission = map[string]model.Transmission{"l": l}
		return m
	}
	cases := []struct {
		name string
		mut  func(*model.Transmission)
		want string
	}{
		{"efficiency above 1", func(l *model.Transmission) {
			l.Efficiency = model.Num(1.5)
		}, "efficiency must be within (0,1]"},
		{"efficiency zero", func(l *model.Transmission) {
			l.Efficiency = model.Num(0)
		}, "efficiency must be within (0,1]"},
		{"negative distance", func(l *model.Transmission) {
			l.Distance = fp(-5)
		}, "distance must not be negative"},
		{"min_flow out of range", func(l *model.Transmission) {
			l.MinFlow = fp(1.5)
		}, "min_flow must be within [0,1]"},
		{"energy_consumption undefined carrier", func(l *model.Transmission) {
			l.EnergyConsumption = &model.TransportEnergy{Carrier: "unobtainium"}
		}, `energy_consumption carrier "unobtainium" not defined`},
		{"energy_consumption negative rate", func(l *model.Transmission) {
			l.EnergyConsumption = &model.TransportEnergy{Carrier: "el", PerFlow: -1}
		}, "rates must not be negative"},
		{"energy_consumption per-distance needs distance", func(l *model.Transmission) {
			l.EnergyConsumption = &model.TransportEnergy{Carrier: "el", PerFlowDistance: 0.1}
		}, "per_flow_distance needs distance"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := mk(tc.mut)
			wantValidateErr(t, &m, tc.want)
		})
	}
}

func TestEmissionLimitAndTradeChecks(t *testing.T) {
	m := baseModel()
	m.EmissionLimits = []model.EmissionLimit{{Sense: "<"}}
	wantValidateErr(t, &m, `sense "<" must be one of <=, >=, ==`)

	m = baseModel()
	m.Trade = map[string]model.Trade{"tr": {Node: "ghost", Carrier: "el",
		Import: &model.TradeSide{Price: model.Num(50)}}}
	wantValidateErr(t, &m, `trade "tr": node "ghost" not defined`)

	m = baseModel()
	m.Trade = map[string]model.Trade{"tr": {Node: "n1", Carrier: "unobtainium",
		Import: &model.TradeSide{Price: model.Num(50)}}}
	wantValidateErr(t, &m, `trade "tr": carrier "unobtainium" not defined`)

	m = baseModel()
	m.Trade = map[string]model.Trade{"tr": {Node: "n1", Carrier: "el",
		Import: &model.TradeSide{Price: model.Series("missing_price")}}}
	wantValidateErr(t, &m, `import price series "missing_price" not defined`)
}

func TestPerformanceValidate(t *testing.T) {
	cases := []struct {
		name string
		perf model.Performance
		want string
	}{
		{"too few breakpoints", model.Performance{Type: model.PerfPiecewise,
			Breakpoints: []model.Breakpoint{{Load: 1, Efficiency: 0.5}}},
			"at least 2 breakpoints"},
		{"load above 1", model.Performance{Type: model.PerfPiecewise,
			Breakpoints: []model.Breakpoint{{Load: 0.5, Efficiency: 0.4}, {Load: 1.2, Efficiency: 0.5}}},
			"must be within [0,1]"},
		{"load below 0", model.Performance{Type: model.PerfPiecewise,
			Breakpoints: []model.Breakpoint{{Load: -0.1, Efficiency: 0.4}, {Load: 1, Efficiency: 0.5}}},
			"must be within [0,1]"},
		{"non-increasing breakpoints", model.Performance{Type: model.PerfPiecewise,
			Breakpoints: []model.Breakpoint{{Load: 0.5, Efficiency: 0.4}, {Load: 0.5, Efficiency: 0.5}}},
			"strictly increasing"},
		{"non-positive efficiency", model.Performance{Type: model.PerfPiecewise,
			Breakpoints: []model.Breakpoint{{Load: 0.2, Efficiency: 0}, {Load: 1, Efficiency: 0.5}}},
			"efficiency must be positive"},
		{"min_load out of range", model.Performance{Type: model.PerfPiecewise,
			Breakpoints: []model.Breakpoint{{Load: 0.2, Efficiency: 0.4}, {Load: 1, Efficiency: 0.5}},
			MinLoad:     fp(1.2)},
			"min_load"},
		{"unknown physics model", model.Performance{Type: model.PerfPhysics, Model: "fusion"},
			"unknown physics model"},
		{"unknown type", model.Performance{Type: "quantum"},
			"unknown performance type"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.perf.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want error containing %q, got: %v", tc.want, err)
			}
		})
	}

	ok := model.Performance{Type: model.PerfPiecewise, MinLoad: fp(0.3),
		Breakpoints: []model.Breakpoint{{Load: 0.3, Efficiency: 0.35}, {Load: 1, Efficiency: 0.5}}}
	if err := ok.Validate(); err != nil {
		t.Fatalf("valid piecewise curve rejected: %v", err)
	}
	var nilPerf *model.Performance
	if err := nilPerf.Validate(); err != nil {
		t.Fatalf("nil performance must validate: %v", err)
	}
}

// --- new hardening rules ------------------------------------------------------

func TestIdentifierGuard(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*model.Model)
		want string
	}{
		{"node id with path traversal", func(m *model.Model) {
			m.Nodes["../etc"] = model.Node{}
		}, `nodes: id "../etc" is invalid`},
		{"node id with backslash", func(m *model.Model) {
			m.Nodes[`a\b`] = model.Node{}
		}, `nodes: id "a\\b" is invalid`},
		{"node id with leading dot", func(m *model.Model) {
			m.Nodes[".hidden"] = model.Node{}
		}, `nodes: id ".hidden" is invalid`},
		{"tech id with slash", func(m *model.Model) {
			m.Technologies["a/b"] = model.Technology{Role: model.RoleSupply, Node: model.StringList{"n1"},
				CarrierOut: model.StringList{"el"}}
		}, `technologies: id "a/b" is invalid`},
		{"carrier id with space", func(m *model.Model) {
			m.Carriers["natural gas"] = model.Carrier{}
		}, `carriers: id "natural gas" is invalid`},
		{"transmission id empty", func(m *model.Model) {
			m.Transmission = map[string]model.Transmission{"": {Carrier: "el", From: "n1", To: "n2"}}
		}, `transmission: id "" is invalid`},
		{"trade id with dotdot", func(m *model.Model) {
			m.Trade = map[string]model.Trade{"..": {Node: "n1", Carrier: "el",
				Import: &model.TradeSide{Price: model.Num(50)}}}
		}, `trade: id ".." is invalid`},
		{"timeseries id with slash", func(m *model.Model) {
			m.Timeseries = map[string]model.TimeSeries{"a/b": {Source: "inline", Values: []float64{1}}}
		}, `timeseries: id "a/b" is invalid`},
		{"interned id with unsafe segment", func(m *model.Model) {
			m.Timeseries = map[string]model.TimeSeries{"_inline:tech:../evil:max_pu": {Source: "inline", Values: []float64{1}}}
		}, `timeseries: id "_inline:tech:../evil:max_pu" is invalid`},
		{"cost class with slash", func(m *model.Model) {
			m.Technologies["g"] = model.Technology{Role: model.RoleSupply, Node: model.StringList{"n1"},
				CarrierOut: model.StringList{"el"},
				Costs:      map[string]model.CostClass{"mon/etary": {FixedOM: fp(5)}}}
		}, `cost class "mon/etary" is invalid`},
		{"climate column with slash", func(m *model.Model) {
			m.Nodes["n1"] = model.Node{Climate: map[string]model.Value{"gh/i": *model.Num(1)}}
		}, `climate column "gh/i" is invalid`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := baseModel()
			tc.mut(&m)
			wantValidateErr(t, &m, tc.want)
		})
	}

	// The permitted charset (including dots, underscores, hyphens, and the
	// interner's colon-separated form) stays accepted.
	m := baseModel()
	m.Nodes["DE.north_01-a"] = model.Node{}
	m.Timeseries = map[string]model.TimeSeries{
		"pv_cf.2030-v1":         {Source: "inline", Values: []float64{1}},
		"_inline:tech:g:max_pu": {Source: "inline", Values: []float64{1}},
		"_inline:time:weights":  {Source: "inline", Values: []float64{1}},
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("valid identifiers rejected: %v", err)
	}
}

func TestDuplicateConstraintNames(t *testing.T) {
	term := []model.Term{{Coefficient: 1, Variable: model.VarCapacity}}

	m := baseModel()
	m.Constraints = []model.Constraint{
		{Name: "cap", Terms: term, Sense: "<="},
		{Name: "cap", Terms: term, Sense: "<="},
	}
	wantValidateErr(t, &m, `constraints[1]: name "cap" is already used by constraints[0]`)

	m = baseModel()
	m.EmissionLimits = []model.EmissionLimit{
		{Name: "co2_cap", Sense: "<="},
		{Name: "co2_cap", Sense: "<="},
	}
	wantValidateErr(t, &m, `emission_limits[1]: name "co2_cap" is already used by emission_limits[0]`)

	// Cross-list collision: Calliope renders both lists into one YAML map.
	m = baseModel()
	m.Constraints = []model.Constraint{{Name: "cap", Terms: term, Sense: "<="}}
	m.EmissionLimits = []model.EmissionLimit{{Name: "cap", Sense: "<="}}
	wantValidateErr(t, &m, `emission_limits[0]: name "cap" is already used by constraints[0]`)

	// Unnamed entries never collide.
	m = baseModel()
	m.Constraints = []model.Constraint{
		{Terms: term, Sense: "<="},
		{Terms: term, Sense: "<="},
	}
	m.EmissionLimits = []model.EmissionLimit{{Sense: "<="}, {Sense: "<="}}
	if err := m.Validate(); err != nil {
		t.Fatalf("unnamed constraints/limits rejected: %v", err)
	}
}

func TestTimeConfigRules(t *testing.T) {
	m := baseModel()
	m.Time.Start, m.Time.End = "2030-01-02", "2030-01-01"
	wantValidateErr(t, &m, "must be after time.start")

	// Equal instants across layouts are caught too (date-only means midnight).
	m = baseModel()
	m.Time.Start, m.Time.End = "2030-01-01", "2030-01-01T00:00:00"
	wantValidateErr(t, &m, "must be after time.start")

	m = baseModel()
	m.Time.Timesteps = []string{"2030-01-01 00:00", "2030-01-01 01:00", "2030-01-01 00:00"}
	wantValidateErr(t, &m, `time.timesteps[2] duplicates timesteps[0]`)

	m = baseModel()
	m.Time.Timesteps = []string{"2030-01-01 00:00", "2030-01-01 01:00"}
	if err := m.Validate(); err != nil {
		t.Fatalf("distinct timesteps rejected: %v", err)
	}
}

func TestSweepRunCap(t *testing.T) {
	vals := func(n int) []float64 {
		out := make([]float64, n)
		for i := range out {
			out[i] = float64(i)
		}
		return out
	}
	axes := func(counts ...int) []model.SweepAxis {
		out := make([]model.SweepAxis, len(counts))
		for i, n := range counts {
			out[i] = model.SweepAxis{Parameter: string(rune('a' + i)), Values: vals(n)}
		}
		return out
	}

	e := model.Experiment{Sweep: axes(11, 10, 10)} // 1100 runs
	if err := e.Validate(); err == nil || !strings.Contains(err.Error(), "expands to 1100 runs; the limit is 1000") {
		t.Fatalf("want sweep cap error, got: %v", err)
	}

	e = model.Experiment{Sweep: axes(10, 10, 10)} // exactly the limit
	if err := e.Validate(); err != nil {
		t.Fatalf("sweep at the limit rejected: %v", err)
	}

	// The product must not overflow before the cap fires (100^10 > MaxInt).
	e = model.Experiment{Sweep: axes(100, 100, 100, 100, 100, 100, 100, 100, 100, 100)}
	if err := e.Validate(); err == nil || !strings.Contains(err.Error(), "more runs than can be counted") {
		t.Fatalf("want overflow-safe sweep cap error, got: %v", err)
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
