// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

// Package emit is the shared emission toolkit the target packages build on:
// deterministic CSV/JSON writers, scalar/series value helpers, native
// passthrough merging, and the cost math every framework translation needs
// (CRF annualization). It depends only on the model package; the
// framework-specific emitters live in internal/target/{pypsa,calliope,adoptnet0}.
package emit

import (
	"math"
)

// ---------------------------------------------------------------------------
// Shared transformation helpers.
// ---------------------------------------------------------------------------

// AnnualizedCapex converts an overnight investment cost into an annualized cost
// via the capital recovery factor. Targets that expect annualized investment
// (PyPSA capital_cost) call this when Technology.CostBasis is "overnight";
// targets that annualize internally (Calliope, given interest_rate+lifetime)
// pass the overnight figure through unchanged.
//
// This is the single most error-prone step in cross-framework translation: skip
// it and every objective value is off by the CRF.
func AnnualizedCapex(overnight, interestRate, lifetime float64) float64 {
	if lifetime <= 0 {
		return overnight
	}
	if interestRate == 0 {
		return overnight / lifetime
	}
	// The textbook CRF r(1+r)^L/((1+r)^L-1) stays in place for the normal
	// parameter range (the golden corpus pins its exact bit patterns), but it
	// is ill-conditioned at the edges: for tiny |r| the (1+r)^L-1 subtraction
	// cancels catastrophically ((1+r) rounds to 1 below ~1e-16, dividing by
	// zero), and a huge L overflows the power into Inf/Inf = NaN. Those
	// regimes route through the algebraically identical r/-expm1(-L*log1p(r)),
	// which is well-conditioned there.
	crf := math.NaN()
	if math.Abs(interestRate) >= 1e-6 {
		pw := math.Pow(1+interestRate, lifetime)
		crf = interestRate * pw / (pw - 1)
	}
	if math.IsNaN(crf) || math.IsInf(crf, 0) {
		crf = interestRate / -math.Expm1(-lifetime*math.Log1p(interestRate))
	}
	if math.IsNaN(crf) || math.IsInf(crf, 0) {
		// Rates at or below -100% (or otherwise degenerate inputs) have no
		// defined CRF; fall back to straight-line depreciation so every finite
		// input yields a finite result. (The CSV writer additionally rejects
		// non-finite cells, so garbage inputs still cannot reach a file.)
		return overnight / lifetime
	}
	return overnight * crf
}
