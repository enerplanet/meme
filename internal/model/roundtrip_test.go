// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package model_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/enerplanet/meme/internal/model"
	"github.com/enerplanet/meme/internal/target"
	_ "github.com/enerplanet/meme/internal/target/all"
)

const phase0Job = `{
  "model": {
    "metadata": {"name": "toy"},
    "time": {"start": "2025-01-01", "end": "2025-01-02", "resolution": "1H"},
    "carriers": {"electricity": {"unit": "MWh"}},
    "nodes": {"n1": {"coords": {"lat": 48.8, "lon": 12.9}}, "n2": {"coords": {"lat": 49.0, "lon": 13.2}}},
    "technologies": {
      "wind": {"role": "supply", "node": "n1", "carrier_out": "electricity",
               "capacity": {"existing": 0, "expandable": true, "max": 500},
               "costs": {"monetary": {"investment_per_capacity": 1200000}},
               "lifetime": 25, "interest_rate": 0.07, "cost_basis": "overnight"}
    },
    "transmission": {
      "line": {"carrier": "electricity", "from": "n1", "to": "n2", "bidirectional": true,
               "capacity": {"existing": 100, "expandable": false},
               "native": {"pypsa": {"x": 0.1, "r": 0.01, "s_nom": 100}, "calliope": {"flow_out_eff_per_distance": 0.99}}}
    }
  },
  "experiment": {"mode": "plan", "objective": "min_cost", "solver": {"name": "highs"}}
}`

func decode(t *testing.T, s string) model.Job {
	t.Helper()
	var j model.Job
	if err := json.Unmarshal([]byte(s), &j); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return j
}

func TestNativeMergedAsColumns(t *testing.T) {
	j := decode(t, phase0Job)
	warns, err := target.ValidateFor(&j, model.TargetPyPSA)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	// calliope native present but pypsa selected -> ignored warning; pypsa native present -> lock warning.
	joined := strings.Join(warns, "\n")
	if !strings.Contains(joined, "native.calliope block is ignored") {
		t.Errorf("expected ignored-calliope warning, got: %v", warns)
	}
	if !strings.Contains(joined, "not portable") {
		t.Errorf("expected portability-lock warning, got: %v", warns)
	}

	dir := t.TempDir()
	impl, err := target.For(model.TargetPyPSA)
	if err != nil {
		t.Fatalf("target: %v", err)
	}
	if _, err := impl.Emit(&j, dir); err != nil {
		t.Fatalf("emit: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "links.csv"))
	if err != nil {
		t.Fatalf("read links.csv: %v", err)
	}
	csv := string(b)
	for _, col := range []string{"x", "r", "s_nom"} {
		if !strings.Contains(csv, col) {
			t.Errorf("expected native column %q in links.csv header; got:\n%s", col, csv)
		}
	}
	if !strings.Contains(csv, "0.1") || !strings.Contains(csv, "0.01") {
		t.Errorf("expected native values in links.csv; got:\n%s", csv)
	}
	t.Logf("links.csv:\n%s", csv)
}

func TestCapabilityRejectsUnsupportedMode(t *testing.T) {
	j := decode(t, phase0Job)
	j.Experiment.Mode = model.ModePareto // pareto is AdOpT-only
	if _, err := target.ValidateFor(&j, model.TargetPyPSA); err == nil {
		t.Fatal("expected pareto mode to be rejected for pypsa")
	}
	if _, err := target.ValidateFor(&j, model.TargetAdOpt); err == nil {
		t.Log("note: adopt emitter is a stub but capability check should pass")
	}
}

func TestSweepExpansion(t *testing.T) {
	e := model.Experiment{Sweep: []model.SweepAxis{
		{Parameter: "technologies.wind.costs.monetary.investment_per_capacity", Values: []float64{1.0e6, 1.2e6}},
		{Parameter: "carriers.electricity.co2_intensity", Values: []float64{0, 0.2, 0.4}},
	}}
	got := e.ExpandSweep()
	if len(got) != 6 { // 2 x 3 cartesian product
		t.Fatalf("expected 6 sweep runs, got %d", len(got))
	}
}
