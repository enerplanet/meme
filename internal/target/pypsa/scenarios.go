// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package pypsa

import (
	"fmt"
	"strings"

	"github.com/enerplanet/meme/internal/emit"
	"github.com/enerplanet/meme/internal/model"
)

// ScenarioSet is one per-scenario attribute assignment in the
// _scenarios.json sidecar: run.py sets <table>.loc[(scenario, component*),
// attribute] = value on the scenario-expanded network (component matches the
// canonical tech id and its per-node expansions id@node).
type ScenarioSet struct {
	Table     string  `json:"table"`
	Component string  `json:"component"`
	Attribute string  `json:"attribute"`
	Value     float64 `json:"value"`
}

// ScenarioSets translates one stochastic scenario's canonical override
// paths (the same dotted grammar the sweep expander uses) into PyPSA component
// assignments. An untranslatable path is an error — ValidateFor calls this so a
// stochastic payload never reaches the runner with overrides that would be
// silently dropped.
func ScenarioSets(m *model.Model, s model.Scenario) ([]ScenarioSet, error) {
	out := make([]ScenarioSet, 0, len(s.Overrides))
	for _, path := range emit.Keys(s.Overrides) {
		seg := strings.Split(path, ".")
		fail := func(reason string) error {
			return fmt.Errorf("scenario %q override %q: %s", s.Name, path, reason)
		}
		// The stochastic runner assigns numeric attribute values; string or
		// boolean override values have no PyPSA translation.
		v, numeric := s.NumericOverride(path)
		if !numeric {
			return nil, fail("non-numeric override values are not supported for pypsa stochastic scenarios")
		}
		if len(seg) < 3 {
			return nil, fail("path too short")
		}
		switch seg[0] {
		case "technologies":
			t, ok := m.Technologies[seg[1]]
			if !ok {
				return nil, fail("unknown technology")
			}
			table, err := pypsaTable(t.Role)
			if err != nil {
				return nil, fail(err.Error())
			}
			set := ScenarioSet{Table: table, Component: seg[1], Value: v}
			switch {
			case seg[2] == "efficiency" && t.Role != model.RoleDemand:
				set.Attribute = "efficiency"
			case seg[2] == "demand_profile" && t.Role == model.RoleDemand:
				set.Attribute = "p_set"
			case seg[2] == "capacity" && len(seg) == 4 && seg[3] == "max":
				set.Attribute = "p_nom_max"
			case seg[2] == "capacity" && len(seg) == 4 && seg[3] == "existing":
				set.Attribute = "p_nom"
			case seg[2] == "costs" && len(seg) == 5 && seg[3] == model.PrimaryCostClass && seg[4] == "variable_om":
				set.Attribute = "marginal_cost"
			default:
				return nil, fail("field is not translatable to a PyPSA attribute (supported: efficiency, demand_profile, capacity.max, capacity.existing, costs.monetary.variable_om)")
			}
			out = append(out, set)
		case "carriers":
			if _, ok := m.Carriers[seg[1]]; !ok {
				return nil, fail("unknown carrier")
			}
			if seg[2] != "co2_intensity" {
				return nil, fail("only co2_intensity is translatable on a carrier")
			}
			out = append(out, ScenarioSet{Table: "carriers", Component: seg[1], Attribute: "co2_emissions", Value: v})
		default:
			return nil, fail("unsupported path root")
		}
	}
	return out, nil
}

// pypsaTable maps a role onto its PyPSA component table.
func pypsaTable(r model.Role) (string, error) {
	switch r {
	case model.RoleSupply:
		return "generators", nil
	case model.RoleDemand:
		return "loads", nil
	case model.RoleConversion:
		return "links", nil
	case model.RoleStorage:
		return "storage_units", nil
	}
	return "", fmt.Errorf("role %q has no PyPSA table", r)
}

// NormalizedScenarioWeights returns the scenario probability weights scaled to
// sum to 1; unspecified (zero) weights share the remaining mass equally.
func NormalizedScenarioWeights(scenarios []model.Scenario) map[string]float64 {
	out := make(map[string]float64, len(scenarios))
	total := 0.0
	zeros := 0
	for _, s := range scenarios {
		total += s.Weight
		if s.Weight == 0 {
			zeros++
		}
	}
	if total <= 0 {
		for _, s := range scenarios {
			out[s.Name] = 1.0 / float64(len(scenarios))
		}
		return out
	}
	// Zero-weight scenarios get the average of the specified weights so every
	// branch stays in the program, then everything is normalized.
	fill := total / float64(len(scenarios)-zeros)
	sum := 0.0
	for _, s := range scenarios {
		w := s.Weight
		if w == 0 {
			w = fill
		}
		out[s.Name] = w
		sum += w
	}
	for k := range out {
		out[k] /= sum
	}
	return out
}
