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
	crf := interestRate * math.Pow(1+interestRate, lifetime) /
		(math.Pow(1+interestRate, lifetime) - 1)
	return overnight * crf
}
