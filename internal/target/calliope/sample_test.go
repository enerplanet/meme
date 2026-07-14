// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package calliope_test

import (
	"strings"
	"testing"

	"github.com/enerplanet/meme/internal/model"
)

func TestCalliopeEmitSample(t *testing.T) {
	j := loadSampleFor(t, model.TargetCalliope)
	if _, err := validateFor(&j, model.TargetCalliope); err != nil {
		t.Fatalf("validate: %v", err)
	}
	dir := emitCalliopeDir(t, j)
	y := readFile(t, dir, "model.yaml")
	// The multi-output chp keeps efficiencies at 1 and is ratio-pinned in the
	// extra math file; trade becomes market techs.
	for _, want := range []string{"base_tech: supply", "base_tech: transmission",
		"link_from: n1", "link_to: n2", "flow_cap_max: 300",
		"grid_n1_import", "grid_n1_export", "cap_method: integer"} {
		if !strings.Contains(y, want) {
			t.Errorf("model.yaml missing %q", want)
		}
	}
	math := readFile(t, dir, "additional_math.yaml")
	for _, want := range []string{"meme_ratio_chp_out_heat", "balance_conversion", "NOT [chp] in techs"} {
		if !strings.Contains(math, want) {
			t.Errorf("additional_math.yaml missing %q", want)
		}
	}
}
