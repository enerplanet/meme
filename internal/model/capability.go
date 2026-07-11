// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package model

// Feature is a capability a target may or may not support. The per-target
// matrices live with the target implementations (internal/target/*) and are
// aggregated by the target registry; this file only defines the shared
// vocabulary. Capabilities without a feature key here (AC power flow, custom
// math, ...) are exactly what the native passthrough exists for.
type Feature string

const (
	FeatModePlan         Feature = "mode:plan"
	FeatModeOperate      Feature = "mode:operate"
	FeatModeAlternatives Feature = "mode:alternatives"
	FeatModePareto       Feature = "mode:pareto"
	FeatModeMonteCarlo   Feature = "mode:monte_carlo"
	FeatModeStochastic   Feature = "mode:stochastic"

	FeatTimeAggregation Feature = "time_aggregation"

	// Feature keys backing first-class model fields. Every claim a target
	// makes for one of these is enforced end-to-end: the generic gates in
	// target.validateGeneric reject the field for non-claiming targets, and
	// the capability-coverage test requires a runnable scenario per claim.
	FeatMultiCarrierConversion Feature = "multi_carrier_conversion"
	FeatEmissionLimit          Feature = "emission_limit"
	FeatImportExport           Feature = "import_export"
	FeatCommittable            Feature = "committable"
	FeatArea                   Feature = "area_constraints"
	FeatSource                 Feature = "source_constraints"
	FeatNodeOverride           Feature = "node_override"
	FeatLinearConstraint       Feature = "linear_constraint"
	FeatIndexedParams          Feature = "indexed_params"
	FeatPiecewisePerformance   Feature = "piecewise_performance"
	FeatPhysicsPerformance     Feature = "physics_performance"
	FeatPowerFlow              Feature = "power_flow"
	FeatCustomMath             Feature = "custom_math"

	// FeatObjectiveEmissions marks support for objective "min_emissions"
	// (currently AdOpT-only: the other runners would silently minimize cost).
	FeatObjectiveEmissions Feature = "objective:min_emissions"
)
