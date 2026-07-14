// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package calliope_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/enerplanet/meme/internal/model"
	"github.com/enerplanet/meme/internal/target/calliope"
)

func TestCalliopeAddMathDefault(t *testing.T) {
	j := loadSampleFor(t, model.TargetCalliope) // has constraints + emission_limits, custom_math unset (=> on)
	dir := t.TempDir()
	if _, err := (calliope.Calliope{}).Emit(&j, dir); err != nil {
		t.Fatalf("emit: %v", err)
	}
	yml := readFile(t, dir, "model.yaml")
	// Calliope 0.7: register the file under config.init.math_paths, apply by name
	// via config.init.extra_math. The committable ccgt additionally requests the
	// built-in milp math, which must come before the custom file.
	for _, want := range []string{"math_paths:", "additional_math: additional_math.yaml", "extra_math: [milp, additional_math]"} {
		if !strings.Contains(yml, want) {
			t.Errorf("model.yaml should reference %q:\n%s", want, yml)
		}
	}
	math := readFile(t, dir, "additional_math.yaml")
	for _, want := range []string{
		"constraints:", "max_vre_capacity", "pv_plus_ccgt_cap", "co2_cap",
		// Calliope 0.7 slices one member per dim (no list brackets).
		"sum(flow_cap[techs=pv", "expression:", "<= 800", "cost[costs=co2]",
	} {
		if !strings.Contains(math, want) {
			t.Errorf("additional_math.yaml missing %q:\n%s", want, math)
		}
	}
	// The buggy list-bracket slicing must be gone.
	if strings.Contains(math, "techs=[pv") || strings.Contains(math, "costs=[co2") {
		t.Errorf("additional_math.yaml still emits invalid list-bracket slicing:\n%s", math)
	}
	if _, err := os.Stat(filepath.Join(dir, "_constraints.json")); err == nil {
		t.Errorf("sidecar should NOT be written when custom math is on")
	}
}

func TestCalliopeCustomMathOptional(t *testing.T) {
	j := loadSampleFor(t, model.TargetCalliope)
	off := false
	j.Experiment.CustomMath = &off
	dir := t.TempDir()
	if _, err := (calliope.Calliope{}).Emit(&j, dir); err != nil {
		t.Fatalf("emit: %v", err)
	}
	// custom_math=false only opts the PORTABLE constraints out of the math file
	// (they land in the sidecar); physics like the chp ratio pinning still
	// renders — it is part of the model, not user math.
	math := readFile(t, dir, "additional_math.yaml")
	if strings.Contains(math, "max_vre_capacity") || strings.Contains(math, "co2_cap") {
		t.Errorf("portable constraints must NOT be in the math file when custom_math=false:\n%s", math)
	}
	if !strings.Contains(math, "meme_ratio_chp_out_heat") {
		t.Errorf("ratio pinning must render regardless of custom_math:\n%s", math)
	}
	if _, err := os.Stat(filepath.Join(dir, "_constraints.json")); err != nil {
		t.Errorf("sidecar should be written when custom_math=false: %v", err)
	}
}
