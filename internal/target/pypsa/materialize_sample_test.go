// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package pypsa_test

import (
	"strings"
	"testing"

	"github.com/enerplanet/meme/internal/target/pypsa"
)

// TestMaterializeTimeSeries: the raw emitter plus explicit materialization
// produces the per-attribute snapshot CSVs (the PyPSA target's Emit does both).
func TestMaterializeTimeSeries(t *testing.T) {
	j := loadSample(t)
	dir := t.TempDir()
	if _, err := (pypsa.Emitter{}).Emit(&j, dir); err != nil {
		t.Fatalf("emit: %v", err)
	}
	if err := pypsa.MaterializeTimeSeries(&j.Model, dir); err != nil {
		t.Fatalf("materialize: %v", err)
	}
	loads := readFile(t, dir, "loads-p_set.csv")
	if !strings.Contains(loads, "elec_demand") || !strings.Contains(loads, "40") {
		t.Errorf("loads-p_set.csv wrong:\n%s", loads)
	}
	gens := readFile(t, dir, "generators-p_max_pu.csv")
	if !strings.Contains(gens, "pv@n1") || !strings.Contains(gens, "0.6") {
		t.Errorf("generators-p_max_pu.csv wrong:\n%s", gens)
	}
	snaps := readFile(t, dir, "snapshots.csv")
	if strings.Count(snaps, "\n") < 3 {
		t.Errorf("snapshots.csv too short:\n%s", snaps)
	}
}
