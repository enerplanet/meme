// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package adoptnet0_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/enerplanet/meme/internal/model"
	"github.com/enerplanet/meme/internal/target/adoptnet0"
)

// native.adopt-net0 blocks must land in the emitted AdOpT JSON at every level:
// model (ConfigModel.JSON), tech (technology_data/*.json), and node
// (Technologies.JSON). Deep-merged, overriding emitted keys.
func TestAdOptNativePassthrough(t *testing.T) {
	payload := `{
      "model": {
        "metadata": {"name": "an"},
        "time": {"start": "2025-01-01", "end": "2025-01-02", "resolution": "1H"},
        "carriers": {"electricity": {}},
        "nodes": {"n1": {"native": {"adopt-net0": {"node_flag": 7}}}},
        "technologies": {
          "batt": {
            "role": "storage", "node": "n1", "carrier_in": "electricity", "carrier_out": "electricity",
            "storage": {"max_hours": 4},
            "capacity": {"expandable": true, "max": 100},
            "lifetime": 15, "interest_rate": 0.05,
            "costs": {"monetary": {"investment_per_capacity": 1000}},
            "native": {"adopt-net0": {"Performance": {"custom_flag": 9}}}
          }
        },
        "native": {"adopt-net0": {"typical_days": 30}}
      },
      "experiment": {"mode": "plan", "solver": {"name": "glpk"}}
    }`
	var j model.Job
	if err := json.Unmarshal([]byte(payload), &j); err != nil {
		t.Fatal(err)
	}
	if _, err := validateFor(&j, model.TargetAdOpt); err != nil {
		t.Fatalf("validate: %v", err)
	}
	dir := t.TempDir()
	if _, err := (adoptnet0.AdOptNET0{}).Emit(&j, dir); err != nil {
		t.Fatalf("emit: %v", err)
	}

	// model-level -> ConfigModel.json (deep-merge alongside emitted keys)
	cfg := readJSON(t, filepath.Join(dir, "input_data", "ConfigModel.json"))
	if cfg["typical_days"] != float64(30) {
		t.Errorf("model native not merged into ConfigModel.json: %v", cfg["typical_days"])
	}
	if _, ok := cfg["optimization"]; !ok {
		t.Errorf("model native clobbered emitted config keys: %v", cfg)
	}

	// node-level -> Technologies.json (deep-merge alongside existing/new)
	tj := readJSON(t, filepath.Join(dir, "input_data", "period1", "node_data", "n1", "Technologies.json"))
	if tj["node_flag"] != float64(7) {
		t.Errorf("node native not merged into Technologies.json: %v", tj["node_flag"])
	}
	if _, ok := tj["new"]; !ok {
		t.Errorf("node native clobbered emitted keys: %v", tj)
	}

	// tech-level -> _meme_overrides.json, keyed node -> tech (patched onto the
	// copied DB tech at run time)
	ov := readJSON(t, filepath.Join(dir, "input_data", "_meme_overrides.json"))
	n1, _ := ov["n1"].(map[string]any)
	sb, _ := n1["Storage_Battery"].(map[string]any)
	perf, _ := sb["Performance"].(map[string]any)
	if perf["custom_flag"] != float64(9) {
		t.Errorf("tech native not folded into overrides: %v", sb)
	}
	if _, ok := sb["Economics"]; !ok {
		t.Errorf("tech native clobbered emitted overrides: %v", sb)
	}
}

// node.climate weather series are materialized into ClimateData.csv (so physics
// techs like Photovoltaic can compute output).
func TestAdOptClimateData(t *testing.T) {
	payload := `{
      "model": {
        "metadata": {"name": "clim"},
        "time": {"start": "2025-06-01", "end": "2025-06-01T02:00", "resolution": "1H"},
        "carriers": {"electricity": {}},
        "nodes": {"n1": {"coords": {"lat": 48.8, "lon": 12.9},
          "climate": {"ghi": "s_ghi", "temp_air": 15}}},
        "timeseries": {"s_ghi": {"source": "inline", "values": [0, 400, 800]}},
        "technologies": {
          "pv": {"role": "supply", "node": "n1", "carrier_out": "electricity",
                 "capacity": {"expandable": true, "max": 100},
                 "performance": {"type": "physics", "model": "pv"},
                 "lifetime": 25, "interest_rate": 0.05,
                 "costs": {"monetary": {"investment_per_capacity": 800}}}
        }
      },
      "experiment": {"mode": "plan", "solver": {"name": "glpk"}}
    }`
	var j model.Job
	if err := json.Unmarshal([]byte(payload), &j); err != nil {
		t.Fatal(err)
	}
	if _, err := validateFor(&j, model.TargetAdOpt); err != nil {
		t.Fatalf("validate: %v", err)
	}
	dir := t.TempDir()
	if _, err := (adoptnet0.AdOptNET0{}).Emit(&j, dir); err != nil {
		t.Fatalf("emit: %v", err)
	}
	clim, err := os.ReadFile(filepath.Join(dir, "input_data", "period1", "node_data", "n1", "ClimateData.csv"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(clim)
	// series ghi -> 0/400/800; scalar temp_air -> 15 every row; semicolon-delimited.
	if !strings.Contains(body, ";ghi;") || !strings.Contains(body, ";400;") || !strings.Contains(body, ";15;") {
		t.Errorf("ClimateData.csv not materialized from node.climate:\n%s", body)
	}
}
