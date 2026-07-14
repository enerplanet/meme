// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package calliope_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/enerplanet/meme/internal/model"
	"github.com/enerplanet/meme/internal/target/calliope"
	"github.com/enerplanet/meme/internal/target/pypsa"
)

func emitCalliopeJSON(t *testing.T, payload string) string {
	t.Helper()
	var j model.Job
	if err := json.Unmarshal([]byte(payload), &j); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, err := validateFor(&j, model.TargetCalliope); err != nil {
		t.Fatalf("validate: %v", err)
	}
	dir := t.TempDir()
	if _, err := (calliope.Calliope{}).Emit(&j, dir); err != nil {
		t.Fatalf("emit: %v", err)
	}
	return readFile(t, dir, "model.yaml")
}

// Calliope-specific params supplied via native.calliope (tech + node) must land
// verbatim in model.yaml — including nested objects.
func TestCalliopeNativePassthrough(t *testing.T) {
	payload := `{
      "model": {
        "metadata": {"name": "nat"},
        "time": {"start": "2025-01-01", "end": "2025-01-02", "resolution": "1H"},
        "carriers": {"electricity": {}},
        "nodes": {"n1": {"native": {"calliope": {"available_area": 1234}}}},
        "technologies": {
          "gen": {
            "role": "supply", "node": "n1", "carrier_out": "electricity",
            "capacity": {"expandable": true, "max": 100},
            "native": {"calliope": {
              "flow_out_parasitic_eff": 0.05,
              "source_use_max": {"data": 1, "index": "electricity", "dims": "carriers"}
            }}
          }
        }
      },
      "experiment": {"mode": "plan", "solver": {"name": "cbc"}}
    }`
	y := emitCalliopeJSON(t, payload)
	for _, want := range []string{
		"flow_out_parasitic_eff: 0.05", // tech-level Calliope-only param
		"source_use_max:",              // nested object passthrough
		"available_area: 1234",         // node-level passthrough
	} {
		if !strings.Contains(y, want) {
			t.Errorf("expected native passthrough %q; got:\n%s", want, y)
		}
	}
}

// Shared params (also in PyPSA/AdOpT) are now emitted as first-class Calliope
// attributes rather than dropped.
func TestCalliopeSharedParams(t *testing.T) {
	payload := `{
      "model": {
        "metadata": {"name": "shared"},
        "time": {"start": "2025-01-01", "end": "2025-01-02", "resolution": "1H"},
        "carriers": {"electricity": {}},
        "nodes": {"n1": {}},
        "technologies": {
          "gen": {
            "role": "supply", "node": "n1", "carrier_out": "electricity",
            "capacity": {"expandable": true, "max": 100},
            "operation": {"min_pu": 0.2, "ramp_up": 0.5},
            "lifetime": 25, "interest_rate": 0.05,
            "costs": {"monetary": {"investment_per_capacity": 1000, "fixed_om": 20, "purchase": 5000}}
          },
          "batt": {
            "role": "storage", "node": "n1", "carrier_in": "electricity", "carrier_out": "electricity",
            "storage": {"initial_soc": 0.5, "energy_capacity": {"max": 400, "min": 50}},
            "lifetime": 15, "interest_rate": 0.05,
            "costs": {"monetary": {"investment_per_energy_capacity": 300}}
          }
        }
      },
      "experiment": {"mode": "plan", "solver": {"name": "cbc"}}
    }`
	y := emitCalliopeJSON(t, payload)
	for _, want := range []string{
		"flow_out_min_relative: 0.2", // operation.min_pu
		"flow_ramping: 0.5",          // operation.ramp_up
		"cost_om_annual:",            // costs.fixed_om
		"cost_purchase:",             // costs.purchase
		"storage_initial: 0.5",       // storage.initial_soc
		"storage_cap_max: 400",       // energy_capacity.max
		"storage_cap_min: 50",        // energy_capacity.min
		"cost_storage_cap:",          // investment_per_energy_capacity
	} {
		if !strings.Contains(y, want) {
			t.Errorf("expected shared param %q; got:\n%s", want, y)
		}
	}
}

// fixed_om is folded into PyPSA capital_cost (no separate column exists).
func TestPyPSAFixedOMFolded(t *testing.T) {
	payload := `{
      "model": {
        "metadata": {"name": "fom"},
        "time": {"start": "2025-01-01", "end": "2025-01-02", "resolution": "1H"},
        "carriers": {"electricity": {}},
        "nodes": {"n1": {}},
        "technologies": {
          "gen": {
            "role": "supply", "node": "n1", "carrier_out": "electricity",
            "capacity": {"expandable": true, "max": 100},
            "cost_basis": "annualized",
            "costs": {"monetary": {"investment_per_capacity": 1000, "fixed_om": 250}}
          }
        }
      },
      "experiment": {"mode": "plan", "solver": {"name": "highs"}}
    }`
	var j model.Job
	if err := json.Unmarshal([]byte(payload), &j); err != nil {
		t.Fatal(err)
	}
	if _, err := validateFor(&j, model.TargetPyPSA); err != nil {
		t.Fatalf("validate: %v", err)
	}
	dir := t.TempDir()
	if _, err := (pypsa.PyPSA{}).Emit(&j, dir); err != nil {
		t.Fatalf("emit: %v", err)
	}
	gens := readFile(t, dir, "generators.csv")
	// annualized basis: capital_cost = investment (1000) + fixed_om (250) = 1250.
	if !strings.Contains(gens, "1250") {
		t.Errorf("expected capital_cost to fold in fixed_om (1250); got:\n%s", gens)
	}
}
