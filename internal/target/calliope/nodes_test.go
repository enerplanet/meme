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

// A tech placed on several nodes must get one timeseries data table per node:
// each node's effective tech (base + node_overrides) may carry its own profile
// (e.g. PV capacity factors per region, demand per bidding zone). Two techs that
// share a series at one node must each get their own table/CSV (distinct
// techs/nodes header rows), not overwrite a shared file.
func TestCalliopePerNodeSeriesTables(t *testing.T) {
	payload := `{
      "model": {
        "metadata": {"name": "per-node"},
        "time": {"start": "2025-01-01", "end": "2025-01-03", "resolution": "24H"},
        "carriers": {"electricity": {}},
        "nodes": {"r1": {}, "r2": {}},
        "timeseries": {
          "cf_r1": {"source": "inline", "values": [0.1, 0.2]},
          "cf_r2": {"source": "inline", "values": [0.7, 0.8]}
        },
        "technologies": {
          "pv": {"role": "supply", "node": ["r1", "r2"], "carrier_out": "electricity",
                 "capacity": {"expandable": true},
                 "node_overrides": {"r1": {"operation": {"equals_pu": "cf_r1"}},
                                    "r2": {"operation": {"equals_pu": "cf_r2"}}}},
          "pv_new": {"role": "supply", "node": "r1", "carrier_out": "electricity",
                     "capacity": {"expandable": true}, "operation": {"equals_pu": "cf_r1"}},
          "export": {"role": "demand", "node": ["r1", "r2"], "carrier_in": "electricity",
                     "demand_profile": 50, "demand_curtailable": true}
        }
      },
      "experiment": {"mode": "plan", "solver": {"name": "cbc"}}
    }`
	dir := emitCalliopeJSONDir(t, payload)
	y := readFile(t, dir, "model.yaml")

	for _, key := range []string{"pv__source_use_equals:", "pv__source_use_equals__r2:", "pv_new__source_use_equals:",
		"export__sink_use_max:", "export__sink_use_max__r2:"} {
		if !strings.Contains(y, key) {
			t.Errorf("expected data table %q; got:\n%s", key, y)
		}
	}
	// Each table's CSV names its own tech and node and holds that node's values.
	checks := map[string][]string{
		"pv__source_use_equals.csv":     {"techs,pv", "nodes,r1", ",0.1"},
		"pv__source_use_equals__r2.csv": {"techs,pv", "nodes,r2", ",0.7"},
		"pv_new__source_use_equals.csv": {"techs,pv_new", "nodes,r1", ",0.1"},
	}
	for file, want := range checks {
		csv := readFile(t, dir, file)
		for _, w := range want {
			if !strings.Contains(csv, w) {
				t.Errorf("%s: expected %q in:\n%s", file, w, csv)
			}
		}
	}
}

// Transmission links carry the same capacity semantics as techs (a fixed
// existing line is pinned, an expandable one bounded) and their monetary costs:
// investment per capacity, fixed and variable O&M. Native passthrough supplies
// what the canonical link has no field for (lifetime, interest rate).
func TestCalliopeTransmissionCapacityAndCosts(t *testing.T) {
	payload := `{
      "model": {
        "metadata": {"name": "links"},
        "time": {"start": "2025-01-01", "end": "2025-01-02", "resolution": "1H"},
        "carriers": {"electricity": {}},
        "nodes": {"a": {}, "b": {}},
        "technologies": {
          "gen": {"role": "supply", "node": "a", "carrier_out": "electricity", "capacity": {"expandable": true}},
          "load": {"role": "demand", "node": "b", "carrier_in": "electricity", "demand_profile": 10}
        },
        "transmission": {
          "old_ab": {"carrier": "electricity", "from": "a", "to": "b", "bidirectional": true,
                     "capacity": {"existing": 1300, "expandable": false}},
          "new_ab": {"carrier": "electricity", "from": "a", "to": "b", "bidirectional": true,
                     "capacity": {"max": 5000, "expandable": true},
                     "costs": {"monetary": {"investment_per_capacity": 450, "fixed_om": 3, "variable_om": 0.0022}},
                     "native": {"calliope": {"lifetime": 40, "cost_interest_rate": {"data": 0.1, "index": "monetary", "dims": "costs"}}}}
        }
      },
      "experiment": {"mode": "plan", "solver": {"name": "cbc"}}
    }`
	y := emitCalliopeJSON(t, payload)
	old := section(y, "  old_ab:\n")
	for _, w := range []string{"flow_cap_max: 1300", "flow_cap_min: 1300"} {
		if !strings.Contains(old, w) {
			t.Errorf("existing line: expected %q in:\n%s", w, old)
		}
	}
	nw := section(y, "  new_ab:\n")
	for _, w := range []string{"flow_cap_max: 5000", "cost_flow_cap:", "data: 450", "cost_om_annual:", "data: 3",
		"cost_flow_out:", "data: 0.0022", "lifetime: 40", "cost_interest_rate:", "data: 0.1"} {
		if !strings.Contains(nw, w) {
			t.Errorf("expandable line: expected %q in:\n%s", w, nw)
		}
	}
}

// section returns the YAML block starting at header up to the next sibling key
// at the same indentation.
func section(y, header string) string {
	i := strings.Index(y, header)
	if i < 0 {
		return ""
	}
	rest := y[i+len(header):]
	indent := len(header) - len(strings.TrimLeft(header, " "))
	for j, line := range strings.Split(rest, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if len(line)-len(strings.TrimLeft(line, " ")) <= indent {
			return strings.Join(strings.Split(rest, "\n")[:j], "\n")
		}
	}
	return rest
}

