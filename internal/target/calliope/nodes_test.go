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

// Tech-level capacity and costs come from the base tech, not the first node's
// override: a system-wide cap must survive, and the first node's per-node max /
// cost must not become the default for the tech's other nodes (each node's
// override is projected separately under nodes.<node>.techs).
func TestCalliopeTechLevelCapacityNotFromFirstNode(t *testing.T) {
	payload := `{
      "model": {
        "metadata": {"name": "sysw"},
        "time": {"start": "2025-01-01", "end": "2025-01-02", "resolution": "1H"},
        "carriers": {"electricity": {}},
        "nodes": {"a": {}, "b": {}, "c": {}},
        "technologies": {
          "bio": {"role": "supply", "node": ["a", "b", "c"], "carrier_out": "electricity",
                  "capacity": {"expandable": true, "systemwide_max": 4000},
                  "lifetime": 20, "interest_rate": 0.1,
                  "costs": {"monetary": {"investment_per_capacity": 2901}},
                  "node_overrides": {"a": {"capacity": {"expandable": true, "max": 4000},
                                           "costs": {"monetary": {"investment_per_capacity": 999}}},
                                     "b": {"capacity": {"expandable": true, "max": 1000}}}},
          "load": {"role": "demand", "node": "a", "carrier_in": "electricity", "demand_profile": 10}
        }
      },
      "experiment": {"mode": "plan", "solver": {"name": "cbc"}}
    }`
	y := emitCalliopeJSON(t, payload)
	tech := section(y, "  bio:\n")
	if !strings.Contains(tech, "flow_cap_max_systemwide: 4000") {
		t.Errorf("expected the system-wide cap at tech level; got:\n%s", tech)
	}
	if strings.Contains(tech, "flow_cap_max:") || strings.Contains(tech, "data: 999") {
		t.Errorf("first node's override leaked into the tech defaults:\n%s", tech)
	}
	if !strings.Contains(tech, "data: 2901") {
		t.Errorf("expected the base investment cost at tech level:\n%s", tech)
	}
	for _, w := range []string{"flow_cap_max: 4000", "data: 999", "flow_cap_max: 1000"} {
		if !strings.Contains(y, w) {
			t.Errorf("expected node override %q; got:\n%s", w, y)
		}
	}
}

// A non-expandable tech with no existing capacity is fixed at ZERO (e.g. a
// placeholder tech with energy_cap_equals: 0), not unbounded.
func TestCalliopeFixedZeroCapacityIsPinned(t *testing.T) {
	payload := `{
      "model": {
        "metadata": {"name": "zero"},
        "time": {"start": "2025-01-01", "end": "2025-01-02", "resolution": "1H"},
        "carriers": {"electricity": {}},
        "nodes": {"a": {}},
        "technologies": {
          "dummy": {"role": "supply", "node": "a", "carrier_out": "electricity",
                    "capacity": {"existing": 0, "expandable": false}},
          "load": {"role": "demand", "node": "a", "carrier_in": "electricity", "demand_profile": 10}
        }
      },
      "experiment": {"mode": "plan", "solver": {"name": "cbc"}}
    }`
	tech := section(emitCalliopeJSON(t, payload), "  dummy:\n")
	for _, w := range []string{"flow_cap_max: 0", "flow_cap_min: 0"} {
		if !strings.Contains(tech, w) {
			t.Errorf("expected %q (fixed at zero); got:\n%s", w, tech)
		}
	}
}

// Storage: an investment priced per unit of energy capacity is annualized with
// the tech's interest rate too (not only per-power investment), and an existing
// non-expandable energy capacity is pinned.
func TestCalliopeStorageInterestAndExistingEnergy(t *testing.T) {
	payload := `{
      "model": {
        "metadata": {"name": "store"},
        "time": {"start": "2025-01-01", "end": "2025-01-02", "resolution": "1H"},
        "carriers": {"electricity": {}, "methane": {}},
        "nodes": {"a": {}},
        "technologies": {
          "battery": {"role": "storage", "node": "a", "carrier_in": "electricity", "carrier_out": "electricity",
                      "capacity": {"expandable": true}, "lifetime": 15, "interest_rate": 0.1,
                      "storage": {"charge_eff": 0.98, "discharge_eff": 0.98},
                      "costs": {"monetary": {"investment_per_energy_capacity": 433}}},
          "gas_store": {"role": "storage", "node": "a", "carrier_in": "methane", "carrier_out": "methane",
                        "capacity": {"expandable": true},
                        "storage": {"energy_capacity": {"existing": 1e10, "expandable": false}}},
          "gen": {"role": "supply", "node": "a", "carrier_out": "electricity", "capacity": {"expandable": true}},
          "load": {"role": "demand", "node": "a", "carrier_in": "electricity", "demand_profile": 10}
        }
      },
      "experiment": {"mode": "plan", "solver": {"name": "cbc"}}
    }`
	y := emitCalliopeJSON(t, payload)
	bat := section(y, "  battery:\n")
	for _, w := range []string{"cost_storage_cap:", "cost_interest_rate:", "data: 0.1", "flow_out_eff: 0.98", "flow_in_eff: 0.98"} {
		if !strings.Contains(bat, w) {
			t.Errorf("battery: expected %q in:\n%s", w, bat)
		}
	}
	gas := section(y, "  gas_store:\n")
	for _, w := range []string{"storage_cap_max: 1.0e+10", "storage_cap_min: 1.0e+10"} {
		if !strings.Contains(gas, w) {
			t.Errorf("gas_store: expected %q in:\n%s", w, gas)
		}
	}
}

// A (single-input) conversion's capacity is referenced to its input carrier
// (canonical semantics, as PyPSA's link p_nom): capacity bounds and
// per-capacity costs apply to that carrier only — not once per carrier, which
// would bound and charge the output capacity too.
func TestCalliopeConversionCapacityOnInputCarrier(t *testing.T) {
	payload := `{
      "model": {
        "metadata": {"name": "conv"},
        "time": {"start": "2025-01-01", "end": "2025-01-02", "resolution": "1H"},
        "carriers": {"electricity": {}, "hydrogen": {}},
        "nodes": {"a": {}},
        "technologies": {
          "gen": {"role": "supply", "node": "a", "carrier_out": "electricity", "capacity": {"expandable": true}},
          "electrolysis": {"role": "conversion", "node": "a", "carrier_in": "electricity", "carrier_out": "hydrogen",
                           "efficiency": 0.66, "capacity": {"expandable": true, "max": 1000},
                           "lifetime": 18, "interest_rate": 0.1,
                           "costs": {"monetary": {"investment_per_capacity": 792, "fixed_om": 23.76}}},
          "h2": {"role": "demand", "node": "a", "carrier_in": "hydrogen", "demand_profile": 10}
        }
      },
      "experiment": {"mode": "plan", "solver": {"name": "cbc"}}
    }`
	tech := section(emitCalliopeJSON(t, payload), "  electrolysis:\n")
	for _, w := range []string{
		"flow_cap_max:\n      data: 1000\n      index: electricity\n      dims: carriers",
		"cost_flow_cap:\n      data: 792\n      index: [[monetary, electricity]]\n      dims: [costs, carriers]",
		"cost_om_annual:\n      data: 23.76\n      index: [[monetary, electricity]]\n      dims: [costs, carriers]",
	} {
		if !strings.Contains(tech, w) {
			t.Errorf("expected\n%s\nin:\n%s", w, tech)
		}
	}
}

// Per-node storage settings (e.g. each existing pumped-hydro plant's own
// reservoir size) must be projected into the node override, not dropped —
// otherwise the reservoir is unbounded.
func TestCalliopeNodeOverrideStorage(t *testing.T) {
	payload := `{
      "model": {
        "metadata": {"name": "phs"},
        "time": {"start": "2025-01-01", "end": "2025-01-02", "resolution": "1H"},
        "carriers": {"electricity": {}},
        "nodes": {"a": {}, "b": {}},
        "technologies": {
          "phs": {"role": "storage", "node": ["a", "b"], "carrier_in": "electricity", "carrier_out": "electricity",
                  "storage": {"charge_eff": 0.87, "discharge_eff": 0.87},
                  "node_overrides": {
                    "a": {"capacity": {"existing": 1356, "expandable": false},
                          "storage": {"energy_capacity": {"existing": 125591, "expandable": false}}},
                    "b": {"capacity": {"existing": 100, "expandable": false},
                          "storage": {"energy_capacity": {"existing": 900, "expandable": false}, "max_discharge_rate": 0.25}}}},
          "gen": {"role": "supply", "node": "a", "carrier_out": "electricity", "capacity": {"expandable": true}},
          "load": {"role": "demand", "node": "a", "carrier_in": "electricity", "demand_profile": 10}
        }
      },
      "experiment": {"mode": "plan", "solver": {"name": "cbc"}}
    }`
	y := emitCalliopeJSON(t, payload)
	a := section(y, "      phs:\n") // first node's override block (node a)
	for _, w := range []string{"storage_cap_max: 125591", "storage_cap_min: 125591"} {
		if !strings.Contains(a, w) {
			t.Errorf("node a: expected %q in:\n%s", w, a)
		}
	}
	for _, w := range []string{"storage_cap_max: 900", "storage_cap_min: 900", "flow_cap_per_storage_cap_max: 0.25"} {
		if !strings.Contains(y, w) {
			t.Errorf("node b: expected %q; got:\n%s", w, y)
		}
	}
}

// Tech-level storage settings (efficiencies) survive a node override that only
// sets the node's reservoir size — for multi-node and single-node techs alike.
func TestCalliopeTechLevelStorageSurvivesNodeOverride(t *testing.T) {
	payload := `{
      "model": {
        "metadata": {"name": "phs2"},
        "time": {"start": "2025-01-01", "end": "2025-01-02", "resolution": "1H"},
        "carriers": {"electricity": {}},
        "nodes": {"a": {}, "b": {}},
        "technologies": {
          "phs": {"role": "storage", "node": ["a", "b"], "carrier_in": "electricity", "carrier_out": "electricity",
                  "storage": {"charge_eff": 0.87, "discharge_eff": 0.87},
                  "node_overrides": {"a": {"capacity": {"existing": 10, "expandable": false},
                                           "storage": {"energy_capacity": {"existing": 900, "expandable": false}}}}},
          "solo": {"role": "storage", "node": "a", "carrier_in": "electricity", "carrier_out": "electricity",
                   "storage": {"charge_eff": 0.9, "discharge_eff": 0.9},
                   "node_overrides": {"a": {"storage": {"energy_capacity": {"existing": 50, "expandable": false}}}}},
          "gen": {"role": "supply", "node": "a", "carrier_out": "electricity", "capacity": {"expandable": true}},
          "load": {"role": "demand", "node": "a", "carrier_in": "electricity", "demand_profile": 10}
        }
      },
      "experiment": {"mode": "plan", "solver": {"name": "cbc"}}
    }`
	y := emitCalliopeJSON(t, payload)
	phs := section(y, "  phs:\n")
	for _, w := range []string{"flow_out_eff: 0.87", "flow_in_eff: 0.87"} {
		if !strings.Contains(phs, w) {
			t.Errorf("phs: expected %q in:\n%s", w, phs)
		}
	}
	if strings.Contains(phs, "storage_cap_max: 900") {
		t.Errorf("phs: node a's reservoir leaked to the tech level:\n%s", phs)
	}
	solo := section(y, "  solo:\n")
	for _, w := range []string{"flow_out_eff: 0.9", "flow_in_eff: 0.9", "storage_cap_max: 50"} {
		if !strings.Contains(solo, w) {
			t.Errorf("solo: expected %q in:\n%s", w, solo)
		}
	}
}

