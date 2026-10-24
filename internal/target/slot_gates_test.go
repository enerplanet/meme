// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package target_test

import (
	"strings"
	"testing"

	"github.com/enerplanet/meme/internal/emit"
	"github.com/enerplanet/meme/internal/model"
	"github.com/enerplanet/meme/internal/scenarios"
	"github.com/enerplanet/meme/internal/target"
	_ "github.com/enerplanet/meme/internal/target/all"
)

// TestTimeSubsetGate: time.subset narrows the horizon on Calliope only. The
// other targets have no slot for it, and an ignored subset would solve a
// different horizon than the payload describes — ADR-0002 forbids dropping
// it without a word, so they reject it with a pointer to time.start/end.
func TestTimeSubsetGate(t *testing.T) {
	withSubset := func(file string) model.Job {
		t.Helper()
		j, err := scenarios.Load(file)
		if err != nil {
			t.Fatal(err)
		}
		j.Model.Time.Subset = []string{j.Model.Time.Start, j.Model.Time.End}
		return j
	}
	for _, tc := range []struct {
		file   string
		target model.Target
	}{
		{"pypsa_generators.json", model.TargetPyPSA},
		{"adopt_storage.json", model.TargetAdOpt},
	} {
		j := withSubset(tc.file)
		_, err := target.ValidateFor(&j, tc.target)
		if err == nil || !strings.Contains(err.Error(), "time.subset") || !strings.Contains(err.Error(), "time.start/end") {
			t.Fatalf("%s: want the time.subset gate naming time.start/end, got %v", tc.target, err)
		}
	}
	// Control: both Calliope releases honor the subset.
	for _, tgt := range []model.Target{model.TargetCalliope, model.TargetCalliope06} {
		j := withSubset("calliope_plan.json")
		if _, err := target.ValidateFor(&j, tgt); err != nil {
			t.Fatalf("%s honors time.subset, got %v", tgt, err)
		}
	}
}

// TestPurchaseCostGate: a purchase cost is a fixed charge per purchased unit
// (Calliope's cost_purchase). PyPSA and AdOpT-NET0 have no slot for it and
// reject it instead of solving a cheaper objective than the payload prices;
// a node-override-only purchase cost trips the same gate.
func TestPurchaseCostGate(t *testing.T) {
	withPurchase := func(file, tech string, viaOverride bool) model.Job {
		t.Helper()
		j, err := scenarios.Load(file)
		if err != nil {
			t.Fatal(err)
		}
		tc := j.Model.Technologies[tech]
		if viaOverride {
			tc.NodeOverrides = map[string]model.NodeOverride{tc.Node[0]: {
				Costs: map[string]model.CostClass{model.PrimaryCostClass: {Purchase: fp(1000)}},
			}}
		} else {
			if tc.Costs == nil {
				tc.Costs = map[string]model.CostClass{}
			}
			c := tc.Costs[model.PrimaryCostClass]
			c.Purchase = fp(1000)
			tc.Costs[model.PrimaryCostClass] = c
		}
		j.Model.Technologies[tech] = tc
		return j
	}
	for _, tc := range []struct {
		name        string
		file, tech  string
		viaOverride bool
		target      model.Target
	}{
		{"pypsa base", "pypsa_generators.json", "pv", false, model.TargetPyPSA},
		{"pypsa override-only", "pypsa_generators.json", "pv", true, model.TargetPyPSA},
		{"adopt base", "adopt_storage.json", "batt", false, model.TargetAdOpt},
	} {
		t.Run(tc.name, func(t *testing.T) {
			j := withPurchase(tc.file, tc.tech, tc.viaOverride)
			_, err := target.ValidateFor(&j, tc.target)
			want := `tech "` + tc.tech + `": costs.monetary.purchase is not supported by target "` + string(tc.target) + `"`
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("want gate error containing %q, got: %v", want, err)
			}
		})
	}
	// Control: the committable scenario prices a purchase cost and validates
	// on both Calliope releases, which emit cost_purchase.
	for _, tgt := range []model.Target{model.TargetCalliope, model.TargetCalliope06} {
		j, err := scenarios.Load("calliope_committable.json")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := target.ValidateFor(&j, tgt); err != nil {
			t.Fatalf("%s honors purchase, got %v", tgt, err)
		}
	}
}

// TestAreaPerCapacityWarning: Calliope 0.7 has no mapping for
// area.per_capacity yet and says so in a warning (the field is otherwise
// dropped); Calliope 0.6 emits resource_area_per_energy_cap and stays quiet.
func TestAreaPerCapacityWarning(t *testing.T) {
	withPerCapacity := func() model.Job {
		t.Helper()
		j, err := scenarios.Load("calliope_plan.json")
		if err != nil {
			t.Fatal(err)
		}
		for _, id := range emit.Keys(j.Model.Technologies) {
			tc := j.Model.Technologies[id]
			if tc.Role != model.RoleSupply {
				continue
			}
			tc.Area = &model.AreaSpec{PerCapacity: fp(2)}
			j.Model.Technologies[id] = tc
			return j
		}
		t.Fatal("calliope_plan.json has no supply technology")
		return j
	}
	has := func(warns []string, s string) bool {
		for _, w := range warns {
			if strings.Contains(w, s) {
				return true
			}
		}
		return false
	}
	j := withPerCapacity()
	warns, err := target.ValidateFor(&j, model.TargetCalliope)
	if err != nil {
		t.Fatalf("calliope: %v", err)
	}
	if !has(warns, "area.per_capacity is not emitted for calliope 0.7") {
		t.Fatalf("calliope 0.7 must warn about area.per_capacity, got %q", warns)
	}
	j = withPerCapacity()
	warns, err = target.ValidateFor(&j, model.TargetCalliope06)
	if err != nil {
		t.Fatalf("calliope-0.6: %v", err)
	}
	if has(warns, "area.per_capacity") {
		t.Fatalf("calliope-0.6 emits resource_area_per_energy_cap and must not warn, got %q", warns)
	}
}
