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

// capacity.min must emit PyPSA p_nom_min for an expandable component (previously
// dropped, unlike Calliope's flow_cap_min).
func TestPyPSANomMin(t *testing.T) {
	payload := `{
      "model": {
        "metadata": {"name": "nmin"},
        "time": {"start": "2025-01-01", "end": "2025-01-02", "resolution": "1H"},
        "carriers": {"electricity": {}},
        "nodes": {"n1": {}},
        "technologies": {
          "gen": {
            "role": "supply", "node": "n1", "carrier_out": "electricity",
            "capacity": {"expandable": true, "min": 40, "max": 500},
            "lifetime": 25, "interest_rate": 0.05,
            "costs": {"monetary": {"investment_per_capacity": 1000}}
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
	if !strings.Contains(gens, "p_nom_min") {
		t.Errorf("generators.csv missing p_nom_min column:\n%s", gens)
	}
	if !strings.Contains(gens, "40") {
		t.Errorf("p_nom_min value (40) not emitted:\n%s", gens)
	}
}

// mode:stochastic is not implemented for PyPSA and must be rejected (not silently
// solved deterministically).
func TestPyPSAStochasticRejected(t *testing.T) {
	j := loadSample(t)
	j.Experiment.Mode = model.ModeStochastic
	if _, err := validateFor(&j, model.TargetPyPSA); err == nil {
		t.Fatal("expected mode:stochastic to be rejected for PyPSA, got nil error")
	} else if !strings.Contains(err.Error(), "stochastic") {
		t.Errorf("expected a stochastic-not-supported error, got: %v", err)
	}
}
