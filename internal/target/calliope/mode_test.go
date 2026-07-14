// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package calliope_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/enerplanet/meme/internal/model"
)

// A demand time series must materialize an actual CSV (referenced in
// data_tables) so the emitted model is runnable, not just referenced.
func TestCalliopeMaterializesDemandCSV(t *testing.T) {
	dir := emitCalliopeDir(t, loadSampleFor(t, model.TargetCalliope))
	model := readFile(t, dir, "model.yaml")

	// Find the data-table CSV the model.yaml points at.
	if !strings.Contains(model, "data_tables:") {
		t.Skip("sample emits no data tables")
	}
	var csv string
	for _, line := range strings.Split(model, "\n") {
		if i := strings.Index(line, "data: "); i >= 0 && strings.HasSuffix(strings.TrimSpace(line), ".csv") {
			csv = strings.TrimSpace(line[i+len("data: "):])
			break
		}
	}
	if csv == "" {
		t.Fatal("no data-table CSV referenced in model.yaml")
	}
	if _, err := os.Stat(filepath.Join(dir, csv)); err != nil {
		t.Fatalf("referenced data table %q was not written: %v", csv, err)
	}
	body := readFile(t, dir, csv)
	// Header rows name the column dims; first data row is a timestamp,value pair.
	if !strings.HasPrefix(body, "techs,") || !strings.Contains(body, "\nnodes,") {
		t.Errorf("CSV missing techs/nodes header rows:\n%s", body)
	}
	lines := strings.Split(strings.TrimSpace(body), "\n")
	if len(lines) < 3 {
		t.Errorf("expected header rows + at least one timestep, got:\n%s", body)
	}
}

// Canonical "plan" must emit Calliope's own mode name "base", under config.init.
func TestCalliopePlanModeIsBase(t *testing.T) {
	j := loadSampleFor(t, model.TargetCalliope) // default mode == plan
	y := emitCalliope(t, j)
	if !strings.Contains(y, "mode: base") {
		t.Errorf("expected `mode: base` in model.yaml; got:\n%s", y)
	}
	if strings.Contains(y, "mode: plan") {
		t.Errorf("canonical `plan` leaked into model.yaml (should be `base`)")
	}
}

// Canonical "alternatives" must emit `mode: spores` and a launchable
// config.solve.spores block with the default iteration count.
func TestCalliopeAlternativesEmitsSpores(t *testing.T) {
	j := loadSampleFor(t, model.TargetCalliope)
	j.Experiment.Mode = model.ModeAlternatives
	y := emitCalliope(t, j)

	for _, want := range []string{"mode: spores", "spores:", "number: 3"} {
		if !strings.Contains(y, want) {
			t.Errorf("expected %q in model.yaml; got:\n%s", want, y)
		}
	}
	if strings.Contains(y, "mode: alternatives") {
		t.Errorf("canonical `alternatives` leaked into model.yaml (should be `spores`)")
	}
}

// experiment.solver.options.spores overrides/extends the default spores config.
func TestCalliopeSporesOptionOverride(t *testing.T) {
	j := loadSampleFor(t, model.TargetCalliope)
	j.Experiment.Mode = model.ModeAlternatives
	j.Experiment.Solver.Options = map[string]any{
		"spores": map[string]any{
			"number":             10,
			"tracking_parameter": "spores_tracker",
		},
	}
	y := emitCalliope(t, j)
	for _, want := range []string{"mode: spores", "number: 10", "tracking_parameter: spores_tracker"} {
		if !strings.Contains(y, want) {
			t.Errorf("expected %q in model.yaml; got:\n%s", want, y)
		}
	}
	if strings.Contains(y, "number: 3") {
		t.Errorf("override should replace default number: 3")
	}
}

// Whole-number coordinates must still render as floats (49.0, not 49) so
// Calliope's matching-type latitude/longitude check passes.
func TestCalliopeCoordsRenderAsFloat(t *testing.T) {
	j := loadSampleFor(t, model.TargetCalliope)
	// Sample n2 has lat 49.0 (integral) and lon 13.2 (fractional).
	y := emitCalliope(t, j)
	if !strings.Contains(y, "latitude: 49.0") {
		t.Errorf("expected `latitude: 49.0` (float), got:\n%s", y)
	}
	if strings.Contains(y, "latitude: 49\n") {
		t.Errorf("integral latitude collapsed to int-like `49`")
	}
}

// A time subset must be emitted as init.subset.timesteps (not the invalid
// init.time_subset that Calliope 0.7 rejects).
func TestCalliopeTimeSubset(t *testing.T) {
	j := loadSampleFor(t, model.TargetCalliope)
	j.Model.Time.Subset = []string{"2025-01-01T00:00", "2025-01-01T06:00"}
	y := emitCalliope(t, j)
	if !strings.Contains(y, "subset:") || !strings.Contains(y, "timesteps: [") {
		t.Errorf("expected init.subset.timesteps; got:\n%s", y)
	}
	if strings.Contains(y, "time_subset") {
		t.Errorf("invalid `time_subset` key still emitted")
	}
}

// HiGHS is not usable with Calliope; the emitter substitutes cbc and validation warns.
func TestCalliopeSolverMapsHighsToCbc(t *testing.T) {
	j := loadSampleFor(t, model.TargetCalliope)
	j.Experiment.Solver.Name = "highs"
	warns, err := validateFor(&j, model.TargetCalliope)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	found := false
	for _, w := range warns {
		if strings.Contains(w, "highs") && strings.Contains(w, "cbc") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a highs->cbc warning, got %v", warns)
	}
	y := emitCalliope(t, j)
	if !strings.Contains(y, "solver: cbc") {
		t.Errorf("expected `solver: cbc` in model.yaml, got:\n%s", y)
	}
	if strings.Contains(y, "solver: highs") {
		t.Errorf("highs must not be emitted for Calliope")
	}
}

// Emissions: a fuel-consuming tech gets a co2 cost_flow_in, and co2 is kept out
// of the objective via objective_cost_weights=0, so emission limits can bind.
func TestCalliopeEmissionCost(t *testing.T) {
	// The sample's chp consumes gas (co2_intensity 0.2) via flows.
	y := emitCalliope(t, loadSampleFor(t, model.TargetCalliope))
	for _, want := range []string{
		"cost_flow_in:",
		"index: co2",
		"objective_cost_weights:",
	} {
		if !strings.Contains(y, want) {
			t.Errorf("expected %q in model.yaml; got:\n%s", want, y)
		}
	}
	// co2 rate (gas intensity 0.2) must appear as the inflow cost.
	if !strings.Contains(y, "data: 0.2") {
		t.Errorf("expected co2 rate `data: 0.2`; got:\n%s", y)
	}
}

// A non-SPORES run must NOT emit a spores block.
func TestCalliopeNoSporesWhenPlan(t *testing.T) {
	j := loadSampleFor(t, model.TargetCalliope)
	y := emitCalliope(t, j)
	if strings.Contains(y, "spores:") {
		t.Errorf("unexpected spores block for a plan-mode model:\n%s", y)
	}
}
