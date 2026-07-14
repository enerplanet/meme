// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package pypsa_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/enerplanet/meme/internal/model"
	"github.com/enerplanet/meme/internal/target/pypsa"
)

func TestPhysicsPerformanceSidecar(t *testing.T) {
	dir := emitSampleDir(t) // sample pv uses a physics performance model
	sc := readFile(t, dir, "_performance.json")
	for _, want := range []string{"\"model\": \"pv\"", "physics", "pv_cf"} {
		if !strings.Contains(sc, want) {
			t.Errorf("_performance.json missing %q:\n%s", want, sc)
		}
	}
}

func TestPhysicsWithoutPrecomputedGating(t *testing.T) {
	j := loadSample(t)
	pv := j.Model.Technologies["pv"]
	pv.Performance = &model.Performance{Type: model.PerfPhysics, Model: "pv"} // no precomputed
	j.Model.Technologies["pv"] = pv
	if _, err := validateFor(&j, model.TargetPyPSA); err == nil {
		t.Error("expected pypsa to reject a physics model without a precomputed profile")
	}

	ja := loadSampleFor(t, model.TargetAdOpt)
	pva := ja.Model.Technologies["pv"]
	pva.Performance = &model.Performance{Type: model.PerfPhysics, Model: "pv"}
	ja.Model.Technologies["pv"] = pva
	if _, err := validateFor(&ja, model.TargetAdOpt); err != nil {
		t.Errorf("adopt should accept a physics model natively: %v", err)
	}
}

func TestPiecewiseGating(t *testing.T) {
	minLoad := 0.3
	pw := &model.Performance{
		Type:    model.PerfPiecewise,
		MinLoad: &minLoad,
		Breakpoints: []model.Breakpoint{
			{Load: 0.3, Efficiency: 0.40},
			{Load: 1.0, Efficiency: 0.55},
		},
	}

	j := loadSample(t)
	ccgt := j.Model.Technologies["ccgt"]
	ccgt.Performance = pw
	j.Model.Technologies["ccgt"] = ccgt
	if _, err := validateFor(&j, model.TargetPyPSA); err == nil {
		t.Error("expected pypsa to reject piecewise part-load performance")
	}

	// Calliope accepts piecewise on a fixed-capacity conversion tech (the
	// breakpoints render as constants); an expandable or non-conversion tech is
	// rejected with a pointed message.
	jc := loadSampleFor(t, model.TargetCalliope)
	conv := model.Technology{
		Role:        model.RoleConversion,
		Node:        model.StringList{"n1"},
		CarrierIn:   model.StringList{"gas"},
		CarrierOut:  model.StringList{"electricity"},
		Capacity:    &model.Capacity{Existing: 200, Expandable: false},
		Performance: pw,
	}
	jc.Model.Technologies["pwconv"] = conv
	if _, err := validateFor(&jc, model.TargetCalliope); err != nil {
		t.Errorf("calliope should accept piecewise on a fixed conversion tech: %v", err)
	}
	bad := conv
	bad.Capacity = &model.Capacity{Expandable: true}
	jc.Model.Technologies["pwconv"] = bad
	if _, err := validateFor(&jc, model.TargetCalliope); err == nil {
		t.Error("expected calliope to reject piecewise on an expandable tech")
	}
	delete(jc.Model.Technologies, "pwconv")
}

func TestPerformanceValidation(t *testing.T) {
	cases := map[string]*model.Performance{
		"unknown physics model": {Type: model.PerfPhysics, Model: "fusion"},
		"too few breakpoints":   {Type: model.PerfPiecewise, Breakpoints: []model.Breakpoint{{Load: 0.5, Efficiency: 0.5}}},
		"non-monotonic load": {Type: model.PerfPiecewise, Breakpoints: []model.Breakpoint{
			{Load: 0.8, Efficiency: 0.5}, {Load: 0.3, Efficiency: 0.4}}},
	}
	for name, p := range cases {
		if err := p.Validate(); err == nil {
			t.Errorf("%s: expected a validation error", name)
		}
	}
	ok := &model.Performance{Type: model.PerfPhysics, Model: "heat_pump"}
	if err := ok.Validate(); err != nil {
		t.Errorf("valid physics model rejected: %v", err)
	}
}

func TestConstantPerformanceEfficiency(t *testing.T) {
	raw := `{
      "model": {
        "metadata": {"name": "c"},
        "time": {"start": "2025-01-01", "end": "2025-01-02", "resolution": "1H"},
        "carriers": {"gas": {"unit": "MWh"}, "electricity": {"unit": "MWh"}},
        "nodes": {"n1": {}},
        "technologies": {
          "gt": {"role": "conversion", "node": "n1", "carrier_in": "gas", "carrier_out": "electricity",
                 "performance": {"type": "constant", "efficiency": 0.6},
                 "capacity": {"existing": 50, "expandable": false}}
        }
      },
      "experiment": {"solver": {"name": "highs"}}
    }`
	var j model.Job
	if err := json.Unmarshal([]byte(raw), &j); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, err := validateFor(&j, model.TargetPyPSA); err != nil {
		t.Fatalf("validate: %v", err)
	}
	dir := t.TempDir()
	if _, err := (pypsa.PyPSA{}).Emit(&j, dir); err != nil {
		t.Fatalf("emit: %v", err)
	}
	links := readFile(t, dir, "links.csv")
	if !strings.Contains(links, "0.6") {
		t.Errorf("constant performance efficiency 0.6 should reach links.csv:\n%s", links)
	}
}
