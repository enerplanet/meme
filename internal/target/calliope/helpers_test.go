// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package calliope_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/enerplanet/meme/internal/model"
	"github.com/enerplanet/meme/internal/scenarios"
	"github.com/enerplanet/meme/internal/target"
	_ "github.com/enerplanet/meme/internal/target/all"
	"github.com/enerplanet/meme/internal/target/calliope"
)

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

// emitCalliope emits j and returns model.yaml's contents.
func emitCalliope(t *testing.T, j model.Job) string {
	t.Helper()
	return readFile(t, emitCalliopeDir(t, j), "model.yaml")
}

// emitCalliopeDir emits j into a temp dir and returns it.
func emitCalliopeDir(t *testing.T, j model.Job) string {
	t.Helper()
	dir := t.TempDir()
	if _, err := (calliope.Calliope{}).Emit(&j, dir); err != nil {
		t.Fatalf("emit: %v", err)
	}
	return dir
}

func readFile(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(b)
}
