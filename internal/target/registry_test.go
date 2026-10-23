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
// with a message listing the known ones, Names() is stable and sorted, and
// Defaults() leaves out the version variants.
func TestRegistryResolution(t *testing.T) {
	want := []model.Target{"adopt-net0", "calliope", "calliope-0.6", "pypsa"}
	got := target.Names()
	if len(got) != len(want) {
		t.Fatalf("Names() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Names() = %v, want %v (sorted)", got, want)
		}
	}
	wantDefaults := []model.Target{"adopt-net0", "calliope", "pypsa"}
	if gotDefaults := target.Defaults(); len(gotDefaults) != len(wantDefaults) {
		t.Fatalf("Defaults() = %v, want %v", gotDefaults, wantDefaults)
	} else {
		for i := range wantDefaults {
			if gotDefaults[i] != wantDefaults[i] {
				t.Fatalf("Defaults() = %v, want %v", gotDefaults, wantDefaults)
			}
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

// TestVersionedTargets: the Calliope releases are separate targets with their
// own framework version; "calliope-0.7" is an alias of the default "calliope"
// (resolving everywhere a name is accepted, never listed as its own target),
// and "calliope-0.6" is a variant selected only by name.
func TestVersionedTargets(t *testing.T) {
	versions := target.Versions()
	for name, want := range map[string]string{"calliope": "0.7.0", "calliope-0.6": "0.6.8", "pypsa": "1.2.4", "adopt-net0": "0.1.10"} {
		if versions[name] != want {
			t.Errorf("Versions()[%s] = %q, want %q", name, versions[name], want)
		}
	}
	if got := target.Resolve("calliope-0.7"); got != model.TargetCalliope {
		t.Errorf("Resolve(calliope-0.7) = %s, want calliope", got)
	}
	if got := target.Resolve("calliope-0.6"); got != model.TargetCalliope06 {
		t.Errorf("Resolve(calliope-0.6) = %s (a variant is its own target)", got)
	}
	impl, err := target.For("calliope-0.7")
	if err != nil || impl.Name() != model.TargetCalliope {
		t.Fatalf("For(calliope-0.7) = %v, %v; want the calliope target", impl, err)
	}
	if !target.Known("calliope-0.7") {
		t.Error("Known(calliope-0.7) = false")
	}
	if aliases := target.Aliases(); aliases["calliope-0.7"] != model.TargetCalliope || len(aliases) != 1 {
		t.Errorf("Aliases() = %v, want {calliope-0.7: calliope}", aliases)
	}
	for _, n := range target.Names() {
		if n == "calliope-0.7" {
			t.Error("an alias must not appear in Names()")
		}
	}
	v06, _ := target.For(model.TargetCalliope06)
	if variant, ok := v06.(target.Variant); !ok || variant.VariantOf() != model.TargetCalliope {
		t.Errorf("calliope-0.6 must be a Variant of calliope")
	}
	if _, ok := impl.(target.Variant); ok {
		t.Error("the default calliope target must not be a Variant")
	}
	// Validation through an alias runs against the aliased target: the
	// native-block warnings name the canonical target, not the alias.
	j, err := scenarios.SampleFor(model.TargetCalliope)
	if err != nil {
		t.Fatal(err)
	}
	warns, err := target.ValidateFor(&j, "calliope-0.7")
	if err != nil {
		t.Fatalf("ValidateFor via alias: %v", err)
	}
	if joined := strings.Join(warns, "\n"); !strings.Contains(joined, `ignored when emitting for "calliope"`) {
		t.Errorf("alias validation should report the canonical target in its warnings, got %v", warns)
	}
}

// TestCapabilityMatrixAggregation: the aggregated matrix mirrors each
// implementation's own profile exactly.
func TestCapabilityMatrixAggregation(t *testing.T) {
	matrix := target.CapabilityMatrix()
	if len(matrix) != 4 {
		t.Fatalf("matrix has %d targets, want 4", len(matrix))
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
