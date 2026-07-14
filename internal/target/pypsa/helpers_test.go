// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package pypsa_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/enerplanet/meme/internal/model"
	"github.com/enerplanet/meme/internal/scenarios"
	"github.com/enerplanet/meme/internal/target"
	_ "github.com/enerplanet/meme/internal/target/all"
	"github.com/enerplanet/meme/internal/target/pypsa"
)

func loadSample(t *testing.T) model.Job {
	t.Helper()
	j, err := scenarios.LoadSample()
	if err != nil {
		t.Fatalf("load sample: %v", err)
	}
	return j
}

func loadSampleFor(t *testing.T, tgt model.Target) model.Job {
	t.Helper()
	j, err := scenarios.SampleFor(tgt)
	if err != nil {
		t.Fatalf("load sample for %s: %v", tgt, err)
	}
	return j
}

func validateFor(j *model.Job, tgt model.Target) ([]string, error) {
	return target.ValidateFor(j, tgt)
}

// emitSample validates + emits the sample as PyPSA (including materialized
// time series) and returns the emit dir.
func emitSample(t *testing.T) string {
	t.Helper()
	j := loadSample(t)
	if _, err := validateFor(&j, model.TargetPyPSA); err != nil {
		t.Fatalf("validate sample: %v", err)
	}
	dir := t.TempDir()
	if _, err := (pypsa.PyPSA{}).Emit(&j, dir); err != nil {
		t.Fatalf("emit: %v", err)
	}
	return dir
}

func emitSampleDir(t *testing.T) string { return emitSample(t) }

func readFile(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(b)
}
