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
