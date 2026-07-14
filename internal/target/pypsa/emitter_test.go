// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package pypsa_test

import (
	"strings"
	"testing"

	"github.com/enerplanet/meme/internal/model"
)

func TestMultiPortLink(t *testing.T) {
	csv := readFile(t, emitSample(t), "links.csv")
	// CHP: bus0=gas, bus1=electricity(eff 0.4), bus2=heat(eff2 0.45).
	for _, want := range []string{"bus2", "efficiency2", "n1::gas", "n1::heat", "0.4", "0.45"} {
		if !strings.Contains(csv, want) {
			t.Errorf("links.csv missing %q:\n%s", want, csv)
		}
	}
}

func TestEmissionLimitGlobalConstraint(t *testing.T) {
	csv := readFile(t, emitSample(t), "global_constraints.csv")
	for _, want := range []string{"co2_cap", "primary_energy", "co2_emissions", "<=", "1e+06"} {
		if !strings.Contains(csv, want) {
			t.Errorf("global_constraints.csv missing %q:\n%s", want, csv)
		}
	}
}

func TestTradeGenerators(t *testing.T) {
	csv := readFile(t, emitSample(t), "generators.csv")
	if !strings.Contains(csv, "grid_n1_import") || !strings.Contains(csv, "grid_n1_export") {
		t.Errorf("generators.csv missing trade rows:\n%s", csv)
	}
	if !strings.Contains(csv, "p_min_pu") || !strings.Contains(csv, "-1") {
		t.Errorf("export generator should set p_min_pu=-1:\n%s", csv)
	}
	// Import price 80 present. The export generator carries the price as a
	// POSITIVE marginal_cost (40): it only absorbs energy (p <= 0), so the
	// objective term marginal_cost*p turns negative — an export revenue. A
	// negative value here would book exports as a cost and the solver would
	// never export (verified end-to-end in test/e2e).
	if !strings.Contains(csv, "80") || !strings.Contains(csv, "grid_n1_export,n1::electricity,electricity,0,True,500,,40,") {
		t.Errorf("expected import price 80 and export marginal_cost +40:\n%s", csv)
	}
}

func TestCommittableColumns(t *testing.T) {
	csv := readFile(t, emitSample(t), "generators.csv")
	for _, want := range []string{"committable", "start_up_cost", "min_up_time", "ramp_limit_up", "5000"} {
		if !strings.Contains(csv, want) {
			t.Errorf("generators.csv missing committable column %q:\n%s", want, csv)
		}
	}
}

func TestStorageDetailColumns(t *testing.T) {
	csv := readFile(t, emitSample(t), "storage_units.csv")
	for _, want := range []string{"standing_loss", "cyclic_state_of_charge", "state_of_charge_initial", "True"} {
		if !strings.Contains(csv, want) {
			t.Errorf("storage_units.csv missing %q:\n%s", want, csv)
		}
	}
}

func TestAreaSourceCapabilityGating(t *testing.T) {
	j := loadSample(t)
	// Attach an area constraint to pv (Calliope-only feature).
	pv := j.Model.Technologies["pv"]
	max := 1500.0
	pv.Area = &model.AreaSpec{Max: &max}
	j.Model.Technologies["pv"] = pv

	if _, err := validateFor(&j, model.TargetPyPSA); err == nil {
		t.Error("expected pypsa to reject area constraints")
	}
	// Calliope supports area, so validation passes.
	jc := loadSampleFor(t, model.TargetCalliope)
	pvc := jc.Model.Technologies["pv"]
	pvc.Area = &model.AreaSpec{Max: &max}
	jc.Model.Technologies["pv"] = pvc
	if _, err := validateFor(&jc, model.TargetCalliope); err != nil {
		t.Errorf("calliope should accept area constraints: %v", err)
	}
}
