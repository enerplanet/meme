// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package target_test

import (
	"strings"
	"testing"

	"github.com/enerplanet/meme/internal/model"
	"github.com/enerplanet/meme/internal/scenarios"
	"github.com/enerplanet/meme/internal/target"
	_ "github.com/enerplanet/meme/internal/target/all"
)

// TestRegistryResolution: every built-in target resolves, unknown names fail
// with a message listing the known ones, and Names() is stable and sorted.
func TestRegistryResolution(t *testing.T) {
	want := []model.Target{"adopt-net0", "calliope", "pypsa"}
	got := target.Names()
	if len(got) != len(want) {
		t.Fatalf("Names() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Names() = %v, want %v (sorted)", got, want)
		}
	}
	for _, name := range want {
		impl, err := target.For(name)
		if err != nil {
			t.Fatalf("For(%s): %v", name, err)
		}
		if impl.Name() != name {
			t.Errorf("For(%s).Name() = %s", name, impl.Name())
		}
		if !target.Known(name) {
			t.Errorf("Known(%s) = false", name)
		}
	}
	if _, err := target.For("nonsense"); err == nil || !strings.Contains(err.Error(), "known:") {
		t.Errorf("For(nonsense) should fail listing known targets, got %v", err)
	}
	if target.Known("nonsense") {
		t.Error("Known(nonsense) = true")
	}
}

// TestCapabilityMatrixAggregation: the aggregated matrix mirrors each
// implementation's own profile exactly.
func TestCapabilityMatrixAggregation(t *testing.T) {
	matrix := target.CapabilityMatrix()
	if len(matrix) != 3 {
		t.Fatalf("matrix has %d targets, want 3", len(matrix))
	}
	for _, name := range target.Names() {
		impl, _ := target.For(name)
		row := matrix[string(name)]
		caps := impl.Capabilities()
		if len(row) != len(caps) {
			t.Errorf("%s: matrix row has %d features, impl has %d", name, len(row), len(caps))
		}
		for f, ok := range caps {
			if row[string(f)] != ok {
				t.Errorf("%s/%s: matrix %v != impl %v", name, f, row[string(f)], ok)
			}
		}
		if !target.Supports(name, model.FeatModePlan) {
			t.Errorf("%s must support mode:plan", name)
		}
	}
}

// TestValidateForPipeline: the registry orchestration rejects unknown targets,
// runs structural validation, applies generic capability gating, and reaches
// the per-target hooks.
func TestValidateForPipeline(t *testing.T) {
	j, err := scenarios.LoadSample()
	if err != nil {
		t.Fatal(err)
	}

	// unknown target
	if _, err := target.ValidateFor(&j, "nonsense"); err == nil {
		t.Error("unknown target must be rejected")
	}

	// structural failure (broken reference) surfaces before any gating
	broken := j
	brokenTech := broken.Model.Technologies["pv"]
	brokenTech.Node = model.StringList{"missing-node"}
	broken.Model.Technologies = map[string]model.Technology{"pv": brokenTech}
	if _, err := target.ValidateFor(&broken, model.TargetPyPSA); err == nil || !strings.Contains(err.Error(), "missing-node") {
		t.Errorf("structural error should surface, got %v", err)
	}

	// generic capability gate: sample uses committable+constraints -> rejected
	// for adopt (committable gate fires from the generic layer).
	if _, err := target.ValidateFor(&j, model.TargetAdOpt); err == nil {
		t.Error("pypsa-shaped sample must be rejected for adopt-net0")
	}

	// hook gate: calliope rejects the UC extras (start_up_cost) — proves the
	// per-target hook runs after the generic layer passes.
	if _, err := target.ValidateFor(&j, model.TargetCalliope); err == nil || !strings.Contains(err.Error(), "MILP") {
		t.Errorf("calliope hook should reject UC extras, got %v", err)
	}

	// happy path: pypsa accepts the sample and returns the native warnings the
	// registry appends after the hooks.
	warns, err := target.ValidateFor(&j, model.TargetPyPSA)
	if err != nil {
		t.Fatalf("pypsa should accept the sample: %v", err)
	}
	joined := strings.Join(warns, "\n")
	if !strings.Contains(joined, "not portable") {
		t.Errorf("the registry should append the native lock warning, got %v", warns)
	}
}

// TestObjectiveEmissionsGate: objective min_emissions is matrix-driven —
// accepted where claimed (adopt), rejected elsewhere.
func TestObjectiveEmissionsGate(t *testing.T) {
	j, err := scenarios.Load("adopt_min_emissions.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := target.ValidateFor(&j, model.TargetAdOpt); err != nil {
		t.Errorf("adopt should accept min_emissions: %v", err)
	}
	j.Model.Trade = nil // isolate the objective gate (pypsa would reject the EF-less trade anyway? keep trade removal simple)
	if _, err := target.ValidateFor(&j, model.TargetPyPSA); err == nil || !strings.Contains(err.Error(), "min_emissions") {
		t.Errorf("pypsa should reject min_emissions, got %v", err)
	}
}
