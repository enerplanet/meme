// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package pypsa_test

import (
	"strings"
	"testing"

	"github.com/enerplanet/meme/internal/scenarios"
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

// TestMaterializeScalarOnlySnapshots: a model without any time series still
// gets the snapshot index model.time describes (2025-01-01 .. 2025-01-02
// hourly, inclusive: 25 steps), so PyPSA solves the same horizon as the other
// targets instead of its single default snapshot.
func TestMaterializeScalarOnlySnapshots(t *testing.T) {
	j, err := scenarios.Load("pypsa_trade.json") // scalar demand, no series
	if err != nil {
		t.Fatal(err)
	}
	if len(j.Model.Timeseries) != 0 {
		t.Fatalf("precondition: the scenario must carry no series, got %d", len(j.Model.Timeseries))
	}
	dir := t.TempDir()
	if _, err := (pypsa.Emitter{}).Emit(&j, dir); err != nil {
		t.Fatalf("emit: %v", err)
	}
	if err := pypsa.MaterializeTimeSeries(&j.Model, dir); err != nil {
		t.Fatalf("materialize: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(readFile(t, dir, "snapshots.csv")), "\n")
	if len(lines) != 1+25 {
		t.Fatalf("snapshots.csv has %d rows, want header + 25 hourly snapshots", len(lines))
	}
	if lines[0] != "name" || lines[1] != "2025-01-01 00:00:00" || lines[25] != "2025-01-02 00:00:00" {
		t.Errorf("snapshot index wrong: %q ... %q", lines[:2], lines[25])
	}
}
