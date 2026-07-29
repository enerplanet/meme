// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package calliope_test

import (
	"strings"
	"testing"
)

// A node with no techs (a hub only crossed by transmission links) must still
// declare the key as an empty mapping: Calliope requires `techs: {}` and
// rejects a node definition without it.
func TestCalliopeEmptyNodeTechs(t *testing.T) {
	payload := `{
      "model": {
        "metadata": {"name": "hub"},
        "time": {"start": "2025-01-01", "end": "2025-01-02", "resolution": "1H"},
        "carriers": {"electricity": {}},
        "nodes": {"n1": {}, "n2": {}, "hub": {}},
        "technologies": {
          "gen": {"role": "supply", "node": "n1", "carrier_out": "electricity",
                  "capacity": {"expandable": true, "max": 1000}},
          "load": {"role": "demand", "node": "n2", "carrier_in": "electricity", "demand_profile": 100}
        },
        "transmission": {
          "l1": {"carrier": "electricity", "from": "n1", "to": "hub", "bidirectional": true,
                 "capacity": {"expandable": true, "max": 500}},
          "l2": {"carrier": "electricity", "from": "hub", "to": "n2", "bidirectional": true,
                 "capacity": {"expandable": true, "max": 500}}
        }
      },
      "experiment": {"mode": "plan", "solver": {"name": "cbc"}}
    }`
	y := emitCalliopeJSON(t, payload)
	if !strings.Contains(y, "  hub:\n    techs: {}\n") {
		t.Errorf("expected tech-less node to emit an empty techs mapping; got:\n%s", y)
	}
	// Nodes with techs keep their populated mapping.
	if !strings.Contains(y, "  n1:\n    techs:\n      gen:") {
		t.Errorf("expected n1 to list its techs; got:\n%s", y)
	}
}
