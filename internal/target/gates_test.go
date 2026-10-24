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

// TestOverrideOnlyCapabilityGates: a gated feature introduced ONLY via a
// node_override (base fields untouched) must trip the same capability gates
// as a base-field usage — Technology.At merges Operation/Area/Source
// wholesale, so an override-only payload reaches the emitters all the same.
func TestOverrideOnlyCapabilityGates(t *testing.T) {
	load := func(file, tech, node string, ov model.NodeOverride) model.Job {
		t.Helper()
		j, err := scenarios.Load(file)
		if err != nil {
			t.Fatal(err)
		}
		tc := j.Model.Technologies[tech]
		tc.NodeOverrides = map[string]model.NodeOverride{node: ov}
		j.Model.Technologies[tech] = tc
		return j
	}

	cases := []struct {
		name   string
		job    model.Job
		target model.Target
		want   string
	}{
		{"override-only area rejected where unclaimed",
			load("pypsa_generators.json", "pv", "n1", model.NodeOverride{Area: &model.AreaSpec{Max: fp(100)}}),
			model.TargetPyPSA, "area constraints"},
		{"override-only source rejected where unclaimed",
			load("pypsa_generators.json", "pv", "n1", model.NodeOverride{Source: &model.SourceSpec{Cap: fp(50)}}),
			model.TargetPyPSA, "source constraints"},
		{"override-only committable rejected where unclaimed",
			load("adopt_storage.json", "batt", "n1", model.NodeOverride{Operation: &model.Operation{Committable: true}}),
			model.TargetAdOpt, "committable operation"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := target.ValidateFor(&tc.job, tc.target)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want gate error containing %q, got: %v", tc.want, err)
			}
		})
	}

	// Control: the same override-only usage passes where the target claims
	// the feature (PyPSA supports committable).
	ok := load("pypsa_generators.json", "pv", "n1", model.NodeOverride{Operation: &model.Operation{Committable: true}})
	if _, err := target.ValidateFor(&ok, model.TargetPyPSA); err != nil {
		t.Fatalf("committable override should pass for pypsa: %v", err)
	}
}

// TestFileBackedSeriesGate: a file-backed series is schema-valid and
// structurally fine, but no target can materialize it, so every target rejects
// it up front with a pointer to inline values (instead of failing at emit).
func TestFileBackedSeriesGate(t *testing.T) {
	for _, tgt := range target.Names() {
		j, err := scenarios.Load("calliope_plan.json")
		if err != nil {
			t.Fatal(err)
		}
		j.Model.Timeseries["dem"] = model.TimeSeries{Source: "file", Path: "demand.csv", Column: "n1"}
		_, err = target.ValidateFor(&j, tgt)
		if err == nil || !strings.Contains(err.Error(), `source "file" is not supported by any target`) {
			t.Errorf("%s: file-backed series should be rejected up front, got %v", tgt, err)
		}
	}
}

func fp(v float64) *float64 { return &v }
