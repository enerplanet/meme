// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package adoptnet0_test

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/enerplanet/meme/internal/model"
	"github.com/enerplanet/meme/internal/target/adoptnet0"
)

func networkJob(t *testing.T, distance string) model.Job {
	t.Helper()
	payload := `{
      "model": {
        "metadata": {"name": "net"},
        "time": {"start": "2025-01-01", "end": "2025-01-01T03:00", "resolution": "1H"},
        "carriers": {"CO2captured": {}},
        "nodes": {"src": {}, "hub": {}, "sink": {}},
        "technologies": {},
        "transmission": {
          "a": {"carrier": "CO2captured", "from": "src", "to": "hub", ` + distance + `
                "capacity": {"expandable": true, "max": 300},
                "costs": {"monetary": {"investment_per_capacity_distance": 5}}},
          "b": {"carrier": "CO2captured", "from": "hub", "to": "sink", "distance": 90,
                "capacity": {"expandable": true, "max": 400},
                "costs": {"monetary": {"investment_per_capacity_distance": 5}}}
        }
      },
      "experiment": {"mode": "plan", "solver": {"name": "glpk"}}
    }`
	var j model.Job
	if err := json.Unmarshal([]byte(payload), &j); err != nil {
		t.Fatal(err)
	}
	return j
}

func TestAdOptNetworks(t *testing.T) {
	j := networkJob(t, `"distance": 150,`)
	if _, err := validateFor(&j, model.TargetAdOpt); err != nil {
		t.Fatalf("validate: %v", err)
	}
	dir := t.TempDir()
	if _, err := (adoptnet0.AdOptNET0{}).Emit(&j, dir); err != nil {
		t.Fatalf("emit: %v", err)
	}
	root := filepath.Join(dir, "input_data")
	topo := filepath.Join(root, "period1", "network_topology", "new", "CO2_Pipeline")

	if got := readFileAbs(t, filepath.Join(root, "period1", "Networks.json")); !strings.Contains(got, `"CO2_Pipeline"`) {
		t.Errorf("Networks.json: %s", got)
	}
	// rows = from, columns = to (nodes sorted: hub, sink, src)
	want := ";hub;sink;src\nhub;0;1;0\nsink;0;0;0\nsrc;1;0;0\n"
	if got := readFileAbs(t, filepath.Join(topo, "connection.csv")); got != want {
		t.Errorf("connection.csv:\n%s", got)
	}
	if got := readFileAbs(t, filepath.Join(topo, "size_max_arcs.csv")); !strings.Contains(got, "hub;0;400;0") {
		t.Errorf("size_max_arcs.csv:\n%s", got)
	}
	ov := readJSON(t, filepath.Join(root, "_meme_network_overrides.json"))
	econ := ov["CO2_Pipeline"].(map[string]any)["Economics"].(map[string]any)
	if econ["gamma4"] != float64(5) || econ["gamma1"] != float64(0) {
		t.Errorf("economics patch: %v", econ)
	}

	j = networkJob(t, "")
	if _, err := validateFor(&j, model.TargetAdOpt); err == nil || !strings.Contains(err.Error(), "distance") {
		t.Errorf("missing distance must be rejected, got %v", err)
	}

	j = networkJob(t, `"distance": 150, "energy_consumption": {"carrier": "CO2captured", "per_flow": 0.1},`)
	if _, err := validateFor(&j, model.TargetAdOpt); err == nil || !strings.Contains(err.Error(), "energy_consumption") {
		t.Errorf("unmapped energy_consumption must be rejected, got %v", err)
	}
}
