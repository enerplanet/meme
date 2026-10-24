// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package model_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/enerplanet/meme/internal/model"
)

func ip(v int) *int { return &v }

// electricalModel is a two-voltage-level network: an HV node feeding two MV
// nodes through a transformer and a line, with a slack generator, a PV
// generator with a fixed dispatch and two loads.
func electricalModel() model.Model {
	m := baseModel()
	m.Nodes = map[string]model.Node{
		"hv":  {Electrical: &model.NodeElectrical{VNom: 110}},
		"mv1": {Electrical: &model.NodeElectrical{VNom: 20}},
		"mv2": {Electrical: &model.NodeElectrical{VNom: 20}},
	}
	m.Timeseries = map[string]model.TimeSeries{
		"pv_out": {Source: "inline", Values: []float64{0, 2, 6, 8}},
		"dem":    {Source: "inline", Values: []float64{10, 12, 14, 11}},
	}
	m.Technologies = map[string]model.Technology{
		"grid": {Role: model.RoleSupply, Node: model.StringList{"hv"}, CarrierOut: model.StringList{"el"},
			Capacity: &model.Capacity{Existing: 100}},
		"pv": {Role: model.RoleSupply, Node: model.StringList{"mv2"}, CarrierOut: model.StringList{"el"},
			Capacity: &model.Capacity{Existing: 10}, Dispatch: model.Series("pv_out")},
		"load": {Role: model.RoleDemand, Node: model.StringList{"mv1"}, CarrierIn: model.StringList{"el"},
			DemandProfile: model.Series("dem")},
	}
	m.Transmission = map[string]model.Transmission{
		"trafo": {Carrier: "el", From: "hv", To: "mv1", Bidirectional: true, Capacity: &model.Capacity{Existing: 40},
			Electrical: &model.TransmissionElectrical{Kind: model.ElectricalTransformer, R: 0.01, X: 0.1, SNom: fp(40)}},
		"l12": {Carrier: "el", From: "mv1", To: "mv2", Bidirectional: true, Capacity: &model.Capacity{Existing: 20},
			Electrical: &model.TransmissionElectrical{Kind: model.ElectricalLine, R: 1.2, X: 3.5, SNom: fp(20)}},
	}
	return m
}

// The electrical fields round-trip through JSON under their documented keys.
func TestElectricalFieldsJSON(t *testing.T) {
	raw := `{
	  "model": {
	    "metadata": {"name": "pf"},
	    "time": {"start": "2025-01-01", "end": "2025-01-02", "resolution": "1H"},
	    "carriers": {"el": {}},
	    "nodes": {"a": {"electrical": {"v_nom": 110}}, "b": {"electrical": {"v_nom": 20}}},
	    "technologies": {
	      "g": {"role": "supply", "node": ["a", "b"], "carrier_out": "el", "capacity": {"existing": 5},
	            "dispatch": 3, "node_overrides": {"b": {"dispatch": [1, 2, 3]}}}
	    },
	    "transmission": {
	      "t": {"carrier": "el", "from": "a", "to": "b", "bidirectional": true,
	            "electrical": {"kind": "transformer", "r": 0.01, "x": 0.1, "s_nom": 40, "num_parallel": 2, "tap_ratio": 1.02}}
	    }
	  },
	  "experiment": {"mode": "power_flow",
	                 "power_flow": {"method": "linear", "slack": "g@a", "distribute_slack": true, "tolerance": 1e-5}}
	}`
	var j model.Job
	if err := json.Unmarshal([]byte(raw), &j); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if j.Model.Nodes["a"].Electrical == nil || j.Model.Nodes["a"].Electrical.VNom != 110 {
		t.Errorf("node electrical not decoded: %+v", j.Model.Nodes["a"].Electrical)
	}
	e := j.Model.Transmission["t"].Electrical
	if e == nil || e.Kind != model.ElectricalTransformer || e.R != 0.01 || e.X != 0.1 || *e.SNom != 40 || *e.NumParallel != 2 || *e.TapRatio != 1.02 {
		t.Errorf("transmission electrical not decoded: %+v", e)
	}
	g := j.Model.Technologies["g"]
	if f, ok := g.Dispatch.Reduce(); !ok || f != 3 {
		t.Errorf("scalar dispatch not decoded: %+v", g.Dispatch)
	}
	if !g.NodeOverrides["b"].Dispatch.IsInline() {
		t.Errorf("override dispatch array not decoded: %+v", g.NodeOverrides["b"].Dispatch)
	}
	if j.Experiment.Mode != model.ModePowerFlow {
		t.Errorf("mode: got %q", j.Experiment.Mode)
	}
	pf := j.Experiment.PowerFlow
	if pf == nil || pf.Method != model.PowerFlowLinear || pf.Slack != "g@a" || !pf.DistributeSlack || *pf.Tolerance != 1e-5 {
		t.Errorf("power_flow options not decoded: %+v", pf)
	}

	out, err := json.Marshal(j)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"electrical":{"v_nom":110}`, `"kind":"transformer"`, `"tap_ratio":1.02`, `"dispatch":3`, `"mode":"power_flow"`, `"distribute_slack":true`} {
		if !strings.Contains(string(out), key) {
			t.Errorf("encoded job lacks %s:\n%s", key, out)
		}
	}
}

// Dispatch is a Value like the other per-timestep inputs: inline arrays are
// interned (base field and per-node override), and the override wins in At.
func TestDispatchInternAndOverride(t *testing.T) {
	m := electricalModel()
	pv := m.Technologies["pv"]
	pv.Node = model.StringList{"mv1", "mv2"}
	pv.Dispatch = &model.Value{Inline: []float64{1, 2, 3, 4}}
	pv.NodeOverrides = map[string]model.NodeOverride{"mv2": {Dispatch: &model.Value{Inline: []float64{4, 3, 2, 1}}}}
	m.Technologies["pv"] = pv
	if err := m.InternInlineSeries(); err != nil {
		t.Fatal(err)
	}
	pv = m.Technologies["pv"]
	if pv.Dispatch.SeriesID() != "_inline:tech:pv:dispatch" {
		t.Errorf("base dispatch not interned: %+v", pv.Dispatch)
	}
	if pv.NodeOverrides["mv2"].Dispatch.SeriesID() != "_inline:tech:pv:mv2:dispatch" {
		t.Errorf("override dispatch not interned: %+v", pv.NodeOverrides["mv2"].Dispatch)
	}
	if got := pv.At("mv2").Dispatch.SeriesID(); got != "_inline:tech:pv:mv2:dispatch" {
		t.Errorf("At(mv2) should carry the override dispatch, got %q", got)
	}
	if got := pv.At("mv1").Dispatch.SeriesID(); got != "_inline:tech:pv:dispatch" {
		t.Errorf("At(mv1) should keep the base dispatch, got %q", got)
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("interned model should validate: %v", err)
	}
}

func TestNodeElectricalChecks(t *testing.T) {
	m := electricalModel()
	m.Nodes["hv"] = model.Node{Electrical: &model.NodeElectrical{VNom: 0}}
	wantValidateErr(t, &m, `node "hv": electrical.v_nom must be positive`)
}

func TestTransmissionElectricalChecks(t *testing.T) {
	cases := []struct {
		name string
		mut  func(m *model.Model)
		want string
	}{
		{"unknown kind", func(m *model.Model) {
			l := m.Transmission["l12"]
			l.Electrical.Kind = "cable"
			m.Transmission["l12"] = l
		},
			`transmission "l12": electrical.kind "cable" is invalid (line or transformer)`},
		{"zero reactance", func(m *model.Model) { l := m.Transmission["l12"]; l.Electrical.X = 0; m.Transmission["l12"] = l },
			`transmission "l12": electrical.x must be positive`},
		{"negative resistance", func(m *model.Model) { l := m.Transmission["l12"]; l.Electrical.R = -1; m.Transmission["l12"] = l },
			`transmission "l12": electrical.r must not be negative`},
		{"non-positive rating", func(m *model.Model) { l := m.Transmission["l12"]; l.Electrical.SNom = fp(0); m.Transmission["l12"] = l },
			`transmission "l12": electrical.s_nom must be positive`},
		{"zero parallel circuits", func(m *model.Model) {
			l := m.Transmission["l12"]
			l.Electrical.NumParallel = ip(0)
			m.Transmission["l12"] = l
		},
			`transmission "l12": electrical.num_parallel must be at least 1`},
		{"tap ratio on a line", func(m *model.Model) {
			l := m.Transmission["l12"]
			l.Electrical.TapRatio = fp(1.02)
			m.Transmission["l12"] = l
		},
			`transmission "l12": electrical.tap_ratio applies to a transformer only`},
		{"non-positive tap ratio", func(m *model.Model) {
			l := m.Transmission["trafo"]
			l.Electrical.TapRatio = fp(0)
			m.Transmission["trafo"] = l
		},
			`transmission "trafo": electrical.tap_ratio must be positive`},
		{"one-way electrical arc", func(m *model.Model) { l := m.Transmission["l12"]; l.Bidirectional = false; m.Transmission["l12"] = l },
			`transmission "l12": an electrical arc carries power both ways; set bidirectional: true`},
		{"line across voltage levels", func(m *model.Model) {
			l := m.Transmission["trafo"]
			l.Electrical.Kind = model.ElectricalLine
			m.Transmission["trafo"] = l
		},
			`transmission "trafo": a line connects nodes of equal nominal voltage (hv 110 kV, mv1 20 kV); use kind "transformer"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := electricalModel()
			c.mut(&m)
			wantValidateErr(t, &m, c.want)
		})
	}

	// The reference network itself is valid, and a line between nodes without
	// a voltage level is not a voltage mismatch (nothing to compare).
	m := electricalModel()
	if err := m.Validate(); err != nil {
		t.Fatalf("reference electrical model should validate: %v", err)
	}
	m.Nodes["mv1"] = model.Node{}
	if err := m.Validate(); err != nil {
		t.Fatalf("line with one unleveled node should validate structurally: %v", err)
	}
}

func TestDispatchChecks(t *testing.T) {
	m := electricalModel()
	load := m.Technologies["load"]
	load.Dispatch = model.Num(5)
	m.Technologies["load"] = load
	wantValidateErr(t, &m, `tech "load": dispatch applies to supply, conversion and storage; demand uses demand_profile`)

	m = electricalModel()
	pv := m.Technologies["pv"]
	pv.Dispatch = model.Series("missing")
	m.Technologies["pv"] = pv
	wantValidateErr(t, &m, `tech "pv": time-series "missing" not defined`)

	m = electricalModel()
	pv = m.Technologies["pv"]
	pv.NodeOverrides = map[string]model.NodeOverride{"mv2": {Dispatch: model.Series("missing")}}
	m.Technologies["pv"] = pv
	wantValidateErr(t, &m, `tech "pv" override "mv2": time-series "missing" not defined`)
}

func TestExperimentPowerFlowValidate(t *testing.T) {
	e := model.Experiment{Mode: model.ModePowerFlow, PowerFlow: &model.PowerFlowOptions{Slack: "grid"}}
	if err := e.Validate(); err != nil {
		t.Fatalf("power_flow with defaults should validate: %v", err)
	}
	if !model.ModePowerFlow.Valid() {
		t.Error("power_flow must be a valid mode")
	}
	e.PowerFlow.Method = "gauss"
	if err := e.Validate(); err == nil || !strings.Contains(err.Error(), `experiment.power_flow.method "gauss" is invalid (newton_raphson or linear)`) {
		t.Errorf("bad method: %v", err)
	}
	e.PowerFlow.Method = model.PowerFlowLinear
	e.PowerFlow.Tolerance = fp(0)
	if err := e.Validate(); err == nil || !strings.Contains(err.Error(), "experiment.power_flow.tolerance must be positive") {
		t.Errorf("bad tolerance: %v", err)
	}

	// The typed block only takes effect in its mode; elsewhere it is a mistake
	// worth a warning, like the other mode blocks.
	e = model.Experiment{Mode: model.ModePlan, PowerFlow: &model.PowerFlowOptions{Slack: "grid"}}
	found := false
	for _, w := range e.OptionWarnings() {
		if strings.Contains(w, `experiment.power_flow is set but mode is "plan"`) {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a mode-mismatch warning for power_flow, got %v", e.OptionWarnings())
	}
}

// reactiveModel extends the electrical network with every reactive-power and
// voltage-control field: a slack set-point, a PV generator, a derived-Q load,
// a set-point load, a shunt, a controlled link to an island, and the branch
// refinements.
func reactiveModel() model.Model {
	m := electricalModel()
	m.Nodes["mv1"] = model.Node{Electrical: &model.NodeElectrical{VNom: 20, Shunt: &model.Shunt{G: 0, B: 0.001}}}
	m.Nodes["mv3"] = model.Node{Electrical: &model.NodeElectrical{VNom: 20}}
	m.Timeseries["q2"] = model.TimeSeries{Source: "inline", Values: []float64{2, 2.3, 2.6, 2}}
	grid := m.Technologies["grid"]
	grid.Electrical = &model.TechElectrical{VoltageSetpoint: fp(1.02)}
	m.Technologies["grid"] = grid
	pv := m.Technologies["pv"]
	pv.Electrical = &model.TechElectrical{Control: model.ControlPV, VoltageSetpoint: fp(1.0)}
	m.Technologies["pv"] = pv
	load := m.Technologies["load"]
	load.Electrical = &model.TechElectrical{PowerFactor: fp(0.95)}
	m.Technologies["load"] = load
	m.Technologies["load2"] = model.Technology{Role: model.RoleDemand, Node: model.StringList{"mv2"}, CarrierIn: model.StringList{"el"},
		DemandProfile: model.Series("dem"), Electrical: &model.TechElectrical{ReactiveDispatch: model.Series("q2")}}
	m.Technologies["dg"] = model.Technology{Role: model.RoleSupply, Node: model.StringList{"mv3"}, CarrierOut: model.StringList{"el"},
		Capacity: &model.Capacity{Existing: 5}, Dispatch: model.Num(1), Electrical: &model.TechElectrical{PowerFactor: fp(0.9)}}
	l12 := m.Transmission["l12"]
	l12.Distance = fp(10)
	l12.Electrical = &model.TransmissionElectrical{Kind: model.ElectricalLine, RPerLength: fp(0.12), XPerLength: fp(0.35), SNom: fp(20), B: fp(0.0001)}
	m.Transmission["l12"] = l12
	trafo := m.Transmission["trafo"]
	trafo.Electrical.PhaseShift = fp(2.5)
	m.Transmission["trafo"] = trafo
	m.Transmission["hvdc"] = model.Transmission{Carrier: "el", From: "mv2", To: "mv3", Bidirectional: true,
		Capacity: &model.Capacity{Existing: 30}, Dispatch: model.Num(3)}
	return m
}

func TestReactiveFieldsJSON(t *testing.T) {
	raw := `{"role":"supply","node":"a","carrier_out":"el","capacity":{"existing":5},"dispatch":2,
	         "electrical":{"reactive_dispatch":[0.5,0.6],"control":"pv","voltage_setpoint":1.01},
	         "node_overrides":{"a":{"electrical":{"power_factor":0.9,"leading":true}}}}`
	var tech model.Technology
	if err := json.Unmarshal([]byte(raw), &tech); err != nil {
		t.Fatal(err)
	}
	e := tech.Electrical
	if e == nil || !e.ReactiveDispatch.IsInline() || e.Control != model.ControlPV || *e.VoltageSetpoint != 1.01 {
		t.Errorf("technology electrical not decoded: %+v", e)
	}
	if ov := tech.NodeOverrides["a"].Electrical; ov == nil || *ov.PowerFactor != 0.9 || !ov.Leading {
		t.Errorf("override electrical not decoded: %+v", ov)
	}
	var n model.Node
	if err := json.Unmarshal([]byte(`{"electrical":{"v_nom":20,"shunt":{"g":0,"b":0.001}}}`), &n); err != nil {
		t.Fatal(err)
	}
	if n.Electrical.Shunt == nil || n.Electrical.Shunt.B != 0.001 {
		t.Errorf("shunt not decoded: %+v", n.Electrical)
	}
	var l model.Transmission
	if err := json.Unmarshal([]byte(`{"carrier":"el","from":"a","to":"b","bidirectional":true,"dispatch":"flow",
	    "electrical":{"kind":"line","r_per_length":0.12,"x_per_length":0.35,"b":0.0001,"g":0,"phase_shift":2.5}}`), &l); err != nil {
		t.Fatal(err)
	}
	if l.Dispatch.SeriesID() != "flow" || *l.Electrical.RPerLength != 0.12 || *l.Electrical.B != 0.0001 || *l.Electrical.PhaseShift != 2.5 {
		t.Errorf("transmission fields not decoded: %+v %+v", l.Dispatch, l.Electrical)
	}
	out, _ := json.Marshal(tech)
	for _, key := range []string{`"control":"pv"`, `"voltage_setpoint":1.01`, `"power_factor":0.9`, `"leading":true`} {
		if !strings.Contains(string(out), key) {
			t.Errorf("encoded technology lacks %s: %s", key, out)
		}
	}
}

// The reference reactive network validates; the derived impedance and the
// derived reactive power resolve as documented.
func TestReactiveModelValidatesAndDerives(t *testing.T) {
	m := reactiveModel()
	if err := m.Validate(); err != nil {
		t.Fatalf("reactive model should validate: %v", err)
	}
	r, x := m.Transmission["l12"].Electrical.Impedance(m.Transmission["l12"].Distance)
	if r != 1.2 || x != 3.5 {
		t.Errorf("per-length impedance over 10 km: got r %g x %g, want 1.2 3.5", r, x)
	}
	r, x = m.Transmission["trafo"].Electrical.Impedance(nil)
	if r != 0.01 || x != 0.1 {
		t.Errorf("literal impedance must pass through: got r %g x %g", r, x)
	}
	// Q = P tan(acos pf): a 10 MW load at 0.95 draws 3.2868 MVAr; leading flips the sign.
	f := (&model.TechElectrical{PowerFactor: fp(0.95)}).ReactiveFactor()
	if f < 0.3286 || f > 0.3288 {
		t.Errorf("reactive factor at pf 0.95: got %g, want 0.32868", f)
	}
	if g := (&model.TechElectrical{PowerFactor: fp(0.95), Leading: true}).ReactiveFactor(); g >= 0 || g < -0.3288 {
		t.Errorf("leading power factor must give a negative factor, got %g", g)
	}
	if g := (&model.TechElectrical{}).ReactiveFactor(); g != 0 {
		t.Errorf("no power factor means no derived Q, got %g", g)
	}
}

func TestReactiveFieldChecks(t *testing.T) {
	cases := []struct {
		name string
		mut  func(m *model.Model)
		want string
	}{
		{"power factor above one", func(m *model.Model) {
			t := m.Technologies["load"]
			t.Electrical.PowerFactor = fp(1.2)
			m.Technologies["load"] = t
		},
			`tech "load": electrical.power_factor must be within (0,1]`},
		{"power factor zero", func(m *model.Model) {
			t := m.Technologies["load"]
			t.Electrical.PowerFactor = fp(0)
			m.Technologies["load"] = t
		},
			`tech "load": electrical.power_factor must be within (0,1]`},
		{"both ways to give Q", func(m *model.Model) {
			t := m.Technologies["load"]
			t.Electrical.ReactiveDispatch = model.Num(1)
			m.Technologies["load"] = t
		},
			`tech "load": electrical.reactive_dispatch and electrical.power_factor are mutually exclusive`},
		{"leading without power factor", func(m *model.Model) {
			t := m.Technologies["load2"]
			t.Electrical.Leading = true
			m.Technologies["load2"] = t
		},
			`tech "load2": electrical.leading needs electrical.power_factor`},
		{"unknown control", func(m *model.Model) {
			t := m.Technologies["pv"]
			t.Electrical.Control = "pq2"
			m.Technologies["pv"] = t
		},
			`tech "pv": electrical.control "pq2" is invalid (pq or pv)`},
		{"control on a load", func(m *model.Model) {
			t := m.Technologies["load"]
			t.Electrical.Control = model.ControlPV
			m.Technologies["load"] = t
		},
			`tech "load": electrical.control applies to supply technologies`},
		{"voltage set-point on a load", func(m *model.Model) {
			t := m.Technologies["load"]
			t.Electrical.VoltageSetpoint = fp(1)
			m.Technologies["load"] = t
		},
			`tech "load": electrical.voltage_setpoint applies to supply technologies`},
		{"non-positive voltage set-point", func(m *model.Model) {
			t := m.Technologies["pv"]
			t.Electrical.VoltageSetpoint = fp(0)
			m.Technologies["pv"] = t
		},
			`tech "pv": electrical.voltage_setpoint must be positive`},
		{"reactive fields on a conversion", func(m *model.Model) {
			m.Technologies["boiler"] = model.Technology{Role: model.RoleConversion, Node: model.StringList{"mv1"}, CarrierIn: model.StringList{"el"},
				CarrierOut: model.StringList{"heat"}, Capacity: &model.Capacity{Existing: 2}, Dispatch: model.Num(1),
				Electrical: &model.TechElectrical{PowerFactor: fp(0.9)}}
		}, `tech "boiler": electrical reactive fields apply to supply, demand and storage; a conversion transfers active power only`},
		{"undefined reactive series", func(m *model.Model) {
			t := m.Technologies["load2"]
			t.Electrical.ReactiveDispatch = model.Series("missing")
			m.Technologies["load2"] = t
		},
			`tech "load2": time-series "missing" not defined`},
		{"negative shunt conductance", func(m *model.Model) {
			m.Nodes["mv1"] = model.Node{Electrical: &model.NodeElectrical{VNom: 20, Shunt: &model.Shunt{G: -1}}}
		},
			`node "mv1": electrical.shunt.g must not be negative`},
		{"dispatch and impedance on one arc", func(m *model.Model) { l := m.Transmission["l12"]; l.Dispatch = model.Num(1); m.Transmission["l12"] = l },
			`transmission "l12": dispatch and electrical are mutually exclusive (a line's flow is a result of the power flow)`},
		{"undefined arc dispatch series", func(m *model.Model) {
			l := m.Transmission["hvdc"]
			l.Dispatch = model.Series("missing")
			m.Transmission["hvdc"] = l
		},
			`transmission "hvdc": time-series "missing" not defined`},
		{"line admittance on a transformer", func(m *model.Model) {
			l := m.Transmission["trafo"]
			l.Electrical.B = fp(0.1)
			m.Transmission["trafo"] = l
		},
			`transmission "trafo": electrical.b and electrical.g apply to a line only`},
		{"phase shift on a line", func(m *model.Model) {
			l := m.Transmission["l12"]
			l.Electrical.PhaseShift = fp(1)
			m.Transmission["l12"] = l
		},
			`transmission "l12": electrical.phase_shift applies to a transformer only`},
		{"per-length without distance", func(m *model.Model) { l := m.Transmission["l12"]; l.Distance = nil; m.Transmission["l12"] = l },
			`transmission "l12": electrical.r_per_length and electrical.x_per_length need distance`},
		{"per-length and literal together", func(m *model.Model) { l := m.Transmission["l12"]; l.Electrical.X = 3; m.Transmission["l12"] = l },
			`transmission "l12": give either electrical.r and x or electrical.r_per_length and x_per_length, not both`},
		{"non-positive per-length reactance", func(m *model.Model) {
			l := m.Transmission["l12"]
			l.Electrical.XPerLength = fp(0)
			m.Transmission["l12"] = l
		},
			`transmission "l12": electrical.x_per_length must be positive`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := reactiveModel()
			c.mut(&m)
			wantValidateErr(t, &m, c.want)
		})
	}
}

// Reactive series and arc dispatch intern like every other inline array, and
// the override electrical block replaces the base block in At.
func TestReactiveInternAndOverride(t *testing.T) {
	m := reactiveModel()
	load := m.Technologies["load2"]
	load.Electrical.ReactiveDispatch = &model.Value{Inline: []float64{1, 2, 3, 4}}
	m.Technologies["load2"] = load
	pv := m.Technologies["pv"]
	pv.NodeOverrides = map[string]model.NodeOverride{"mv2": {Electrical: &model.TechElectrical{Control: model.ControlPQ, ReactiveDispatch: &model.Value{Inline: []float64{0, 0, 1, 1}}}}}
	m.Technologies["pv"] = pv
	hvdc := m.Transmission["hvdc"]
	hvdc.Dispatch = &model.Value{Inline: []float64{3, 3, 2, 2}}
	m.Transmission["hvdc"] = hvdc
	if err := m.InternInlineSeries(); err != nil {
		t.Fatal(err)
	}
	if got := m.Technologies["load2"].Electrical.ReactiveDispatch.SeriesID(); got != "_inline:tech:load2:reactive_dispatch" {
		t.Errorf("reactive series not interned: %q", got)
	}
	if got := m.Technologies["pv"].NodeOverrides["mv2"].Electrical.ReactiveDispatch.SeriesID(); got != "_inline:tech:pv:mv2:reactive_dispatch" {
		t.Errorf("override reactive series not interned: %q", got)
	}
	if got := m.Transmission["hvdc"].Dispatch.SeriesID(); got != "_inline:transmission:hvdc:dispatch" {
		t.Errorf("arc dispatch not interned: %q", got)
	}
	if at := m.Technologies["pv"].At("mv2"); at.Electrical.Control != model.ControlPQ || at.Electrical.VoltageSetpoint != nil {
		t.Errorf("At must take the override electrical block wholesale, got %+v", at.Electrical)
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("interned reactive model should validate: %v", err)
	}
}

// --- ADR-0010: the network inside the optimization -------------------------

func lopfModel() model.Model {
	m := baseModel()
	m.Nodes = map[string]model.Node{
		"a": {Electrical: &model.NodeElectrical{VNom: 110}},
		"b": {Electrical: &model.NodeElectrical{VNom: 110}},
	}
	m.Technologies = map[string]model.Technology{
		"gen":  {Role: model.RoleSupply, Node: model.StringList{"a"}, CarrierOut: model.StringList{"el"}, Capacity: &model.Capacity{Existing: 100}},
		"load": {Role: model.RoleDemand, Node: model.StringList{"b"}, CarrierIn: model.StringList{"el"}, DemandProfile: model.Num(50)},
	}
	m.Transmission = map[string]model.Transmission{
		"ab": {Carrier: "el", From: "a", To: "b", Bidirectional: true, Capacity: &model.Capacity{Existing: 30, Expandable: true, Max: fp(100)},
			Costs: map[string]model.CostClass{"monetary": {InvestmentPerCapacity: fp(20000)}}, Lifetime: fp(40), InterestRate: fp(0.05),
			Electrical: &model.TransmissionElectrical{Kind: model.ElectricalLine, R: 1, X: 10, MaxLoading: fp(0.9)}},
	}
	return m
}

// Transmission economics and the loading limit round-trip and validate.
func TestTransmissionEconomicsJSON(t *testing.T) {
	var l model.Transmission
	raw := `{"carrier":"el","from":"a","to":"b","bidirectional":true,"lifetime":40,"interest_rate":0.05,"cost_basis":"overnight",
	         "electrical":{"kind":"line","r":1,"x":10,"max_loading":0.9}}`
	if err := json.Unmarshal([]byte(raw), &l); err != nil {
		t.Fatal(err)
	}
	if *l.Lifetime != 40 || *l.InterestRate != 0.05 || l.CostBasis != model.CostOvernight || *l.Electrical.MaxLoading != 0.9 {
		t.Errorf("transmission economics not decoded: %+v %+v", l, l.Electrical)
	}
	m := lopfModel()
	if err := m.Validate(); err != nil {
		t.Fatalf("lopf model should validate: %v", err)
	}
	cases := []struct {
		name string
		mut  func(m *model.Model)
		want string
	}{
		{"lifetime", func(m *model.Model) { l := m.Transmission["ab"]; l.Lifetime = fp(0); m.Transmission["ab"] = l }, `transmission "ab": lifetime must be positive`},
		{"interest", func(m *model.Model) { l := m.Transmission["ab"]; l.InterestRate = fp(-1); m.Transmission["ab"] = l }, `transmission "ab": interest_rate must not be negative`},
		{"cost basis", func(m *model.Model) { l := m.Transmission["ab"]; l.CostBasis = "weird"; m.Transmission["ab"] = l }, `transmission "ab": cost_basis "weird" is invalid`},
		{"loading above one", func(m *model.Model) {
			l := m.Transmission["ab"]
			l.Electrical.MaxLoading = fp(1.5)
			m.Transmission["ab"] = l
		}, `transmission "ab": electrical.max_loading must be within (0,1]`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := lopfModel()
			c.mut(&m)
			wantValidateErr(t, &m, c.want)
		})
	}
}

// experiment.network: the model enum and the security block's shape.
func TestExperimentNetworkValidate(t *testing.T) {
	e := model.Experiment{Network: &model.NetworkOptions{Model: model.NetworkLinearPowerFlow}}
	if err := e.Validate(); err != nil {
		t.Fatalf("linear_power_flow should validate: %v", err)
	}
	if (&model.Experiment{}).EffectiveNetworkModel() != model.NetworkTransport {
		t.Error("the default network model is transport")
	}
	e.Network.Model = "mesh"
	if err := e.Validate(); err == nil || !strings.Contains(err.Error(), `experiment.network.model "mesh" is invalid (transport or linear_power_flow)`) {
		t.Errorf("bad model: %v", err)
	}
	e = model.Experiment{Network: &model.NetworkOptions{Model: model.NetworkLinearPowerFlow, Security: &model.SecurityOptions{}}}
	if err := e.Validate(); err == nil || !strings.Contains(err.Error(), "experiment.network.security.outages must name at least one transmission arc") {
		t.Errorf("empty outages: %v", err)
	}
	e = model.Experiment{Network: &model.NetworkOptions{Security: &model.SecurityOptions{Outages: []string{"ab"}}}}
	if err := e.Validate(); err == nil || !strings.Contains(err.Error(), `experiment.network.security needs network.model "linear_power_flow"`) {
		t.Errorf("security without the voltage law: %v", err)
	}
	e = model.Experiment{Mode: model.ModeOperate, Network: &model.NetworkOptions{Model: model.NetworkLinearPowerFlow, Security: &model.SecurityOptions{Outages: []string{"ab"}}}}
	if err := e.Validate(); err == nil || !strings.Contains(err.Error(), `experiment.network.security needs mode "plan"`) {
		t.Errorf("security outside plan: %v", err)
	}
}

// --- ADR-0011: storage profile ----------------------------------------------

// soc_profile is a Value like the other storage inputs: it interns and must
// name a defined series.
func TestStorageSOCProfile(t *testing.T) {
	m := baseModel()
	m.Technologies["pumped"] = model.Technology{Role: model.RoleStorage, Node: model.StringList{"n1"}, CarrierOut: model.StringList{"el"},
		Capacity: &model.Capacity{Existing: 4}, Storage: &model.Storage{MaxHours: fp(6), SOCProfile: &model.Value{Inline: []float64{12, 12}}}}
	if err := m.InternInlineSeries(); err != nil {
		t.Fatal(err)
	}
	if got := m.Technologies["pumped"].Storage.SOCProfile.SeriesID(); got != "_inline:tech:pumped:soc_profile" {
		t.Errorf("soc_profile not interned: %q", got)
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("storage with soc_profile should validate: %v", err)
	}
	p := m.Technologies["pumped"]
	p.Storage.SOCProfile = model.Series("missing")
	m.Technologies["pumped"] = p
	wantValidateErr(t, &m, `tech "pumped": time-series "missing" not defined`)
}

// --- ADR-0011: unit-commitment extras and the quadratic cost -----------------

func TestUnitCommitmentExtrasValidate(t *testing.T) {
	mk := func(op model.Operation, cc model.CostClass) model.Model {
		m := baseModel()
		m.Technologies["g"] = model.Technology{Role: model.RoleSupply, Node: model.StringList{"n1"}, CarrierOut: model.StringList{"el"},
			Capacity: &model.Capacity{Existing: 10}, Operation: &op, Costs: map[string]model.CostClass{"monetary": cc}}
		return m
	}
	ok := mk(model.Operation{Committable: true, RampStartUp: fp(0.6), RampShutDown: fp(0.6), StandbyCost: fp(5), InitialUptime: fp(2)}, model.CostClass{VariableOMQuadratic: fp(0.5)})
	if err := ok.Validate(); err != nil {
		t.Fatalf("extras should validate: %v", err)
	}
	cases := []struct {
		name string
		op   model.Operation
		cc   model.CostClass
		want string
	}{
		{"ramp start above one", model.Operation{RampStartUp: fp(1.5)}, model.CostClass{}, `tech g: operation.ramp_start_up must be within (0,1]`},
		{"ramp stop zero", model.Operation{RampShutDown: fp(0)}, model.CostClass{}, `tech g: operation.ramp_shut_down must be within (0,1]`},
		{"negative standby cost", model.Operation{StandbyCost: fp(-1)}, model.CostClass{}, `tech g: operation.standby_cost must not be negative`},
		{"negative initial uptime", model.Operation{InitialUptime: fp(-1)}, model.CostClass{}, `tech g: operation.initial_uptime must not be negative`},
		{"both initial times", model.Operation{InitialUptime: fp(2), InitialDowntime: fp(1)}, model.CostClass{}, `tech g: operation.initial_uptime and operation.initial_downtime are mutually exclusive`},
		{"negative quadratic cost", model.Operation{}, model.CostClass{VariableOMQuadratic: fp(-0.1)}, `tech g: costs.monetary.variable_om_quadratic must not be negative`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := mk(c.op, c.cc)
			wantValidateErr(t, &m, c.want)
		})
	}
	var op model.Operation
	if err := json.Unmarshal([]byte(`{"ramp_start_up":0.6,"ramp_shut_down":0.5,"standby_cost":5,"initial_uptime":2,"initial_downtime":0}`), &op); err != nil {
		t.Fatal(err)
	}
	if *op.RampStartUp != 0.6 || *op.RampShutDown != 0.5 || *op.StandbyCost != 5 || *op.InitialUptime != 2 || *op.InitialDowntime != 0 {
		t.Errorf("operation extras not decoded: %+v", op)
	}
}

// --- ADR-0011: investment periods ---------------------------------------------

func TestPeriodsValidate(t *testing.T) {
	m := baseModel()
	m.Technologies["g"] = model.Technology{Role: model.RoleSupply, Node: model.StringList{"n1"}, CarrierOut: model.StringList{"el"}, Capacity: &model.Capacity{Existing: 10}}
	m.Periods = []model.Period{{Name: "2030", Year: ip(2030)}, {Name: "2040", Year: ip(2040)}}
	if err := m.Validate(); err != nil {
		t.Fatalf("periods with years should validate: %v", err)
	}
	m.Periods = []model.Period{{Name: "2040", Year: ip(2040)}, {Name: "2030", Year: ip(2030)}}
	wantValidateErr(t, &m, "periods must be in ascending year order")
	m.Periods = []model.Period{{Name: "a", Year: ip(2030)}, {Name: "b"}}
	wantValidateErr(t, &m, `periods[1] (b): every period needs a year once any period has one`)
	m.Periods = []model.Period{{Name: "a", Year: ip(2030), LengthYears: fp(0)}}
	wantValidateErr(t, &m, `periods[0] (a): length_years must be positive`)
}
