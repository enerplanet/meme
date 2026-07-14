// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package target_test

import (
	"testing"

	"github.com/enerplanet/meme/internal/model"
	"github.com/enerplanet/meme/internal/scenarios"
	"github.com/enerplanet/meme/internal/target"
	_ "github.com/enerplanet/meme/internal/target/all"
)

// featureScenarios maps every claimed (target, feature) pair onto the scenario
// file that exercises it end-to-end. TestCapabilityCoverage enforces that the
// capability matrix can NEVER again claim a feature no scenario covers: adding
// a matrix entry without a scenario (or a documented unit-test exception) is a
// CI failure.
var featureScenarios = map[model.Target]map[model.Feature]string{
	model.TargetPyPSA: {
		model.FeatModePlan:               "pypsa_generators.json",
		model.FeatModeOperate:            "pypsa_operate.json",
		model.FeatModeAlternatives:       "pypsa_alternatives.json",
		model.FeatModeStochastic:         "pypsa_stochastic.json",
		model.FeatMultiCarrierConversion: "pypsa_conversion.json",
		model.FeatEmissionLimit:          "pypsa_emission.json",
		model.FeatImportExport:           "pypsa_trade.json",
		model.FeatCommittable:            "pypsa_committable.json",
		model.FeatNodeOverride:           "pypsa_node_override.json",
		model.FeatLinearConstraint:       "pypsa_constraints_sidecar.json",
	},
	model.TargetCalliope: {
		model.FeatModePlan:               "calliope_plan.json",
		model.FeatModeOperate:            "calliope_operate.json",
		model.FeatModeAlternatives:       "calliope_spores.json",
		model.FeatTimeAggregation:        "calliope_resample.json",
		model.FeatMultiCarrierConversion: "calliope_conversion.json",
		model.FeatEmissionLimit:          "calliope_custommath.json",
		model.FeatImportExport:           "calliope_trade.json",
		model.FeatCommittable:            "calliope_committable.json",
		model.FeatArea:                   "calliope_source_native.json",
		model.FeatSource:                 "calliope_source_native.json",
		model.FeatNodeOverride:           "calliope_node_override.json",
		model.FeatLinearConstraint:       "calliope_custommath.json",
		model.FeatPiecewisePerformance:   "calliope_piecewise.json",
		model.FeatCustomMath:             "calliope_custommath.json",
	},
	model.TargetAdOpt: {
		model.FeatModePlan:           "adopt_storage.json",
		model.FeatModePareto:         "adopt_pareto.json",
		model.FeatModeMonteCarlo:     "adopt_monte_carlo.json",
		model.FeatTimeAggregation:    "adopt_typicaldays.json",
		model.FeatImportExport:       "adopt_demand_trade.json",
		model.FeatEmissionLimit:      "adopt_emission.json",
		model.FeatNodeOverride:       "adopt_node_override.json",
		model.FeatPhysicsPerformance: "adopt_pv_climate.json",
		model.FeatObjectiveEmissions: "adopt_min_emissions.json",
	},
}

// coverageExceptions are claimed features whose coverage lives in a unit test
// instead of a runnable scenario — each with the covering test named, so the
// exception stays auditable.
var coverageExceptions = map[model.Target]map[model.Feature]string{
	model.TargetCalliope: {
		model.FeatIndexedParams: "TestMultiValuedIndexedRejectedForPyPSA (indexed Value round-trip + calliope acceptance)",
	},
}

// TestCapabilityCoverage: every capability the matrix claims must be backed by
// an existing, target-valid scenario (or a named unit-test exception).
func TestCapabilityCoverage(t *testing.T) {
	for targetName, feats := range target.CapabilityMatrix() {
		tgt := model.Target(targetName)
		for featName, claimed := range feats {
			if !claimed {
				continue
			}
			feat := model.Feature(featName)
			file, ok := featureScenarios[tgt][feat]
			if !ok {
				if why, exempt := coverageExceptions[tgt][feat]; exempt {
					t.Logf("%s/%s covered by unit test: %s", tgt, feat, why)
					continue
				}
				t.Errorf("capability %s/%s is claimed but has no covering scenario (add one to featureScenarios or a justified exception)", tgt, feat)
				continue
			}
			j, err := scenarios.Load(file)
			if err != nil {
				t.Errorf("capability %s/%s: scenario %s unreadable: %v", tgt, feat, file, err)
				continue
			}
			if _, err := target.ValidateFor(&j, tgt); err != nil {
				t.Errorf("capability %s/%s: scenario %s does not validate for its target: %v", tgt, feat, file, err)
			}
		}
	}
}
